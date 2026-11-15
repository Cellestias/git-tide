package security

import (
	"sort"
	"strconv"
	"strings"

	"github.com/cellestias/git-tide/internal/provider"
)

// Group is the Dependabot alerts of one package in one manifest: the unit a
// person fixes, with one upgrade. Twelve alerts on two packages are two tasks.
type Group struct {
	Path    string
	Package string
	// Severity is the highest of the group's alerts.
	Severity string
	Alerts   []provider.SecurityAlert
	// UpgradeTo is the lowest version that fixes every alert of the group that
	// has a fix: the highest of their first patched versions. Empty when none has.
	UpgradeTo string
	// Unfixed counts the alerts with no patched version yet.
	Unfixed int
}

// Groups collects the Dependabot alerts that share a manifest and a package,
// most severe group first, and keeps the groups of two or more alerts: a lone
// alert is already one line. An alert with no package (a code scanning finding)
// is a finding in one place, not a dependency to upgrade, so it is never grouped.
func Groups(alerts []provider.SecurityAlert) []Group {
	var out []Group
	for _, g := range Fixes(alerts) {
		if len(g.Alerts) >= 2 {
			out = append(out, g)
		}
	}
	return out
}

// Fixes returns, for every package with Dependabot alerts, what to do about it:
// the upgrade that fixes its alerts, lone alerts included. It is what a person
// acts on. Most severe group first.
func Fixes(alerts []provider.SecurityAlert) []Group {
	type key struct{ path, pkg string }
	by := map[key]*Group{}
	var order []key
	for _, a := range alerts {
		if a.Source != provider.SourceDependabot || a.Package == "" {
			continue
		}
		k := key{a.Path, a.Package}
		g := by[k]
		if g == nil {
			g = &Group{Path: a.Path, Package: a.Package}
			by[k] = g
			order = append(order, k)
		}
		g.Alerts = append(g.Alerts, a)
		if Rank(a.Severity) > Rank(g.Severity) {
			g.Severity = a.Severity
		}
		if a.FixedIn == "" {
			g.Unfixed++
		} else if g.UpgradeTo == "" || CompareVersions(a.FixedIn, g.UpgradeTo) > 0 {
			g.UpgradeTo = a.FixedIn
		}
	}
	out := make([]Group, 0, len(order))
	for _, k := range order {
		out = append(out, *by[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := Rank(a.Severity), Rank(b.Severity); ra != rb {
			return ra > rb
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Package < b.Package
	})
	return out
}

// CompareVersions orders two version strings: -1, 0 or 1. It compares the
// dot-separated parts one by one, numbers as numbers ("0.9" < "0.55"), and
// treats a leading "v" as nothing. A pre-release ("1.0.0-rc.1") sorts before
// the release it precedes. It is meant for the versions a vulnerability
// database gives, which follow the dotted form on every ecosystem Dependabot reads.
func CompareVersions(a, b string) int {
	pa, preA := splitVersion(a)
	pb, preB := splitVersion(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		x, y := part{isNum: true}, part{isNum: true} // a missing part is a zero: 1.2 is 1.2.0
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if c := comparePart(x, y); c != 0 {
			return c
		}
	}
	switch {
	case preA == preB:
		return 0
	case preA == "":
		return 1 // a release is later than its pre-release
	case preB == "":
		return -1
	}
	return strings.Compare(preA, preB)
}

type part struct {
	num   int
	text  string
	isNum bool
}

func splitVersion(v string) (parts []part, pre string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		if v[i] == '-' {
			pre = v[i+1:]
		}
		v = v[:i]
	}
	for _, s := range strings.Split(v, ".") {
		if n, err := strconv.Atoi(s); err == nil {
			parts = append(parts, part{num: n, isNum: true})
		} else {
			parts = append(parts, part{text: s})
		}
	}
	return parts, pre
}

func comparePart(x, y part) int {
	switch {
	case x.isNum && y.isNum:
		switch {
		case x.num < y.num:
			return -1
		case x.num > y.num:
			return 1
		}
		return 0
	case x.isNum:
		return 1 // a missing or textual part sorts below a number
	case y.isNum:
		return -1
	}
	return strings.Compare(strings.ToLower(x.text), strings.ToLower(y.text))
}
