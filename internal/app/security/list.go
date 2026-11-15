package security

import (
	"context"
	"fmt"
	"strings"

	"github.com/cellestias/git-tide/internal/apperr"
	sec "github.com/cellestias/git-tide/internal/domain/security"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
)

// List prints the open alerts of the repository, whatever the security level:
// the command is for looking, the level is for stopping. It fails when no source
// could be read, so a script can tell "no alerts" from "could not look".
func List(ctx context.Context, prov provider.Provider, repo provider.Repo, level sec.Level, asJSON bool, p output.Printer) error {
	reader, ok := prov.(provider.SecurityReader)
	if !ok {
		return apperr.New(apperr.CodeConfig, label(prov)+" has no security alerts API Tide can read",
			"security alerts are read from GitHub (Dependabot and code scanning)")
	}
	ctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	report, err := reader.SecurityAlerts(ctx, repo)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read the security alerts", "check your network and token", err)
	}
	unverified := report.Unverified()
	if len(unverified) == len(report.Sources) && len(unverified) > 0 {
		return apperr.New(apperr.CodeExternal, "cannot read the security alerts: "+clean(unverified[0].Err.Error(), 200), tokenHint(unverified))
	}
	alerts := sec.Sorted(report.Alerts)
	if asJSON {
		return writeJSON(p, prov, repo, level, alerts, report)
	}

	d := sec.Decide(level, sec.StagePush, report, nil)
	switch {
	case len(alerts) > 0:
		p.Line("%d open security alert%s on %s (%s)", len(alerts), plural(len(alerts)), repo, countText(alerts))
		rows := [][]string{{"SEVERITY", "SOURCE", "FILE", "WHAT", "LINK"}}
		for _, a := range alerts {
			rows = append(rows, []string{strings.ToUpper(a.Severity), a.Source, location(a), what(a), clean(a.URL, 200)})
		}
		p.Table(rows, nil)
		if report.Truncated {
			p.Warn("the list was cut: there are more open alerts than Tide reads")
		}
	case report.NothingChecked():
		// not a green tick: nothing is being looked at, so "no alerts" would mislead
		p.Warn("Nothing is being checked on %s: no source of alerts is enabled there", repo)
	case len(unverified) > 0:
		p.Warn("No open alerts found on %s, but not everything could be read", repo)
	default:
		p.Success("No open security alerts on %s", repo)
	}
	details(p, level, report, alerts, d)
	return nil
}

// details prints what was looked at and what the level makes of it, one fact per line.
func details(p output.Printer, level sec.Level, report provider.SecurityReport, alerts []provider.SecurityAlert, d sec.Decision) {
	open := map[string]int{}
	for _, a := range alerts {
		open[a.Source]++
	}
	row := func(label, format string, args ...any) { p.Info("%-16s"+format, append([]any{label}, args...)...) }
	for _, g := range sec.Fixes(alerts) {
		switch {
		case g.UpgradeTo == "":
			row("to fix", "%s: %s has no fixed version yet (%d alert%s)", clean(g.Path, 120), clean(g.Package, 60), len(g.Alerts), plural(len(g.Alerts)))
		case g.Unfixed > 0:
			row("to fix", "%s: upgrade %s to %s or later (%d alert%s; %d have no fix yet)", clean(g.Path, 120), clean(g.Package, 60), clean(g.UpgradeTo, 40), len(g.Alerts), plural(len(g.Alerts)), g.Unfixed)
		default:
			row("to fix", "%s: upgrade %s to %s or later (%d alert%s)", clean(g.Path, 120), clean(g.Package, 60), clean(g.UpgradeTo, 40), len(g.Alerts), plural(len(g.Alerts)))
		}
	}
	for _, s := range report.Sources {
		switch {
		case s.Err != nil:
			row("could not read", "%s: %s", sourceName(s.Source), clean(s.Err.Error(), 200))
		case !s.Enabled:
			row("not checked", "%s: not enabled on this repository (%s)", sourceName(s.Source), enableHint(s.Source))
		default:
			row("checked", "%s: %d open", sourceName(s.Source), open[s.Source])
		}
	}
	row("level", "%s", level.Describe())
	row("push", "%s", pushText(level, report, d))
	row("alerts", "read from the default branch; a fix shows as resolved once it is merged there")
}

// enableHint says how a source that is off gets turned on.
func enableHint(source string) string {
	if source == provider.SourceCodeScanning {
		return "free on public repositories; a private one needs GitHub Code Security, a paid add-on, then Settings, Advanced Security, CodeQL analysis"
	}
	return "turn it on in the repository's Settings, Advanced Security"
}

// pushText says whether a push from here would be held back, and why.
func pushText(level sec.Level, report provider.SecurityReport, d sec.Decision) string {
	switch {
	case level == sec.Off:
		return "security.level is off: nothing is blocked"
	case d.UnverifiedBlocks:
		return "would be BLOCKED: the alerts could not be fully checked, and this level requires it"
	case d.Block:
		return fmt.Sprintf("would be BLOCKED by %d alert%s (%s); a push that changes %s is allowed so the fix can be sent",
			len(d.Blockers), plural(len(d.Blockers)), countText(d.Blockers), affected(d.Blockers))
	case report.NothingChecked():
		return "would go through (there is nothing to check)"
	}
	return "would go through"
}

// notEnabledText says which sources the repository does not use: "Dependabot is
// not enabled there", "Dependabot and code scanning are not enabled there".
func notEnabledText(r provider.SecurityReport) string {
	var off []string
	for _, s := range r.Sources {
		if s.Err == nil && !s.Enabled {
			off = append(off, sourceName(s.Source))
		}
	}
	switch len(off) {
	case 0:
		return ""
	case 1:
		return off[0] + " is not enabled there"
	}
	return strings.Join(off[:len(off)-1], ", ") + " and " + off[len(off)-1] + " are not enabled there"
}

type jsonAlert struct {
	Source   string `json:"source"`
	Number   int    `json:"number"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Package  string `json:"package"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	FixedIn  string `json:"fixed_in"`
	URL      string `json:"url"`
}

type jsonSource struct {
	Source  string `json:"source"`
	Status  string `json:"status"` // "ok", "not_enabled" or "error"
	Message string `json:"message"`
	Open    int    `json:"open"` // open alerts from this source
}

// jsonFix is what to do about one package: the upgrade that fixes its alerts.
type jsonFix struct {
	Path      string `json:"path"`
	Package   string `json:"package"`
	Severity  string `json:"severity"` // the highest of its alerts
	Alerts    int    `json:"alerts"`
	UpgradeTo string `json:"upgrade_to"` // empty when no alert has a patched version
	Unfixed   int    `json:"unfixed"`    // alerts with no patched version yet
}

// jsonPush says whether a push from here would be held back by the level.
type jsonPush struct {
	WouldBlock bool `json:"would_block"`
	Blockers   int  `json:"blockers"`
	// Unverified is true when the push would be held back only because the alerts could not be read.
	Unverified bool `json:"unverified"`
}

func writeJSON(p output.Printer, prov provider.Provider, repo provider.Repo, level sec.Level, alerts []provider.SecurityAlert, report provider.SecurityReport) error {
	out := struct {
		Provider  string         `json:"provider"`
		Repo      string         `json:"repo"`
		Level     string         `json:"level"`
		Alerts    []jsonAlert    `json:"alerts"`
		Counts    map[string]int `json:"counts"`
		Fixes     []jsonFix      `json:"fixes"`
		Sources   []jsonSource   `json:"sources"`
		Push      jsonPush       `json:"push"`
		Truncated bool           `json:"truncated"`
	}{Provider: prov.Name(), Repo: repo.String(), Level: string(level), Alerts: []jsonAlert{}, Fixes: []jsonFix{}, Counts: map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0}, Sources: []jsonSource{}, Truncated: report.Truncated}
	for k, v := range sec.Counts(alerts) {
		out.Counts[k] = v
	}
	for _, a := range alerts {
		out.Alerts = append(out.Alerts, jsonAlert{a.Source, a.Number, a.Severity, clean(a.Title, 300), a.Package, a.Path, a.Line, a.FixedIn, a.URL})
	}
	for _, g := range sec.Fixes(alerts) {
		out.Fixes = append(out.Fixes, jsonFix{g.Path, g.Package, g.Severity, len(g.Alerts), g.UpgradeTo, g.Unfixed})
	}
	d := sec.Decide(level, sec.StagePush, report, nil)
	out.Push = jsonPush{WouldBlock: d.Block, Blockers: len(d.Blockers), Unverified: d.UnverifiedBlocks}
	open := map[string]int{}
	for _, a := range alerts {
		open[a.Source]++
	}
	for _, s := range report.Sources {
		js := jsonSource{Source: s.Source, Status: "ok", Open: open[s.Source]}
		switch {
		case s.Err != nil:
			js.Status, js.Message = "error", clean(s.Err.Error(), 300)
		case !s.Enabled:
			js.Status = "not_enabled"
		}
		out.Sources = append(out.Sources, js)
	}
	if err := p.JSON(out); err != nil {
		return apperr.Wrap(apperr.CodeGeneral, "cannot write JSON", "", err)
	}
	return nil
}

// tokenHint says what to do about the failures of every source. A 401 is a token
// GitHub does not accept at all (wrong, expired, revoked), which no permission
// fixes; anything else is a token that is accepted but may not read the alerts.
func tokenHint(failed []provider.SourceResult) string {
	for _, f := range failed {
		if f.Err != nil && strings.Contains(f.Err.Error(), "(401)") {
			return "the token is not accepted: make a new one and check which one is in use (secret.tide.yml, GITHUB_TOKEN, GH_TOKEN or .env: see the README, \"Where the token comes from\")"
		}
	}
	return "the token needs to read the alerts: Dependabot alerts and Code scanning alerts (Read) for a fine-grained token, or security_events for a classic one (see the README, Security alerts)"
}
