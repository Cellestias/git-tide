package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/apperr"
)

func write(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadMissingReturnsDefaults(t *testing.T) {
	res, err := Load(t.TempDir())
	if err != nil || res.Found {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if res.Config.Branches.DefaultBase != "main" {
		t.Errorf("defaults not applied: %+v", res.Config)
	}
}

func TestLoadAppliesDefaultsOverPartialFile(t *testing.T) {
	dir := write(t, "version: 1\nbranches:\n  default_base: stage\n  protected: [main, stage]\nupdate:\n  strategy: rebase\n")
	res, err := Load(dir)
	if err != nil || !res.Found {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	c := res.Config
	if c.Branches.DefaultBase != "stage" || c.Update.Strategy != "rebase" {
		t.Errorf("file values lost: %+v", c)
	}
	if !c.Secrets.Enabled || !c.Hooks.Manage || c.Remote != "origin" {
		t.Errorf("defaults lost: %+v", c)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := map[string]string{
		"unknown key":     "version: 1\nbogus: true\n",
		"bad yaml":        "version: [\n",
		"bad value":       "version: 1\nupdate:\n  strategy: squash\n",
		"wrong type":      "version: 1\nsecrets:\n  enabled: maybe\n",
		"bad regex":       "version: 1\nsecrets:\n  custom_patterns:\n    - {name: x, pattern: '('}\n",
		"unsupported ver": "version: 9\n",
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, content))
			if err == nil {
				t.Fatal("expected error")
			}
			if apperr.CodeOf(err) != apperr.CodeConfig {
				t.Errorf("code = %d", apperr.CodeOf(err))
			}
			if !strings.Contains(err.Error(), FileName) {
				t.Errorf("error should name the file: %v", err)
			}
		})
	}
}

func TestLoadRejectsHugeFile(t *testing.T) {
	_, err := Load(write(t, "version: 1\n# "+strings.Repeat("x", maxFileSize)))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestInitTemplateParses(t *testing.T) {
	// guards the README example shape
	dir := write(t, `version: 1
branches:
  default_base: stage
  protected: [main, stage]
  base:
    feature/*: stage
    release/*: main
update: {strategy: merge}
commits: {convention: conventional, require_scope: false}
secrets:
  enabled: true
  custom_patterns:
    - name: openai_api_key
      pattern: "sk-[A-Za-z0-9_-]{20,}"
hooks: {manage: true}
`)
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
}
