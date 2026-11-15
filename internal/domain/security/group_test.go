package security

import (
	"testing"

	"github.com/cellestias/git-tide/internal/provider"
)

func dep(pkg, path, sev, fixed string, n int) provider.SecurityAlert {
	return provider.SecurityAlert{Source: provider.SourceDependabot, Number: n, Severity: sev, Path: path, Package: pkg, FixedIn: fixed}
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0.9.0", "0.55.0", -1}, // numbers, not text
		{"4.17.21", "4.17.9", 1},
		{"4.18.0", "4.17.23", 1},
		{"1.0.0", "1.0.0", 0},
		{"v1.2.3", "1.2.3", 0},
		{"1.2", "1.2.0", 0},
		{"1.2.1", "1.2", 1},
		{"1.0.0-rc.1", "1.0.0", -1}, // a release is after its pre-release
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-rc.1", "1.0.0-rc.2", -1},
		{"2.0.0", "10.0.0", -1},
		{"1.0.0+build5", "1.0.0", 0},
		{"1.0.alpha", "1.0.1", -1},
		{"", "0.0.1", -1},
	} {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if tc.want != 0 {
			if back := CompareVersions(tc.b, tc.a); back != -tc.want {
				t.Errorf("CompareVersions(%q, %q) = %d, want %d (it must be antisymmetric)", tc.b, tc.a, back, -tc.want)
			}
		}
	}
}

func TestGroupsAreWhatAPersonFixes(t *testing.T) {
	alerts := []provider.SecurityAlert{
		dep("go:x/net", "backend/go.mod", "high", "0.17.0", 1),
		dep("go:x/net", "backend/go.mod", "medium", "0.55.0", 2),
		dep("go:x/net", "backend/go.mod", "medium", "0.23.0", 3),
		dep("npm:lodash", "package.json", "medium", "4.17.21", 4),
		dep("npm:lodash", "package.json", "high", "4.18.0", 5),
		dep("npm:left-pad", "package.json", "low", "1.3.0", 6), // alone: not a group
		{Source: provider.SourceCodeScanning, Number: 7, Severity: "high", Path: "src/a.js", Title: "xss"},
		{Source: provider.SourceCodeScanning, Number: 8, Severity: "high", Path: "src/a.js", Title: "xss"}, // two findings are not a package
		dep("npm:lodash", "web/package.json", "low", "4.17.0", 9),                                          // same package, another manifest
	}
	g := Groups(alerts)
	if len(g) != 2 {
		t.Fatalf("groups = %+v", g)
	}
	if g[0].Package != "go:x/net" || g[0].Path != "backend/go.mod" || g[0].Severity != "high" || len(g[0].Alerts) != 3 || g[0].UpgradeTo != "0.55.0" || g[0].Unfixed != 0 {
		t.Errorf("first group = %+v (the upgrade that fixes all three is the highest patched version, 0.55.0, not the last one listed)", g[0])
	}
	if g[1].Package != "npm:lodash" || g[1].Path != "package.json" || g[1].Severity != "high" || len(g[1].Alerts) != 2 || g[1].UpgradeTo != "4.18.0" {
		t.Errorf("second group = %+v", g[1])
	}
}

func TestFixesIncludeLoneAlertsAndSayWhatHasNoFix(t *testing.T) {
	alerts := []provider.SecurityAlert{
		dep("npm:left-pad", "package.json", "low", "1.3.0", 1),
		dep("npm:unfixable", "package.json", "critical", "", 2),
		dep("npm:half", "package.json", "high", "2.0.0", 3),
		dep("npm:half", "package.json", "high", "", 4),
		{Source: provider.SourceCodeScanning, Number: 5, Severity: "high", Path: "src/a.js"},
	}
	f := Fixes(alerts)
	if len(f) != 3 {
		t.Fatalf("fixes = %+v (a code scanning finding is not a package)", f)
	}
	if f[0].Package != "npm:unfixable" || f[0].UpgradeTo != "" || f[0].Unfixed != 1 {
		t.Errorf("most severe first, and no fix is said as such: %+v", f[0])
	}
	if f[1].Package != "npm:half" || f[1].UpgradeTo != "2.0.0" || f[1].Unfixed != 1 || len(f[1].Alerts) != 2 {
		t.Errorf("a package with one fixed and one unfixed alert: %+v", f[1])
	}
	if f[2].Package != "npm:left-pad" || f[2].UpgradeTo != "1.3.0" {
		t.Errorf("lone alert: %+v", f[2])
	}
	if len(Groups(alerts)) != 1 || Groups(alerts)[0].Package != "npm:half" {
		t.Errorf("only the package with two alerts is a group: %+v", Groups(alerts))
	}
	if Groups(nil) != nil && len(Groups(nil)) != 0 {
		t.Error("no alerts, no groups")
	}
}
