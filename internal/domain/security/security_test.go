package security

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/provider"
)

func alert(sev, path string, n int) provider.SecurityAlert {
	return provider.SecurityAlert{Source: provider.SourceDependabot, Number: n, Severity: sev, Path: path}
}

func report(alerts ...provider.SecurityAlert) provider.SecurityReport {
	return provider.SecurityReport{Alerts: alerts}
}

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]Level{"": Off, "off": Off, "low": Low, " Medium ": Medium, "HIGH": High} {
		if got, ok := ParseLevel(in); !ok || got != want {
			t.Errorf("ParseLevel(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"none", "critical", "1", "low,high"} {
		if _, ok := ParseLevel(in); ok {
			t.Errorf("ParseLevel(%q) should be refused", in)
		}
	}
}

func TestWantedWhereAlertsAreLookedUp(t *testing.T) {
	want := map[Level][2]bool{ // commit, push
		Off: {false, false}, Low: {false, true}, Medium: {true, true}, High: {true, true},
	}
	for l, w := range want {
		if Wanted(l, StageCommit) != w[0] || Wanted(l, StagePush) != w[1] {
			t.Errorf("%s: commit=%v push=%v, want %v", l, Wanted(l, StageCommit), Wanted(l, StagePush), w)
		}
	}
}

func TestWhatBlocksAPushAtEachLevel(t *testing.T) {
	// the rows are the severities of the one open alert; the columns the levels
	cases := []struct {
		severity string
		block    map[Level]bool
	}{
		{"critical", map[Level]bool{Off: false, Low: false, Medium: true, High: true}},
		{"high", map[Level]bool{Off: false, Low: false, Medium: false, High: true}},
		{"medium", map[Level]bool{Off: false, Low: false, Medium: false, High: true}},
		{"low", map[Level]bool{Off: false, Low: false, Medium: false, High: true}},
	}
	for _, c := range cases {
		for l, want := range c.block {
			d := Decide(l, StagePush, report(alert(c.severity, "go.mod", 1)), nil)
			if d.Block != want {
				t.Errorf("%s alert at level %s: block=%v, want %v", c.severity, l, d.Block, want)
			}
			if l != Off && len(d.Alerts) != 1 {
				t.Errorf("%s at %s: the alert must be listed at every level", c.severity, l)
			}
		}
	}
}

func TestACommitNeverBlocks(t *testing.T) {
	for _, l := range []Level{Low, Medium, High} {
		d := Decide(l, StageCommit, report(alert("critical", "go.mod", 1)), nil)
		if d.Block || len(d.Blockers) != 0 || len(d.Alerts) != 1 {
			t.Errorf("%s at commit: %+v", l, d)
		}
	}
	if d := Decide(High, StageCommit, provider.SecurityReport{Sources: []provider.SourceResult{{Source: "x", Err: errors.New("boom")}}}, nil); d.Block {
		t.Error("an unverifiable report must not block a commit")
	}
}

func TestOffSeesNothing(t *testing.T) {
	d := Decide(Off, StagePush, report(alert("critical", "go.mod", 1)), nil)
	if d.Block || len(d.Alerts) != 0 {
		t.Errorf("off: %+v", d)
	}
}

func TestThePushThatFixesAnAlertIsAllowed(t *testing.T) {
	r := report(alert("critical", "package.json", 1), alert("critical", "backend/go.mod", 2), alert("low", "package.json", 3))

	d := Decide(High, StagePush, r, []string{"src/app.js", "package.json"})
	if !d.Block || len(d.Blockers) != 1 || d.Blockers[0].Number != 2 {
		t.Fatalf("only the alert on a file this push does not touch should block: %+v", d)
	}
	if len(d.Exempt) != 2 {
		t.Errorf("exempt = %+v, want the two alerts on package.json", d.Exempt)
	}

	d = Decide(High, StagePush, r, []string{"package.json", "backend/go.mod"})
	if d.Block || len(d.Blockers) != 0 || len(d.Exempt) != 3 {
		t.Errorf("touching every affected file lets the push through: %+v", d)
	}
	if len(d.Alerts) != 3 {
		t.Error("the alerts are still listed")
	}
}

func TestPathsAreComparedAsRepositoryPaths(t *testing.T) {
	r := report(alert("critical", "./backend/go.mod", 1))
	if d := Decide(Medium, StagePush, r, []string{"backend/go.mod"}); d.Block {
		t.Error("a leading ./ must not matter")
	}
	r = report(alert("critical", `backend\go.mod`, 1))
	if d := Decide(Medium, StagePush, r, []string{"backend/go.mod"}); d.Block {
		t.Error("separators must not matter")
	}
	// the same name in another directory is another file
	r = report(alert("critical", "go.mod", 1))
	if d := Decide(Medium, StagePush, r, []string{"backend/go.mod"}); !d.Block {
		t.Error("backend/go.mod is not go.mod")
	}
	// an alert with no path cannot be exempted
	if d := Decide(Medium, StagePush, report(alert("critical", "", 1)), []string{""}); !d.Block {
		t.Error("an alert without a path must not be exempt")
	}
}

func TestWhenTheAlertsCannotBeChecked(t *testing.T) {
	failed := provider.SecurityReport{Sources: []provider.SourceResult{
		{Source: provider.SourceDependabot, Enabled: true},
		{Source: provider.SourceCodeScanning, Err: errors.New("denied")},
	}}
	for l, want := range map[Level]bool{Low: false, Medium: false, High: true} {
		d := Decide(l, StagePush, failed, nil)
		if d.Block != want || d.UnverifiedBlocks != want || len(d.Unverified) != 1 {
			t.Errorf("%s: block=%v unverifiedBlocks=%v unverified=%d, want block=%v", l, d.Block, d.UnverifiedBlocks, len(d.Unverified), want)
		}
	}
	// when alerts already block, that is the reason, not the failed source
	r := failed
	r.Alerts = []provider.SecurityAlert{alert("high", "go.mod", 1)}
	if d := Decide(High, StagePush, r, nil); !d.Block || d.UnverifiedBlocks || len(d.Blockers) != 1 {
		t.Errorf("%+v", d)
	}
	// a feature that is not enabled is not a failure
	notEnabled := provider.SecurityReport{Sources: []provider.SourceResult{{Source: provider.SourceCodeScanning, Enabled: false}}}
	if d := Decide(High, StagePush, notEnabled, nil); d.Block || len(d.Unverified) != 0 {
		t.Errorf("not enabled must not block: %+v", d)
	}
}

func TestSortedIsMostSevereFirstAndStable(t *testing.T) {
	in := []provider.SecurityAlert{
		alert("low", "b", 1), alert("critical", "z", 2), alert("high", "a", 3), alert("critical", "a", 4), alert("medium", "a", 5), alert("critical", "a", 1),
	}
	got := Sorted(in)
	var order [][2]string
	for _, a := range got {
		order = append(order, [2]string{a.Severity, a.Path})
	}
	want := [][2]string{{"critical", "a"}, {"critical", "a"}, {"critical", "z"}, {"high", "a"}, {"medium", "a"}, {"low", "b"}}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
	if got[0].Number != 1 || got[1].Number != 4 {
		t.Errorf("ties go by number: %+v", got[:2])
	}
	if in[0].Severity != "low" {
		t.Error("Sorted must not reorder its input")
	}
}

func TestRankAndCounts(t *testing.T) {
	if Rank("CRITICAL") != 4 || Rank("high") != 3 || Rank("medium") != 2 || Rank("low") != 1 || Rank("note") != 0 || Rank("") != 0 {
		t.Error("rank")
	}
	c := Counts([]provider.SecurityAlert{alert("high", "a", 1), alert("High", "b", 2), alert("low", "c", 3)})
	if c["high"] != 2 || c["low"] != 1 || c["critical"] != 0 {
		t.Errorf("counts = %v", c)
	}
}

func TestDescribeSaysWhatEachLevelDoes(t *testing.T) {
	for l, want := range map[Level]string{
		Off: "off: nothing", Low: "low: warns at push, never blocks",
		Medium: "medium: warns at commit and push, blocks a push while a critical alert is open",
		High:   "high: warns at commit, blocks a push while any alert is open",
	} {
		if got := l.Describe(); !strings.Contains(got, want) {
			t.Errorf("%s: %q does not contain %q", l, got, want)
		}
	}
}
