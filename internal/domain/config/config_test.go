package config

import (
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateReportsFields(t *testing.T) {
	c := Default()
	c.Version = 2
	c.Remote = "-bad"
	c.Branches.DefaultBase = "a..b"
	c.Branches.Protected = []string{"ok", "-x"}
	c.Branches.Base = map[string]string{"feature/*": "bad base"}
	c.Update.Strategy = "squash"
	c.Commits.Convention = "gitmoji"
	c.Secrets.CustomPatterns = []CustomPattern{
		{Name: "-bad", Pattern: "x"},
		{Name: "ok", Pattern: "("},
		{Name: "dup", Pattern: "a"},
		{Name: "dup", Pattern: "b"},
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, field := range []string{
		"version:", "remote:", "branches.default_base:", "branches.protected[1]:", "branches.base[feature/*]:",
		"update.strategy:", "commits.convention:", "secrets.custom_patterns[0].name:",
		"secrets.custom_patterns[1].pattern:", "secrets.custom_patterns[3].name:",
	} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("error missing %q:\n%v", field, err)
		}
	}
}

func TestSecretRulesIncludeCustom(t *testing.T) {
	c := Default()
	c.Secrets.CustomPatterns = []CustomPattern{{Name: "corp", Pattern: "corp_[a-z]{4}"}}
	rules := c.SecretRules()
	last := rules[len(rules)-1]
	if last.Name != "corp" || !last.Pattern.MatchString("corp_abcd") {
		t.Fatalf("custom rule missing: %+v", last)
	}
}

func TestBaseFor(t *testing.T) {
	c := Default()
	c.Branches.DefaultBase = "stage"
	c.Branches.Base = map[string]string{"release/*": "main"}
	if got := c.BaseFor("release/1"); got != "main" {
		t.Errorf("got %q", got)
	}
	if got := c.BaseFor("feature/x"); got != "stage" {
		t.Errorf("got %q", got)
	}
}

func TestSecurityLevel(t *testing.T) {
	c := Default()
	if c.Security.Level != "off" || c.SecurityLevel() != "off" {
		t.Fatalf("the default must be off, so an existing setup does not change: %q", c.Security.Level)
	}
	for _, ok := range []string{"", "off", "low", "medium", "high", "HIGH"} {
		c.Security.Level = ok
		if err := c.Validate(); err != nil {
			t.Errorf("security.level %q: %v", ok, err)
		}
	}
	for _, bad := range []string{"critical", "strict", "0", "low high"} {
		c.Security.Level = bad
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "security.level") {
			t.Errorf("security.level %q: err = %v", bad, err)
		}
	}
	c.Security.Level = "nonsense"
	if c.SecurityLevel() != "off" {
		t.Error("an unknown level must read as off, never as stricter")
	}
}
