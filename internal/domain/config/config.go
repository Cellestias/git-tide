// Package config defines the typed Tide configuration, its defaults, and its
// semantic validation. Parsing from disk lives in internal/config.
package config

import (
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/cellestias/git-tide/internal/domain/branch"
	"github.com/cellestias/git-tide/internal/domain/secret"
	"github.com/cellestias/git-tide/internal/provider"
)

const (
	CurrentVersion = 1

	StrategyMerge  = "merge"
	StrategyRebase = "rebase"

	ConventionConventional = "conventional"

	maxCustomPatterns = 100
	maxPatternLength  = 512
)

type Config struct {
	Version  int      `yaml:"version"`
	Provider string   `yaml:"provider"` // auto, github or gitlab
	Remote   string   `yaml:"remote"`
	Branches Branches `yaml:"branches"`
	Update   Update   `yaml:"update"`
	Commits  Commits  `yaml:"commits"`
	Secrets  Secrets  `yaml:"secrets"`
	Hooks    Hooks    `yaml:"hooks"`
}

type Branches struct {
	DefaultBase string            `yaml:"default_base"`
	Protected   []string          `yaml:"protected"`
	Base        map[string]string `yaml:"base"`
}

type Update struct {
	Strategy string `yaml:"strategy"`
}

type Commits struct {
	Convention   string `yaml:"convention"`
	RequireScope bool   `yaml:"require_scope"`
}

type Secrets struct {
	Enabled bool `yaml:"enabled"`
	// CustomPatterns is accepted for backward compatibility. Prefer the
	// `secrets.patterns` list in secret.tide.yml, which is not committed.
	CustomPatterns []CustomPattern `yaml:"custom_patterns"`
	// LocalPatterns come from secret.tide.yml. They are never part of .tide.yml.
	LocalPatterns []CustomPattern `yaml:"-"`
}

type CustomPattern struct {
	Name    string `yaml:"name"`
	Pattern string `yaml:"pattern"`
}

type Hooks struct {
	Manage bool `yaml:"manage"`
}

// Default returns the configuration used for every key a file omits.
func Default() Config {
	return Config{
		Version:  CurrentVersion,
		Provider: provider.Auto,
		Remote:   "origin",
		Branches: Branches{
			DefaultBase: "main",
			Protected:   []string{"main"},
		},
		Update:  Update{Strategy: StrategyMerge},
		Commits: Commits{Convention: ConventionConventional},
		Secrets: Secrets{Enabled: true},
		Hooks:   Hooks{Manage: true},
	}
}

var (
	patternName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)
	remoteName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// Validate checks semantic constraints and reports every problem found,
// each prefixed with the offending field.
func (c Config) Validate() error {
	var errs []error
	bad := func(field, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", field, fmt.Sprintf(format, args...)))
	}

	if c.Version != CurrentVersion {
		bad("version", "unsupported version %d (expected %d)", c.Version, CurrentVersion)
	}
	switch c.Provider {
	case provider.Auto, provider.GitHub, provider.GitLab:
	default:
		bad("provider", "must be %q, %q or %q, got %q", provider.Auto, provider.GitHub, provider.GitLab, c.Provider)
	}
	if !remoteName.MatchString(c.Remote) {
		bad("remote", "invalid remote name %q", c.Remote)
	}
	if err := branch.ValidName(c.Branches.DefaultBase); err != nil {
		bad("branches.default_base", "%v", err)
	}
	for i, name := range c.Branches.Protected {
		if err := branch.ValidName(name); err != nil {
			bad(fmt.Sprintf("branches.protected[%d]", i), "%v", err)
		}
	}
	patterns := make([]string, 0, len(c.Branches.Base))
	for p := range c.Branches.Base {
		patterns = append(patterns, p)
	}
	sort.Strings(patterns)
	for _, p := range patterns {
		if err := branch.ValidPattern(p); err != nil {
			bad("branches.base", "%v", err)
		}
		if err := branch.ValidName(c.Branches.Base[p]); err != nil {
			bad(fmt.Sprintf("branches.base[%s]", p), "%v", err)
		}
	}
	if c.Update.Strategy != StrategyMerge && c.Update.Strategy != StrategyRebase {
		bad("update.strategy", "must be %q or %q, got %q", StrategyMerge, StrategyRebase, c.Update.Strategy)
	}
	if c.Commits.Convention != ConventionConventional {
		bad("commits.convention", "unsupported convention %q (only %q)", c.Commits.Convention, ConventionConventional)
	}
	errs = append(errs, ValidatePatterns("secrets.custom_patterns", c.Secrets.CustomPatterns)...)
	errs = append(errs, ValidatePatterns("secret.tide.yml: secrets.patterns", c.Secrets.LocalPatterns)...)
	return errors.Join(errs...)
}

// ValidatePatterns reports every problem in a list of custom secret patterns,
// each prefixed with field.
func ValidatePatterns(field string, patterns []CustomPattern) []error {
	var errs []error
	bad := func(f, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", f, fmt.Sprintf(format, args...)))
	}
	if len(patterns) > maxCustomPatterns {
		bad(field, "at most %d patterns are allowed", maxCustomPatterns)
	}
	seen := map[string]bool{}
	for i, cp := range patterns {
		f := fmt.Sprintf("%s[%d]", field, i)
		switch {
		case !patternName.MatchString(cp.Name):
			bad(f+".name", "must match %s, got %q", patternName, cp.Name)
		case seen[cp.Name]:
			bad(f+".name", "duplicate rule name %q", cp.Name)
		}
		seen[cp.Name] = true
		if len(cp.Pattern) == 0 || len(cp.Pattern) > maxPatternLength {
			bad(f+".pattern", "length must be between 1 and %d", maxPatternLength)
			continue
		}
		if _, err := regexp.Compile(cp.Pattern); err != nil {
			bad(f+".pattern", "invalid regular expression: %v", err)
		}
	}
	return errs
}

// SecretRules returns built-in rules followed by the custom rules from
// .tide.yml and secret.tide.yml. Call it only on a validated Config.
func (c Config) SecretRules() []secret.Rule {
	rules := secret.BuiltinRules()
	for _, cp := range append(append([]CustomPattern{}, c.Secrets.CustomPatterns...), c.Secrets.LocalPatterns...) {
		rules = append(rules, secret.Rule{Name: cp.Name, Pattern: regexp.MustCompile(cp.Pattern)})
	}
	return rules
}

// BaseFor resolves the base branch for current.
func (c Config) BaseFor(current string) string {
	return branch.ResolveBase(current, c.Branches.DefaultBase, c.Branches.Base)
}
