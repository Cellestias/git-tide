package secret

import (
	"strings"
	"testing"
)

// Values below are obviously fake and built at runtime so scanners that read
// this repository do not flag the source file.
func TestBuiltinRules(t *testing.T) {
	d := NewDetector(BuiltinRules())
	tests := []struct {
		rule, line string
	}{
		{"aws_access_key_id", `key = "` + "AKIA" + strings.Repeat("A", 16) + `"`},
		{"github_token", "token: " + "ghp_" + strings.Repeat("a", 36)},
		{"github_fine_grained_token", "github_pat_" + strings.Repeat("a", 30)},
		{"slack_token", "xoxb-" + strings.Repeat("1", 12)},
		{"stripe_live_key", "sk_live_" + strings.Repeat("a", 24)},
		{"google_api_key", "AIza" + strings.Repeat("a", 35)},
		{"private_key", "-----BEGIN " + "RSA PRIVATE KEY-----"},
	}
	for _, tt := range tests {
		got := d.ScanLine("f.txt", 7, tt.line)
		if len(got) != 1 || got[0].Rule != tt.rule || got[0].Path != "f.txt" || got[0].Line != 7 {
			t.Errorf("rule %s: findings = %+v", tt.rule, got)
		}
	}
}

func TestNoFalsePositivesAndAllowMarker(t *testing.T) {
	d := NewDetector(BuiltinRules())
	for _, line := range []string{
		"just a normal line",
		"AKIA is a prefix",
		"password = changeme",
		"AKIA" + strings.Repeat("A", 16) + " // " + AllowMarker,
	} {
		if got := d.ScanLine("f", 1, line); len(got) != 0 {
			t.Errorf("line %q: unexpected findings %+v", line, got)
		}
	}
}

func TestFindingNeverContainsMatch(t *testing.T) {
	d := NewDetector(BuiltinRules())
	secretValue := "AKIA" + strings.Repeat("Z", 16)
	for _, f := range d.ScanLine("f", 1, secretValue) {
		if strings.Contains(f.String(), secretValue) {
			t.Fatalf("finding leaks secret: %s", f)
		}
	}
}
