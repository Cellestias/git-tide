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
	Enabled        bool            `yaml:"enabled"`
	CustomPatterns []CustomPattern `yaml:"custom_patterns"`
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
		Version: CurrentVersion,
		Remote:  "origin",
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
	patternName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
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
	if len(c.Secrets.CustomPatterns) > maxCustomPatterns {
		bad("secrets.custom_patterns", "at most %d patterns are allowed", maxCustomPatterns)
	}
	seen := map[string]bool{}
	for i, cp := range c.Secrets.CustomPatterns {
		field := fmt.Sprintf("secrets.custom_patterns[%d]", i)
		switch {
		case !patternName.MatchString(cp.Name):
			bad(field+".name", "must match %s, got %q", patternName, cp.Name)
		case seen[cp.Name]:
			bad(field+".name", "duplicate rule name %q", cp.Name)
		}
		seen[cp.Name] = true
		if len(cp.Pattern) == 0 || len(cp.Pattern) > maxPatternLength {
			bad(field+".pattern", "length must be between 1 and %d", maxPatternLength)
			continue
		}
		if _, err := regexp.Compile(cp.Pattern); err != nil {
			bad(field+".pattern", "invalid regular expression: %v", err)
		}
	}
	return errors.Join(errs...)
}

// SecretRules returns built-in rules followed by the configured custom rules.
// Call it only on a validated Config.
func (c Config) SecretRules() []secret.Rule {
	rules := secret.BuiltinRules()
	for _, cp := range c.Secrets.CustomPatterns {
		rules = append(rules, secret.Rule{Name: cp.Name, Pattern: regexp.MustCompile(cp.Pattern)})
	}
	return rules
}

// BaseFor resolves the base branch for current.
func (c Config) BaseFor(current string) string {
	return branch.ResolveBase(current, c.Branches.DefaultBase, c.Branches.Base)
}
