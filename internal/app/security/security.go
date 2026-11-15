// Package security applies Tide's security level: it looks the open alerts up
// at the provider, lists them, and stops a push the level says must not go. The
// rules themselves are in internal/domain/security.
package security

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	sec "github.com/cellestias/git-tide/internal/domain/security"
	"github.com/cellestias/git-tide/internal/git/repository"
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

// Touched lists the files a push of rev (the tip of branch) stands for: what
// the push sends, and what the branch changes against its base, which is what
// a pull request of it proposes. The second part is what lets a fix that was
// already pushed still go through when its pull request is opened, or when a
// later commit is pushed to the same branch. remote selects whose tracking refs
// count as "already sent" (empty: every remote's). Anything that cannot be read
// is left out, so a failure only ever makes the check stricter.
func Touched(ctx context.Context, repo *repository.Repository, cfg domain.Config, remote, branch, rev string) []string {
	seen := map[string]bool{}
	var paths []string
	add := func(list []string) {
		for _, p := range list {
			if !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	if sent, err := repo.PushedPaths(ctx, remote, rev); err == nil {
		add(sent)
	}
	if base := cfg.BaseFor(branch); base != branch {
		for _, ref := range []string{"refs/remotes/" + cfg.Remote + "/" + base, "refs/heads/" + base} {
			if ok, err := repo.CommitExists(ctx, ref); err == nil && ok {
				if changed, err := repo.ChangedSince(ctx, ref, rev); err == nil {
					add(changed)
				}
				break
			}
		}
	}
	return paths
}

// MarkChecked tells the pre-push hook, for as long as the returned function has
// not been called, that the check just ran. Call it before running `git push`.
func MarkChecked() (undo func()) {
	prev, had := os.LookupEnv(CheckedEnv)
	_ = os.Setenv(CheckedEnv, "1")
	return func() {
		if had {
			_ = os.Setenv(CheckedEnv, prev)
		} else {
			_ = os.Unsetenv(CheckedEnv)
		}
	}
}

// AlreadyChecked reports whether a command that runs git push has just checked.
func AlreadyChecked() bool { return os.Getenv(CheckedEnv) == "1" }

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
		announce(p, level, stage, repo, report, d)
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
		return blocked(level, "the security alerts could not be checked", "fix the problem above (the token and what it must be allowed to read are in the README, Security alerts; or the network), or lower `security.level` in .tide.yml")
	}
	return nil
}

// announce prints what the check found: the alerts (at every level), the sources
// that could not be read, and the alerts that this push is allowed through.
func announce(p output.Printer, level sec.Level, stage sec.Stage, repo provider.Repo, report provider.SecurityReport, d sec.Decision) {
	for _, s := range d.Unverified {
		p.Warn("could not check %s alerts: %s", sourceName(s.Source), clean(s.Err.Error(), 200))
	}
	if stage == sec.StagePush && report.NothingChecked() {
		// an empty list here is not a clean bill of health: say so, once, where it matters
		p.Warn("security.level is %s, but nothing is being checked on %s: %s", level, repo, notEnabledText(report))
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
		return blocked(level, "the security alerts could not be fully checked", "fix the problem above (the token and what it must be allowed to read are in the README, Security alerts), or lower `security.level` in .tide.yml")
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

// render writes the alerts a person has to act on, one line each: severity, the
// file (with the line), what it is. Dependabot alerts of the same package in the
// same manifest are one line with the upgrade that fixes them all, so twelve
// alerts on two packages read as two tasks. It writes to w, stderr for warnings.
func render(w io.Writer, alerts []provider.SecurityAlert, max int) {
	type line struct {
		rank              int
		severity, loc, in string
		path              string
	}
	var lines []line
	type id struct {
		source string
		number int
	}
	grouped := map[id]bool{} // the alerts folded into a group
	collapsed := false
	for _, g := range sec.Groups(alerts) {
		for _, a := range g.Alerts {
			grouped[id{a.Source, a.Number}] = true
		}
		collapsed = true
		lines = append(lines, line{sec.Rank(g.Severity), g.Severity, clean(g.Path, 120), groupText(g), g.Path})
	}
	for _, a := range alerts {
		if !grouped[id{a.Source, a.Number}] {
			lines = append(lines, line{sec.Rank(a.Severity), a.Severity, location(a), what(a), a.Path})
		}
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].rank != lines[j].rank {
			return lines[i].rank > lines[j].rank
		}
		return lines[i].path < lines[j].path
	})
	shown := lines
	if max > 0 && len(shown) > max {
		shown = shown[:max]
	}
	wsev, wloc := 0, 0
	for _, l := range shown {
		wsev = maxInt(wsev, len(l.severity))
		wloc = maxInt(wloc, len(l.loc))
	}
	for _, l := range shown {
		_, _ = fmt.Fprintf(w, "    %-*s  %-*s  %s\n", wsev, strings.ToUpper(l.severity), wloc, l.loc, l.in)
	}
	switch {
	case len(lines) > len(shown):
		_, _ = fmt.Fprintf(w, "    ... and %d more (`gtide security` lists every alert)\n", len(lines)-len(shown))
	case collapsed:
		_, _ = fmt.Fprintf(w, "    (`gtide security` lists every alert)\n")
	}
}

// groupText is the line of a package with several alerts: how many, and the
// upgrade that fixes all of them.
func groupText(g sec.Group) string {
	text := fmt.Sprintf("%s: %d alerts", clean(g.Package, 60), len(g.Alerts))
	switch {
	case g.UpgradeTo == "":
		return text + ", no fixed version yet"
	case g.Unfixed > 0:
		return fmt.Sprintf("%s, upgrade to %s or later (%d have no fix yet)", text, clean(g.UpgradeTo, 40), g.Unfixed)
	}
	return fmt.Sprintf("%s, upgrade to %s or later", text, clean(g.UpgradeTo, 40))
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
