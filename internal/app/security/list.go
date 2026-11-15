package security

import (
	"context"
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
		return apperr.New(apperr.CodeExternal, "cannot read the security alerts: "+clean(unverified[0].Err.Error(), 200),
			"a token with the security_events scope (or access to the repository) in GITHUB_TOKEN is needed")
	}
	alerts := sec.Sorted(report.Alerts)
	if asJSON {
		return writeJSON(p, prov, repo, level, alerts, report)
	}

	for _, s := range unverified {
		p.Warn("could not check %s alerts: %s", sourceName(s.Source), clean(s.Err.Error(), 200))
	}
	if len(alerts) == 0 {
		p.Success("No open security alerts on %s%s", repo, notEnabledNote(report))
		return nil
	}
	p.Line("%d open security alert%s on %s (%s)", len(alerts), plural(len(alerts)), repo, countText(alerts))
	rows := [][]string{{"SEVERITY", "SOURCE", "FILE", "WHAT", "LINK"}}
	for _, a := range alerts {
		rows = append(rows, []string{strings.ToUpper(a.Severity), a.Source, location(a), what(a), clean(a.URL, 200)})
	}
	p.Table(rows, nil)
	if report.Truncated {
		p.Warn("the list was cut: there are more open alerts than Tide reads")
	}
	if level != sec.Off {
		p.Note("security.level is %s", level)
	}
	return nil
}

func notEnabledNote(r provider.SecurityReport) string {
	var off []string
	for _, s := range r.Sources {
		if s.Err == nil && !s.Enabled {
			off = append(off, sourceName(s.Source))
		}
	}
	if len(off) == 0 {
		return ""
	}
	return " (" + strings.Join(off, " and ") + " is not enabled there)"
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
}

func writeJSON(p output.Printer, prov provider.Provider, repo provider.Repo, level sec.Level, alerts []provider.SecurityAlert, report provider.SecurityReport) error {
	out := struct {
		Provider  string         `json:"provider"`
		Repo      string         `json:"repo"`
		Level     string         `json:"level"`
		Alerts    []jsonAlert    `json:"alerts"`
		Counts    map[string]int `json:"counts"`
		Sources   []jsonSource   `json:"sources"`
		Truncated bool           `json:"truncated"`
	}{Provider: prov.Name(), Repo: repo.String(), Level: string(level), Alerts: []jsonAlert{}, Counts: map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0}, Sources: []jsonSource{}, Truncated: report.Truncated}
	for k, v := range sec.Counts(alerts) {
		out.Counts[k] = v
	}
	for _, a := range alerts {
		out.Alerts = append(out.Alerts, jsonAlert{a.Source, a.Number, a.Severity, clean(a.Title, 300), a.Package, a.Path, a.Line, a.FixedIn, a.URL})
	}
	for _, s := range report.Sources {
		js := jsonSource{Source: s.Source, Status: "ok"}
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
