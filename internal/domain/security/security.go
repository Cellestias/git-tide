// Package security holds the rules of Tide's security level: when alerts are
// looked up and when they stop a push. It never talks to a provider or to Git;
// it only decides from the alerts it is given.
package security

import (
	"sort"
	"strings"

	"github.com/cellestias/git-tide/internal/provider"
)

// Level is how strict Tide is about the security alerts of a repository.
type Level string

const (
	// Off looks nothing up. It is the default, so an existing setup does not change.
	Off Level = "off"
	// Low warns at push and never blocks.
	Low Level = "low"
	// Medium warns at commit and at push, and blocks a push while a critical alert is open.
	Medium Level = "medium"
	// High warns at commit, and blocks a push while any alert is open, or when the alerts cannot be checked.
	High Level = "high"
)

// ParseLevel reads a level name; "" is Off.
func ParseLevel(s string) (Level, bool) {
	switch Level(strings.ToLower(strings.TrimSpace(s))) {
	case "", Off:
		return Off, true
	case Low:
		return Low, true
	case Medium:
		return Medium, true
	case High:
		return High, true
	}
	return Off, false
}

// Describe says in a sentence what the level does.
func (l Level) Describe() string {
	switch l {
	case Low:
		return "low: warns at push, never blocks"
	case Medium:
		return "medium: warns at commit and push, blocks a push while a critical alert is open"
	case High:
		return "high: warns at commit, blocks a push while any alert is open or cannot be checked"
	}
	return "off: nothing is checked at commit or push"
}

// Stage is the moment an alert check runs.
type Stage int

const (
	// StageCommit runs after a commit is made. It never blocks anything.
	StageCommit Stage = iota
	// StagePush runs before a push, and may stop it.
	StagePush
)

// Wanted reports whether alerts are looked up at all at this stage. At Low
// nothing is looked up at commit, so committing stays as fast as ever.
func Wanted(l Level, s Stage) bool {
	switch l {
	case Low:
		return s == StagePush
	case Medium, High:
		return true
	}
	return false
}

// Rank orders severities: critical 4, high 3, medium 2, low 1, anything else 0.
func Rank(severity string) int {
	switch strings.ToLower(severity) {
	case provider.SeverityCritical:
		return 4
	case provider.SeverityHigh:
		return 3
	case provider.SeverityMedium:
		return 2
	case provider.SeverityLow:
		return 1
	}
	return 0
}

// blocksFrom is the lowest severity that stops a push at this level (0: none does).
func blocksFrom(l Level) int {
	switch l {
	case Medium:
		return Rank(provider.SeverityCritical)
	case High:
		return Rank(provider.SeverityLow)
	}
	return 0
}

// Decision is what a level makes of a set of alerts.
type Decision struct {
	// Alerts is every open alert, most severe first: shown at any level.
	Alerts []provider.SecurityAlert
	// Blockers are the alerts that stop the push: severe enough for the level
	// and not on a file this push changes.
	Blockers []provider.SecurityAlert
	// Exempt are alerts severe enough to block that sit on a file this push
	// changes: the push is treated as an attempt to fix them.
	Exempt []provider.SecurityAlert
	// Unverified are the sources whose answer is missing.
	Unverified []provider.SourceResult
	// Block is true when the push must not go ahead: there are blockers, or, at
	// High, something could not be checked.
	Block bool
	// UnverifiedBlocks is true when Block is only because High cannot verify.
	UnverifiedBlocks bool
}

// Decide applies level to report at stage. touched holds the repository-relative
// paths the push changes; an alert on one of them is exempt from blocking, so
// the fix for an alert can always be sent. Only StagePush ever blocks.
func Decide(l Level, s Stage, report provider.SecurityReport, touched []string) Decision {
	if l == Off {
		return Decision{}
	}
	d := Decision{Alerts: Sorted(report.Alerts), Unverified: report.Unverified()}
	if s != StagePush {
		return d
	}
	changed := make(map[string]bool, len(touched))
	for _, p := range touched {
		changed[clean(p)] = true
	}
	min := blocksFrom(l)
	for _, a := range d.Alerts {
		if min == 0 || Rank(a.Severity) < min {
			continue
		}
		if a.Path != "" && changed[clean(a.Path)] {
			d.Exempt = append(d.Exempt, a)
			continue
		}
		d.Blockers = append(d.Blockers, a)
	}
	d.Block = len(d.Blockers) > 0
	if !d.Block && l == High && len(d.Unverified) > 0 {
		d.Block, d.UnverifiedBlocks = true, true
	}
	return d
}

func clean(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	return p
}

// Sorted returns alerts most severe first; equal severities keep a stable order
// by path, then source, then number.
func Sorted(alerts []provider.SecurityAlert) []provider.SecurityAlert {
	out := append([]provider.SecurityAlert(nil), alerts...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := Rank(a.Severity), Rank(b.Severity); ra != rb {
			return ra > rb
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.Number < b.Number
	})
	return out
}

// Counts returns how many alerts there are of each severity.
func Counts(alerts []provider.SecurityAlert) map[string]int {
	n := map[string]int{}
	for _, a := range alerts {
		n[strings.ToLower(a.Severity)]++
	}
	return n
}
