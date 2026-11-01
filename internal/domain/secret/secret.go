// Package secret detects likely secrets in added lines of staged content.
// Findings never carry the matched text.
package secret

import (
	"fmt"
	"regexp"
	"strings"
)

// AllowMarker on a line suppresses findings for that line.
const AllowMarker = "tide:allow-secret"

// Rule is a named detection pattern.
type Rule struct {
	Name    string
	Pattern *regexp.Regexp
}

// Finding locates a likely secret using only safe metadata.
type Finding struct {
	Rule string
	Path string
	Line int
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d (%s)", f.Path, f.Line, f.Rule)
}

// BuiltinRules returns the default detectors.
func BuiltinRules() []Rule {
	defs := []struct{ name, pattern string }{
		{"aws_access_key_id", `\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`},
		{"github_token", `\bgh[pousr]_[A-Za-z0-9]{36,}\b`},
		{"github_fine_grained_token", `\bgithub_pat_[A-Za-z0-9_]{22,}\b`},
		{"slack_token", `\bxox[abprs]-[A-Za-z0-9-]{10,}`},
		{"stripe_live_key", `\b[sr]k_live_[0-9A-Za-z]{24,}\b`},
		{"google_api_key", `\bAIza[0-9A-Za-z_-]{35}\b`},
		{"private_key", `-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP |ENCRYPTED )?PRIVATE KEY`},
	}
	rules := make([]Rule, len(defs))
	for i, d := range defs {
		rules[i] = Rule{Name: d.name, Pattern: regexp.MustCompile(d.pattern)}
	}
	return rules
}

// Detector scans lines against a rule set.
type Detector struct {
	rules []Rule
}

func NewDetector(rules []Rule) *Detector { return &Detector{rules: rules} }

// ScanLine returns one finding per rule that matches line.
func (d *Detector) ScanLine(path string, lineNo int, line string) []Finding {
	if strings.Contains(line, AllowMarker) {
		return nil
	}
	var out []Finding
	for _, r := range d.rules {
		if r.Pattern.MatchString(line) {
			out = append(out, Finding{Rule: r.Name, Path: path, Line: lineNo})
		}
	}
	return out
}
