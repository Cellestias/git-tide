package security

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/apperr"
	sec "github.com/cellestias/git-tide/internal/domain/security"
	"github.com/cellestias/git-tide/internal/github"
	"github.com/cellestias/git-tide/internal/gitlab"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
)

// fakeProvider is GitHub with a canned answer for the alerts.
type fakeProvider struct {
	*github.Client
	report provider.SecurityReport
	err    error
	asked  int
}

func (f *fakeProvider) SecurityAlerts(context.Context, provider.Repo) (provider.SecurityReport, error) {
	f.asked++
	return f.report, f.err
}

var acme = provider.Repo{Owner: "acme", Name: "tool"}

func alert(sev, path string, n int) provider.SecurityAlert {
	return provider.SecurityAlert{Source: provider.SourceDependabot, Number: n, Severity: sev, Path: path, Package: "npm:lodash", Title: "Prototype pollution", FixedIn: "4.17.21"}
}

type out struct{ stdout, stderr bytes.Buffer }

func (o *out) printer() output.Printer { return output.Printer{Out: &o.stdout, Err: &o.stderr} }

func gate(level sec.Level, f *fakeProvider, o *out) Gate {
	return NewGate(level, func(context.Context) (provider.Provider, provider.Repo, error) { return f, acme, nil }, o.printer())
}

func code(err error) int { return apperr.CodeOf(err) }

func TestTheGateAsksOnlyWhereTheLevelLooks(t *testing.T) {
	for _, tc := range []struct {
		level sec.Level
		stage sec.Stage
		asked int
	}{
		{sec.Off, sec.StageCommit, 0}, {sec.Off, sec.StagePush, 0},
		{sec.Low, sec.StageCommit, 0}, {sec.Low, sec.StagePush, 1},
		{sec.Medium, sec.StageCommit, 1}, {sec.Medium, sec.StagePush, 1},
		{sec.High, sec.StageCommit, 1}, {sec.High, sec.StagePush, 1},
	} {
		f := &fakeProvider{}
		var o out
		_ = gate(tc.level, f, &o)(t.Context(), tc.stage, nil)
		if f.asked != tc.asked {
			t.Errorf("%s at stage %d: asked %d times, want %d", tc.level, tc.stage, f.asked, tc.asked)
		}
	}
	// a level that does not look never resolves the provider either
	resolved := false
	g := NewGate(sec.Off, func(context.Context) (provider.Provider, provider.Repo, error) {
		resolved = true
		return nil, provider.Repo{}, errors.New("x")
	}, output.Printer{})
	if err := g(t.Context(), sec.StagePush, nil); err != nil || resolved {
		t.Errorf("off must not even resolve the provider: err=%v resolved=%v", err, resolved)
	}
}

func TestAWarningListsEveryAlertWithItsFile(t *testing.T) {
	f := &fakeProvider{report: provider.SecurityReport{Alerts: []provider.SecurityAlert{
		alert("low", "go.mod", 3),
		alert("critical", "package.json", 1),
		{Source: provider.SourceCodeScanning, Number: 9, Severity: "high", Path: "src/render.js", Line: 42, Title: "Cross-site scripting"},
	}}}
	var o out
	if err := gate(sec.Low, f, &o)(t.Context(), sec.StagePush, nil); err != nil {
		t.Fatalf("low never blocks: %v", err)
	}
	text := o.stderr.String()
	for _, want := range []string{"3 open security alerts on acme/tool (1 critical, 1 high, 1 low)", "CRITICAL", "package.json", "npm:lodash: Prototype pollution (fixed in 4.17.21)", "src/render.js:42", "code-scanning: Cross-site scripting", "go.mod"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if o.stdout.Len() != 0 {
		t.Errorf("warnings go to stderr, stdout got %q", o.stdout.String())
	}
	if strings.Index(text, "CRITICAL") > strings.Index(text, "HIGH") || strings.Index(text, "HIGH") > strings.Index(text, "LOW") {
		t.Errorf("most severe first:\n%s", text)
	}
}

func TestEachLevelStopsWhatItShould(t *testing.T) {
	only := func(sev string) *fakeProvider {
		return &fakeProvider{report: provider.SecurityReport{Alerts: []provider.SecurityAlert{alert(sev, "package.json", 1)}}}
	}
	for _, tc := range []struct {
		level sec.Level
		sev   string
		block bool
	}{
		{sec.Low, "critical", false},
		{sec.Medium, "critical", true}, {sec.Medium, "high", false}, {sec.Medium, "low", false},
		{sec.High, "critical", true}, {sec.High, "high", true}, {sec.High, "medium", true}, {sec.High, "low", true},
	} {
		var o out
		err := gate(tc.level, only(tc.sev), &o)(t.Context(), sec.StagePush, nil)
		if (err != nil) != tc.block {
			t.Errorf("%s with a %s alert: err = %v, want block=%v", tc.level, tc.sev, err, tc.block)
			continue
		}
		if err != nil {
			if code(err) != apperr.CodePolicy {
				t.Errorf("a block is a policy error (exit 4), got %d", code(err))
			}
			if !strings.Contains(err.Error(), "security level "+string(tc.level)) || !strings.Contains(err.Error(), "1 open alert (1 "+tc.sev+")") {
				t.Errorf("message = %q", err)
			}
			if h := apperr.HintOf(err); !strings.Contains(h, "package.json") || !strings.Contains(h, "gtide security") {
				t.Errorf("the hint must say how to get out: %q", h)
			}
		}
	}
}

func TestACommitWarnsButNeverStops(t *testing.T) {
	f := &fakeProvider{report: provider.SecurityReport{Alerts: []provider.SecurityAlert{alert("critical", "package.json", 1)}}}
	for _, l := range []sec.Level{sec.Medium, sec.High} {
		var o out
		if err := gate(l, f, &o)(t.Context(), sec.StageCommit, nil); err != nil {
			t.Errorf("%s: a commit must not be stopped: %v", l, err)
		}
		if !strings.Contains(o.stderr.String(), "package.json") {
			t.Errorf("%s: the commit must list the file:\n%s", l, o.stderr.String())
		}
	}
	// and says nothing when there is nothing to say
	var o out
	_ = gate(sec.High, &fakeProvider{}, &o)(t.Context(), sec.StageCommit, nil)
	if o.stderr.Len() != 0 || o.stdout.Len() != 0 {
		t.Errorf("no alerts, no words: %q %q", o.stderr.String(), o.stdout.String())
	}
}

func TestThePushThatFixesAnAlertGoesThrough(t *testing.T) {
	f := &fakeProvider{report: provider.SecurityReport{Alerts: []provider.SecurityAlert{alert("critical", "package.json", 1), alert("high", "backend/go.mod", 2)}}}
	var o out
	err := gate(sec.High, f, &o)(t.Context(), sec.StagePush, []string{"package.json", "src/a.js"})
	if err == nil || !strings.Contains(err.Error(), "1 open alert (1 high)") {
		t.Fatalf("the alert on backend/go.mod is not being fixed: %v", err)
	}
	o = out{}
	if err := gate(sec.High, f, &o)(t.Context(), sec.StagePush, []string{"package.json", "backend/go.mod"}); err != nil {
		t.Fatalf("a push that touches both files is the fix: %v", err)
	}
	if !strings.Contains(o.stderr.String(), "treated as a fix") {
		t.Errorf("it must say why it let the push through:\n%s", o.stderr.String())
	}
}

func TestWhenTheAlertsCannotBeRead(t *testing.T) {
	denied := provider.SecurityReport{Sources: []provider.SourceResult{
		{Source: provider.SourceDependabot, Enabled: true, Err: errors.New("GitHub denied access to the Dependabot alerts (403)")},
		{Source: provider.SourceCodeScanning, Enabled: true},
	}}
	for l, wantBlock := range map[sec.Level]bool{sec.Low: false, sec.Medium: false, sec.High: true} {
		var o out
		err := gate(l, &fakeProvider{report: denied}, &o)(t.Context(), sec.StagePush, nil)
		if (err != nil) != wantBlock {
			t.Errorf("%s: err = %v, want block=%v", l, err, wantBlock)
		}
		if !strings.Contains(o.stderr.String(), "could not check Dependabot alerts") {
			t.Errorf("%s: the failure must be said:\n%s", l, o.stderr.String())
		}
	}
	// the whole call failing, and the repository not being identifiable, behave the same
	var o out
	if err := gate(sec.High, &fakeProvider{err: errors.New("network down")}, &o)(t.Context(), sec.StagePush, nil); err == nil || code(err) != apperr.CodePolicy {
		t.Errorf("high must not let an unchecked push through: %v", err)
	}
	if err := gate(sec.Medium, &fakeProvider{err: errors.New("network down")}, &out{})(t.Context(), sec.StagePush, nil); err != nil {
		t.Errorf("medium must not be held up by the network: %v", err)
	}
	g := NewGate(sec.High, func(context.Context) (provider.Provider, provider.Repo, error) {
		return nil, provider.Repo{}, errors.New("remote is not configured")
	}, (&out{}).printer())
	if err := g(t.Context(), sec.StagePush, nil); err == nil || !strings.Contains(err.Error(), "could not be checked") {
		t.Errorf("an unidentifiable repository at high: %v", err)
	}
	// but a feature that is simply off is not a failure
	off := provider.SecurityReport{Sources: []provider.SourceResult{{Source: provider.SourceCodeScanning, Enabled: false}, {Source: provider.SourceDependabot, Enabled: true}}}
	if err := gate(sec.High, &fakeProvider{report: off}, &out{})(t.Context(), sec.StagePush, nil); err != nil {
		t.Errorf("code scanning not enabled must not block: %v", err)
	}
}

func TestAProviderWithoutAlertsIsSaidPlainly(t *testing.T) {
	gl, err := gitlab.NewClient("")
	if err != nil {
		t.Skip(err)
	}
	var o out
	g := NewGate(sec.High, func(context.Context) (provider.Provider, provider.Repo, error) { return gl, acme, nil }, o.printer())
	if err := g(t.Context(), sec.StagePush, nil); err != nil {
		t.Errorf("GitLab cannot be checked, so nothing is blocked: %v", err)
	}
	if !strings.Contains(o.stderr.String(), "GitLab has no security alerts API") {
		t.Errorf("it must not pretend to protect:\n%s", o.stderr.String())
	}
}

func TestTextFromTheProviderCannotInjectTerminalCodes(t *testing.T) {
	evil := provider.SecurityAlert{Source: "dependabot", Number: 1, Severity: "high", Path: "a\x1b[31m.json", Package: "npm:x\x07", Title: "bad\x1b]0;pwned\x07 title\nnext line", URL: "u\x1b[0m"}
	f := &fakeProvider{report: provider.SecurityReport{Alerts: []provider.SecurityAlert{evil}}}
	var o out
	_ = gate(sec.Low, f, &o)(t.Context(), sec.StagePush, nil)
	if strings.ContainsAny(o.stderr.String(), "\x1b\x07") {
		t.Errorf("control characters reached the terminal: %q", o.stderr.String())
	}
	if strings.Count(o.stderr.String(), "\n") != 2 { // the heading and one row
		t.Errorf("a newline in a title must not add lines: %q", o.stderr.String())
	}
}

func TestLongListsAreCut(t *testing.T) {
	var many []provider.SecurityAlert
	for i := 1; i <= 30; i++ {
		many = append(many, alert("low", "go.mod", i))
	}
	var o out
	_ = gate(sec.Low, &fakeProvider{report: provider.SecurityReport{Alerts: many, Truncated: true}}, &o)(t.Context(), sec.StagePush, nil)
	text := o.stderr.String()
	if !strings.Contains(text, "30 open security alerts") || !strings.Contains(text, "... and 18 more") || !strings.Contains(text, "the list was cut") {
		t.Errorf("text:\n%s", text)
	}
	if n := strings.Count(text, "LOW"); n != maxRows {
		t.Errorf("%d rows, want %d", n, maxRows)
	}
}

func TestListShowsTheAlertsAndTheSources(t *testing.T) {
	f := &fakeProvider{report: provider.SecurityReport{
		Alerts:  []provider.SecurityAlert{alert("high", "package.json", 1)},
		Sources: []provider.SourceResult{{Source: provider.SourceDependabot, Enabled: true}, {Source: provider.SourceCodeScanning, Enabled: false}},
	}}
	var o out
	if err := List(t.Context(), f, acme, sec.Medium, false, o.printer()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1 open security alert on acme/tool (1 high)", "SEVERITY", "package.json", "npm:lodash"} {
		if !strings.Contains(o.stdout.String(), want) {
			t.Errorf("missing %q in:\n%s", want, o.stdout.String())
		}
	}
	// with nothing open it says so, and which source is off, as separate facts
	o = out{}
	f.report.Alerts = nil
	if err := List(t.Context(), f, acme, sec.Off, false, o.printer()); err != nil {
		t.Fatal(err)
	}
	text := o.stdout.String() + o.stderr.String()
	for _, want := range []string{"✓ No open security alerts on acme/tool\n", "checked         Dependabot: 0 open", "not checked     code scanning: not enabled on this repository", "level           off: nothing is checked", "push            security.level is off: nothing is blocked"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "(code scanning is not enabled there)") {
		t.Errorf("the note must not hide in a parenthesis:\n%s", text)
	}
}

func TestListJSON(t *testing.T) {
	f := &fakeProvider{report: provider.SecurityReport{
		Alerts: []provider.SecurityAlert{alert("low", "go.mod", 2), alert("critical", "package.json", 1)},
		Sources: []provider.SourceResult{
			{Source: provider.SourceDependabot, Enabled: true},
			{Source: provider.SourceCodeScanning, Enabled: true, Err: errors.New("denied\x1b[31m")},
		},
	}}
	var o out
	if err := List(t.Context(), f, acme, sec.High, true, o.printer()); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Provider, Repo, Level string
		Alerts                []struct {
			Severity string
			Path     string
			Number   int
		}
		Counts  map[string]int
		Sources []struct{ Source, Status, Message string }
	}
	if err := json.Unmarshal(o.stdout.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, o.stdout.String())
	}
	if got.Repo != "acme/tool" || got.Level != "high" || got.Provider != "github" || len(got.Alerts) != 2 || got.Alerts[0].Severity != "critical" {
		t.Errorf("got = %+v", got)
	}
	if got.Counts["critical"] != 1 || got.Counts["low"] != 1 || got.Counts["high"] != 0 {
		t.Errorf("counts = %v (every severity is present)", got.Counts)
	}
	if len(got.Sources) != 2 || got.Sources[1].Status != "error" || strings.Contains(got.Sources[1].Message, "\x1b") {
		t.Errorf("sources = %+v", got.Sources)
	}
	// an empty result is still a document, with empty lists
	o = out{}
	f.report = provider.SecurityReport{Sources: []provider.SourceResult{{Source: provider.SourceDependabot, Enabled: true}}}
	if err := List(t.Context(), f, acme, sec.Off, true, o.printer()); err != nil || !strings.Contains(o.stdout.String(), `"alerts": []`) {
		t.Errorf("err=%v\n%s", err, o.stdout.String())
	}
}

func TestListFailsWhenNothingCouldBeRead(t *testing.T) {
	f := &fakeProvider{report: provider.SecurityReport{Sources: []provider.SourceResult{
		{Source: provider.SourceDependabot, Enabled: true, Err: errors.New("rejected the token")},
		{Source: provider.SourceCodeScanning, Enabled: true, Err: errors.New("rejected the token")},
	}}}
	err := List(t.Context(), f, acme, sec.Low, false, (&out{}).printer())
	if err == nil || code(err) != apperr.CodeExternal || !strings.Contains(apperr.HintOf(err), "security_events") {
		t.Errorf("err = %v", err)
	}
	gl, glErr := gitlab.NewClient("")
	if glErr == nil {
		if err := List(t.Context(), gl, acme, sec.Low, false, (&out{}).printer()); err == nil || code(err) != apperr.CodeConfig {
			t.Errorf("GitLab: %v", err)
		}
	}
}

func TestTheHintDependsOnWhyTheTokenFailed(t *testing.T) {
	rejected := &fakeProvider{report: provider.SecurityReport{Sources: []provider.SourceResult{
		{Source: provider.SourceDependabot, Enabled: true, Err: errors.New("GitHub rejected the token (401): it is invalid, expired or revoked")},
		{Source: provider.SourceCodeScanning, Enabled: true, Err: errors.New("GitHub rejected the token (401): it is invalid, expired or revoked")},
	}}}
	err := List(t.Context(), rejected, acme, sec.Low, false, (&out{}).printer())
	h := apperr.HintOf(err)
	if err == nil || !strings.Contains(h, "make a new one") || !strings.Contains(h, "Where the token comes from") || strings.Contains(h, "security_events") {
		t.Errorf("a 401 is a token GitHub does not accept, not a missing scope: hint = %q", h)
	}

	denied := &fakeProvider{report: provider.SecurityReport{Sources: []provider.SourceResult{
		{Source: provider.SourceDependabot, Enabled: true, Err: errors.New("GitHub denied access to the Dependabot alerts (403)")},
		{Source: provider.SourceCodeScanning, Enabled: true, Err: errors.New("GitHub denied access to the code scanning alerts (403)")},
	}}}
	h = apperr.HintOf(List(t.Context(), denied, acme, sec.Low, false, (&out{}).printer()))
	for _, want := range []string{"Dependabot alerts and Code scanning alerts (Read)", "fine-grained", "security_events", "classic"} {
		if !strings.Contains(h, want) {
			t.Errorf("hint for a token without rights lacks %q: %q", want, h)
		}
	}
}

func bothOff() provider.SecurityReport {
	return provider.SecurityReport{Sources: []provider.SourceResult{
		{Source: provider.SourceDependabot, Enabled: false},
		{Source: provider.SourceCodeScanning, Enabled: false},
	}}
}

func TestWhenNothingIsEnabledTheListDoesNotPretendTheRepositoryIsClean(t *testing.T) {
	var o out
	if err := List(t.Context(), &fakeProvider{report: bothOff()}, acme, sec.Low, false, o.printer()); err != nil {
		t.Fatalf("not enabled is not a failure: %v", err)
	}
	all := o.stdout.String() + o.stderr.String()
	if strings.Contains(all, "✓") || strings.Contains(all, "No open security alerts") {
		t.Errorf("no green tick when nothing is looked at:\n%s", all)
	}
	if !strings.Contains(o.stderr.String(), "! Nothing is being checked on acme/tool: no source of alerts is enabled there") {
		t.Errorf("the headline is a warning:\n%s", o.stderr.String())
	}
	for _, want := range []string{
		"not checked     Dependabot: not enabled on this repository (turn it on in the repository's Settings, Advanced Security)",
		"not checked     code scanning: not enabled on this repository (free on public repositories; a private one needs GitHub Code Security",
		"level           low: warns at push, never blocks",
		"push            would go through (there is nothing to check)",
	} {
		if !strings.Contains(o.stdout.String(), want) {
			t.Errorf("missing %q in:\n%s", want, o.stdout.String())
		}
	}

	// with one source on, "no alerts" is true; the other one is a separate fact
	o = out{}
	one := provider.SecurityReport{Sources: []provider.SourceResult{{Source: provider.SourceDependabot, Enabled: true}, {Source: provider.SourceCodeScanning, Enabled: false}}}
	if err := List(t.Context(), &fakeProvider{report: one}, acme, sec.Low, false, o.printer()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"✓ No open security alerts on acme/tool\n", "checked         Dependabot: 0 open", "not checked     code scanning: not enabled on this repository", "push            would go through\n"} {
		if !strings.Contains(o.stdout.String(), want) {
			t.Errorf("missing %q in:\n%s", want, o.stdout.String())
		}
	}

	// the JSON says the same thing in data: both sources not_enabled, an empty list, and the push verdict
	o = out{}
	_ = List(t.Context(), &fakeProvider{report: bothOff()}, acme, sec.Low, true, o.printer())
	var got struct {
		Sources []struct {
			Source, Status string
			Open           int
		}
		Alerts []any
		Push   struct {
			WouldBlock bool `json:"would_block"`
			Blockers   int
			Unverified bool
		}
	}
	if err := json.Unmarshal(o.stdout.Bytes(), &got); err != nil || len(got.Alerts) != 0 || got.Sources[0].Status != "not_enabled" || got.Sources[1].Status != "not_enabled" || got.Push.WouldBlock {
		t.Errorf("err=%v got=%+v", err, got)
	}
}

func TestTheListSaysWhetherAPushFromHereWouldBeBlocked(t *testing.T) {
	r := provider.SecurityReport{
		Alerts: []provider.SecurityAlert{alert("critical", "package.json", 1), alert("high", "backend/go.mod", 2)},
		Sources: []provider.SourceResult{
			{Source: provider.SourceDependabot, Enabled: true},
			{Source: provider.SourceCodeScanning, Enabled: false},
		},
	}
	for level, want := range map[sec.Level]string{
		sec.Off:    "push            security.level is off: nothing is blocked",
		sec.Low:    "push            would go through\n",
		sec.Medium: "push            would be BLOCKED by 1 alert (1 critical); a push that changes package.json is allowed so the fix can be sent",
		sec.High:   "push            would be BLOCKED by 2 alerts (1 critical, 1 high); a push that changes backend/go.mod or package.json is allowed so the fix can be sent",
	} {
		var o out
		if err := List(t.Context(), &fakeProvider{report: r}, acme, level, false, o.printer()); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(o.stdout.String(), want) {
			t.Errorf("%s: missing %q in:\n%s", level, want, o.stdout.String())
		}
		if !strings.Contains(o.stdout.String(), "checked         Dependabot: 2 open") || !strings.Contains(o.stdout.String(), "read from the default branch") {
			t.Errorf("%s: the facts are missing:\n%s", level, o.stdout.String())
		}
	}

	// the JSON carries the verdict too
	var o out
	_ = List(t.Context(), &fakeProvider{report: r}, acme, sec.High, true, o.printer())
	var got struct {
		Push struct {
			WouldBlock bool `json:"would_block"`
			Blockers   int
		}
		Sources []struct{ Open int }
	}
	if err := json.Unmarshal(o.stdout.Bytes(), &got); err != nil || !got.Push.WouldBlock || got.Push.Blockers != 2 || got.Sources[0].Open != 2 {
		t.Errorf("err=%v got=%+v\n%s", err, got, o.stdout.String())
	}

	// a source that failed is its own fact, and at high it is what holds the push
	failed := provider.SecurityReport{Sources: []provider.SourceResult{
		{Source: provider.SourceDependabot, Enabled: true},
		{Source: provider.SourceCodeScanning, Enabled: true, Err: errors.New("denied\x1b[31m")},
	}}
	o = out{}
	if err := List(t.Context(), &fakeProvider{report: failed}, acme, sec.High, false, o.printer()); err != nil {
		t.Fatal(err)
	}
	all := o.stdout.String() + o.stderr.String()
	if strings.Contains(all, "✓") || strings.Contains(all, "\x1b") {
		t.Errorf("a partial answer is not a green tick, and control characters never reach the terminal:\n%q", all)
	}
	for _, want := range []string{"! No open alerts found on acme/tool, but not everything could be read", "could not read  code scanning: denied", "would be BLOCKED: the alerts could not be fully checked"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
}

func TestThePushSaysWhenNothingIsBeingChecked(t *testing.T) {
	for _, level := range []sec.Level{sec.Low, sec.Medium, sec.High} {
		var o out
		if err := gate(level, &fakeProvider{report: bothOff()}, &o)(t.Context(), sec.StagePush, nil); err != nil {
			t.Errorf("%s: a repository with the features off must not block: %v", level, err)
		}
		want := "security.level is " + string(level) + ", but nothing is being checked on acme/tool: Dependabot and code scanning are not enabled there"
		if !strings.Contains(o.stderr.String(), want) {
			t.Errorf("%s: missing %q in:\n%s", level, want, o.stderr.String())
		}
	}
	// a commit does not nag about it
	var o out
	_ = gate(sec.High, &fakeProvider{report: bothOff()}, &o)(t.Context(), sec.StageCommit, nil)
	if o.stderr.Len() != 0 {
		t.Errorf("a commit must stay quiet: %q", o.stderr.String())
	}
	// one source on, or one that failed: that is not "nothing is checked"
	for name, r := range map[string]provider.SecurityReport{
		"one on": {Sources: []provider.SourceResult{{Source: provider.SourceDependabot, Enabled: true}, {Source: provider.SourceCodeScanning, Enabled: false}}},
		"failed": {Sources: []provider.SourceResult{{Source: provider.SourceDependabot, Enabled: true, Err: errors.New("denied")}, {Source: provider.SourceCodeScanning, Enabled: false}}},
	} {
		o = out{}
		_ = gate(sec.Medium, &fakeProvider{report: r}, &o)(t.Context(), sec.StagePush, nil)
		if strings.Contains(o.stderr.String(), "nothing is being checked") {
			t.Errorf("%s: wrong warning:\n%s", name, o.stderr.String())
		}
	}
}
