// Package security applies Tide's security level: it looks the open alerts up
// at the provider, lists them, and stops a push the level says must not go. The
// rules themselves are in internal/domain/security.
package security

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/cellestias/git-tide/internal/apperr"
	sec "github.com/cellestias/git-tide/internal/domain/security"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
)

const (
	// maxRows caps the alerts listed in a warning; `gtide security` lists them all.
	maxRows        = 12
	maxTitle       = 70
	commitTimeout  = 8 * time.Second  // a commit must not wait long for the network
	pushTimeout    = 20 * time.Second // a push can afford a little more
	checkedEnvName = "TIDE_SECURITY_CHECKED"
)

// CheckedEnv is set while `gtide push` or `gtide pr` runs git, so the pre-push
// hook, which would ask the same question again, knows it was just answered.
// It only silences the hook: the command's own check is not skippable this way.
const CheckedEnv = checkedEnvName

// Resolver identifies the provider and the hosted repository behind the remote.
type Resolver func(ctx context.Context) (provider.Provider, provider.Repo, error)

// Gate checks the alerts at a stage. touched lists the repository-relative files
// a push changes (nil at commit). A nil error means go ahead; a policy error
// means stop. Warnings are printed on stderr.
type Gate func(ctx context.Context, stage sec.Stage, touched []string) error

// NewGate builds the check for level. Nothing is resolved or asked until it is
// called at a stage the level looks at, so an unconfigured or offline
// repository pays nothing at level off.
func NewGate(level sec.Level, resolve Resolver, p output.Printer) Gate {
	return func(ctx context.Context, stage sec.Stage, touched []string) error {
		if !sec.Wanted(level, stage) {
			return nil
		}
		timeout := commitTimeout
		if stage == sec.StagePush {
			timeout = pushTimeout
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		prov, repo, err := resolve(ctx)
		if err != nil {
			return unverifiable(level, stage, "the repository on the remote could not be identified: "+clean(err.Error(), 200), p)
		}
		reader, ok := prov.(provider.SecurityReader)
		if !ok {
			p.Warn("security.level is %s, but %s has no security alerts API Tide can read: nothing is checked", level, label(prov))
			return nil
		}
		report, err := reader.SecurityAlerts(ctx, repo)
		if err != nil {
			return unverifiable(level, stage, clean(err.Error(), 200), p)
		}
		d := sec.Decide(level, stage, report, touched)
		announce(p, level, repo, report, d)
		return verdict(level, d)
	}
}

func label(p provider.Provider) string {
	if p.Name() == provider.GitLab {
		return "GitLab"
	}
	return "GitHub"
}

// unverifiable handles a check that could not run at all: it blocks only a push
// at level high, which asked for a guarantee.
func unverifiable(level sec.Level, stage sec.Stage, why string, p output.Printer) error {
	p.Warn("security alerts could not be checked: %s", why)
	if level == sec.High && stage == sec.StagePush {
		return blocked(level, "the security alerts could not be checked", "fix the problem above (a token with the security_events scope in GITHUB_TOKEN, or network access), or lower `security.level` in .tide.yml")
	}
	return nil
}

// announce prints what the check found: the alerts (at every level), the sources
// that could not be read, and the alerts that this push is allowed through.
func announce(p output.Printer, level sec.Level, repo provider.Repo, report provider.SecurityReport, d sec.Decision) {
	for _, s := range d.Unverified {
		p.Warn("could not check %s alerts: %s", sourceName(s.Source), clean(s.Err.Error(), 200))
	}
	if len(d.Alerts) > 0 {
		p.Warn("%d open security alert%s on %s (%s)", len(d.Alerts), plural(len(d.Alerts)), repo, countText(d.Alerts))
		render(p.Err, d.Alerts, maxRows)
		if report.Truncated {
			p.Warn("the list was cut: GitHub has more open alerts than Tide reads")
		}
	}
	if len(d.Exempt) > 0 {
		p.Note("%d alert%s on files this push changes: treated as a fix, so it is not held back", len(d.Exempt), plural(len(d.Exempt)))
	}
}

// verdict turns a decision into the error that stops a push, or nil.
func verdict(level sec.Level, d sec.Decision) error {
	if !d.Block {
		return nil
	}
	if d.UnverifiedBlocks {
		return blocked(level, "the security alerts could not be fully checked", "fix the problem above (a token with the security_events scope in GITHUB_TOKEN), or lower `security.level` in .tide.yml")
	}
	reason := fmt.Sprintf("%d open alert%s (%s)", len(d.Blockers), plural(len(d.Blockers)), countText(d.Blockers))
	paths := affected(d.Blockers)
	hint := "fix them: a push that changes the file of an alert, such as " + paths + ", is allowed so the fix can be sent. `gtide security` lists them"
	return blocked(level, reason, hint)
}

func blocked(level sec.Level, reason, hint string) error {
	return apperr.New(apperr.CodePolicy, fmt.Sprintf("push blocked by security level %s: %s", level, reason), hint)
}

// affected names up to two files that hold blocking alerts.
func affected(alerts []provider.SecurityAlert) string {
	seen := map[string]bool{}
	var paths []string
	for _, a := range alerts {
		if a.Path != "" && !seen[a.Path] {
			seen[a.Path] = true
			paths = append(paths, a.Path)
		}
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return "the affected file"
	}
	if len(paths) > 2 {
		paths = paths[:2]
	}
	return strings.Join(paths, " or ")
}

// render writes one line per alert: severity, the file (with the line), what it
// is. It writes to w, which is stderr for warnings and stdout for the listing.
func render(w io.Writer, alerts []provider.SecurityAlert, max int) {
	shown := alerts
	if max > 0 && len(shown) > max {
		shown = shown[:max]
	}
	wsev, wloc := 0, 0
	for _, a := range shown {
		wsev = maxInt(wsev, len(a.Severity))
		wloc = maxInt(wloc, len(location(a)))
	}
	for _, a := range shown {
		_, _ = fmt.Fprintf(w, "    %-*s  %-*s  %s\n", wsev, strings.ToUpper(a.Severity), wloc, location(a), what(a))
	}
	if len(alerts) > len(shown) {
		_, _ = fmt.Fprintf(w, "    ... and %d more (`gtide security` lists them all)\n", len(alerts)-len(shown))
	}
}

func location(a provider.SecurityAlert) string {
	switch {
	case a.Path == "":
		return "-"
	case a.Line > 0:
		return fmt.Sprintf("%s:%d", clean(a.Path, 120), a.Line)
	}
	return clean(a.Path, 120)
}

// what describes an alert: the package (or the source) and the title.
func what(a provider.SecurityAlert) string {
	head := a.Package
	if head == "" {
		head = a.Source
	}
	text := clean(head, 60) + ": " + clean(a.Title, maxTitle)
	if a.FixedIn != "" {
		text += " (fixed in " + clean(a.FixedIn, 40) + ")"
	}
	return text
}

func sourceName(source string) string {
	if source == provider.SourceCodeScanning {
		return "code scanning"
	}
	return "Dependabot"
}

// countText is "1 critical, 2 high", most severe first.
func countText(alerts []provider.SecurityAlert) string {
	counts := sec.Counts(alerts)
	var parts []string
	for _, s := range []string{provider.SeverityCritical, provider.SeverityHigh, provider.SeverityMedium, provider.SeverityLow} {
		if counts[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
		}
	}
	return strings.Join(parts, ", ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// clean strips control characters, so text that comes from the provider cannot
// inject terminal escape sequences, and cuts it to max runes.
func clean(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

// ErrUnsupported means the provider cannot list security alerts.
var ErrUnsupported = errors.New("the provider has no security alerts API Tide can read")
