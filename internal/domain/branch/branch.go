// Package branch holds branch-name validation and base-branch resolution.
package branch

import (
	"fmt"
	"regexp"
	"strings"
)

// ValidName reports whether name is safe to pass to Git as a branch name.
// It is stricter than git check-ref-format on purpose: it rejects anything
// that could be mistaken for an option or a revision expression.
func ValidName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("branch name is empty")
	case strings.HasPrefix(name, "-"):
		return fmt.Errorf("branch name %q must not start with '-'", name)
	case strings.HasPrefix(name, "/"), strings.HasSuffix(name, "/"), strings.Contains(name, "//"):
		return fmt.Errorf("branch name %q has an empty path segment", name)
	case strings.Contains(name, ".."), strings.Contains(name, "@{"):
		return fmt.Errorf("branch name %q contains a forbidden sequence", name)
	case strings.HasSuffix(name, ".") || strings.HasSuffix(name, ".lock") || name == "@":
		return fmt.Errorf("branch name %q has a forbidden ending", name)
	}
	for _, r := range name {
		if r < 0x21 || r == 0x7f || strings.ContainsRune("~^:?*[\\", r) {
			return fmt.Errorf("branch name %q contains forbidden character %q", name, r)
		}
	}
	for _, seg := range strings.Split(name, "/") {
		if strings.HasPrefix(seg, ".") {
			return fmt.Errorf("branch name %q has a segment starting with '.'", name)
		}
	}
	return nil
}

// ValidPattern reports whether pattern is a valid branch glob: a branch name in
// which '*' matches within one path segment and '**' matches across segments.
func ValidPattern(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("branch pattern is empty")
	}
	return ValidName(strings.NewReplacer("**", "x", "*", "x").Replace(pattern))
}

// Match reports whether name matches the glob pattern.
func Match(pattern, name string) bool {
	re, err := compile(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(name)
}

func compile(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch {
		case strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(".*")
			i++
		case pattern[i] == '*':
			b.WriteString("[^/]*")
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// specificity ranks a matching pattern: exact names beat globs, and among
// globs the one with more literal characters wins.
func specificity(pattern string) int {
	if !strings.Contains(pattern, "*") {
		return 1 << 20
	}
	return len(strings.ReplaceAll(pattern, "*", ""))
}

// ResolveBase returns the base branch for current. The most specific matching
// pattern wins, independent of map order; if none match, defaultBase is used.
func ResolveBase(current, defaultBase string, mapping map[string]string) string {
	best, bestScore := "", -1
	for pattern, base := range mapping {
		if !Match(pattern, current) {
			continue
		}
		score := specificity(pattern)
		if score > bestScore || (score == bestScore && pattern < best) {
			best, bestScore = base, score
		}
	}
	if bestScore < 0 {
		return defaultBase
	}
	return best
}

// Contains reports whether name is in list.
func Contains(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}
