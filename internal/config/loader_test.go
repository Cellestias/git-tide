package config

import (
	"encoding/json"
	"errors"
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

func TestViewIsTheEffectiveConfigurationAsData(t *testing.T) {
	dir := write(t, "version: 1\nremote: upstream\nbranches:\n  default_base: stage\n  protected: [main, stage]\n  base:\n    feature/*: stage\nsecurity:\n  level: HIGH\nsecrets:\n  custom_patterns:\n    - name: internal_token\n      pattern: 'corp_[A-Za-z0-9]{12}'\n")
	if err := os.WriteFile(filepath.Join(dir, SecretFileName), []byte("secrets:\n  patterns:\n    - name: local_one\n      pattern: 'zzz_[a-z]{9}'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := View(res)
	if !d.Found || !strings.HasSuffix(d.Path, FileName) || !d.SecretFileFound || d.LocalPatterns != 1 {
		t.Errorf("document = %+v", d)
	}
	c := d.Config
	if c.Remote != "upstream" || c.Branches.DefaultBase != "stage" || len(c.Branches.Protected) != 2 || c.Branches.Base["feature/*"] != "stage" ||
		c.Security.Level != "high" || c.Update.Strategy != "merge" || !c.Hooks.Manage || !c.Secrets.Enabled || len(c.Secrets.CustomPatterns) != 1 || c.Secrets.CustomPatterns[0].Name != "internal_token" {
		t.Errorf("config = %+v", c)
	}
	// the patterns of the local file are never part of the document, only how many there are
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "zzz_") || strings.Contains(string(b), "local_one") {
		t.Errorf("a local pattern leaked into the JSON: %s", b)
	}

	// without a file the document is the defaults, and says so; no list or map is null
	res, _ = Load(t.TempDir())
	b, _ = json.Marshal(View(res))
	for _, want := range []string{`"found":false`, `"default_base":"main"`, `"protected":["main"]`, `"base":{}`, `"custom_patterns":[]`, `"level":"off"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
}

func TestValidateDescribesWhyAFileIsNotValid(t *testing.T) {
	ok, err := Load(write(t, "version: 1\n"))
	if v := Validate(ok, err); !v.Valid || len(v.Errors) != 0 || !strings.HasSuffix(v.Path, FileName) {
		t.Errorf("valid = %+v", v)
	}

	missing, err := Load(t.TempDir())
	if v := Validate(missing, err); v.Valid || len(v.Errors) != 1 || !strings.Contains(v.Errors[0], "not found") {
		t.Errorf("missing = %+v", v)
	}

	bad, err := Load(write(t, "version: 1\nupdate:\n  strategy: squash\nsecurity:\n  level: paranoid\ncommits:\n  convention: nope\n"))
	if err == nil {
		t.Fatal("must be invalid")
	}
	v := Validate(bad, err)
	if v.Valid || len(v.Errors) != 3 {
		t.Fatalf("every problem is listed, one per line: %+v", v)
	}
	for i, field := range []string{"update.strategy", "commits.convention", "security.level"} {
		found := false
		for _, e := range v.Errors {
			found = found || strings.HasPrefix(e, field)
		}
		if !found {
			t.Errorf("no error for %s (%d): %v", field, i, v.Errors)
		}
	}

	// a file that does not even parse
	garbled, err := Load(write(t, "version: [1\n"))
	if v := Validate(garbled, err); v.Valid || len(v.Errors) == 0 {
		t.Errorf("garbled = %+v", v)
	}
	// and an unknown key is refused, like in the text form
	unknown, err := Load(write(t, "version: 1\nsurprise: true\n"))
	if v := Validate(unknown, err); v.Valid || !strings.Contains(strings.Join(v.Errors, " "), "surprise") {
		t.Errorf("unknown key = %+v", v)
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeConfig {
		t.Errorf("an invalid file keeps its exit-code error (3): %v", err)
	}
}
