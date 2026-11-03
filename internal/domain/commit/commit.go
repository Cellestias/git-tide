// Package commit validates commit messages against a convention.
package commit

import (
	"fmt"
	"regexp"
	"strings"
)

// Types are the Conventional Commits types accepted by default.
var Types = []string{"feat", "fix", "docs", "style", "refactor", "perf", "test", "build", "ci", "chore", "revert"}

// Rules configures validation.
type Rules struct {
	RequireScope bool
}

var header = regexp.MustCompile(`^([a-z]+)(?:\(([^()\s][^()]*)\))?(!)?: (\S.*)$`)

// Example is shown to the user when a message is rejected.
func (r Rules) Example() string {
	if r.RequireScope {
		return "feat(auth): add token refresh"
	}
	return "feat: add token refresh"
}

// Validate checks a raw commit message (as stored in COMMIT_EDITMSG) and
// returns a human-readable reason when it is invalid. Messages that Git
// generates itself (merges, reverts, fixup/squash) are accepted.
func (r Rules) Validate(raw string) error {
	subject := Subject(raw)
	if subject == "" {
		return fmt.Errorf("commit message is empty")
	}
	if generated(subject) {
		return nil
	}
	m := header.FindStringSubmatch(subject)
	if m == nil {
		return fmt.Errorf("subject %q does not follow Conventional Commits (<type>[(scope)][!]: <description>)", subject)
	}
	if !contains(Types, m[1]) {
		return fmt.Errorf("unknown type %q; use one of: %s", m[1], strings.Join(Types, ", "))
	}
	if r.RequireScope && m[2] == "" {
		return fmt.Errorf("a scope is required, for example %s(<scope>): description", m[1])
	}
	return nil
}

// Subject returns the first meaningful line of a raw commit message, applying
// Git's default cleanup (comment lines and everything after a scissors line
// are ignored).
func Subject(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if strings.HasPrefix(line, "# ------------------------ >8 ------------------------") {
			return ""
		}
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		return line
	}
	return ""
}

func generated(subject string) bool {
	for _, p := range []string{"Merge ", "Revert \"", "fixup! ", "squash! ", "amend! "} {
		if strings.HasPrefix(subject, p) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
