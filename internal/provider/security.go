package provider

import (
	"context"
	"strings"
)

// Sources of a security alert.
const (
	SourceDependabot   = "dependabot"    // a vulnerable dependency
	SourceCodeScanning = "code-scanning" // a finding in the code itself
)

// Severities, the same on every source.
const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
)

// SeverityValid reports whether s is one of the severities (any case).
func SeverityValid(s string) bool {
	switch strings.ToLower(s) {
	case SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow:
		return true
	}
	return false
}

// SecurityAlert is one open alert, whatever its source.
type SecurityAlert struct {
	Source   string
	Number   int
	Severity string // one of the Severity constants
	Title    string // the advisory summary or the rule description
	Package  string // dependabot: "npm:lodash"; empty for code scanning
	// Path is the file the alert is about: the manifest or lockfile that holds
	// the vulnerable dependency, or the file with the finding. Repository-relative,
	// forward slashes. Empty when the provider gives none.
	Path    string
	Line    int    // code scanning only; 0 when unknown
	FixedIn string // dependabot: the first patched version, when there is one
	URL     string
}

// SourceResult says how asking one source went. Enabled is false when the
// feature is simply not turned on for the repository (nothing to report, not a
// failure); Err is set when the answer could not be had (network, token, rights).
type SourceResult struct {
	Source  string
	Enabled bool
	Err     error
}

// SecurityReport is everything the provider could tell about a repository.
type SecurityReport struct {
	Alerts    []SecurityAlert
	Sources   []SourceResult
	Truncated bool // a list was cut at the adapter's cap
}

// Unverified returns the sources whose answer is missing.
func (r SecurityReport) Unverified() []SourceResult {
	var out []SourceResult
	for _, s := range r.Sources {
		if s.Err != nil {
			out = append(out, s)
		}
	}
	return out
}

// SecurityReader is implemented by the providers that can list security alerts.
// It is a separate interface so a provider without the feature (GitLab) needs
// no stub, and callers say plainly that it is not available.
type SecurityReader interface {
	// SecurityAlerts lists the open alerts of repo. A source that fails is
	// reported in Sources; the error is only for a failure of the whole call.
	SecurityAlerts(ctx context.Context, repo Repo) (SecurityReport, error)
}
