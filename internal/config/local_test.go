package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	domain "github.com/cellestias/git-tide/internal/domain/config"
)

func writeSecret(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, SecretFileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadLocalMissing(t *testing.T) {
	res, err := LoadLocal(t.TempDir())
	if err != nil || res.Found {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

func TestLoadLocalReadsReferenceAndPatterns(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "provider_token: ${GITLAB_TOKEN}\nsecrets:\n  patterns:\n    - name: AWS Access Key\n      pattern: 'AKIA[0-9A-Z]{16}'\n")
	res, err := LoadLocal(dir)
	if err != nil || !res.Found {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if v, ok := res.Local.TokenVar(); v != "GITLAB_TOKEN" || !ok {
		t.Errorf("TokenVar = %q, %v", v, ok)
	}
	if len(res.Local.Secrets.Patterns) != 1 || res.Local.Secrets.Patterns[0].Name != "AWS Access Key" {
		t.Errorf("patterns = %+v", res.Local.Secrets.Patterns)
	}
}

func TestLoadLocalRejectsLiteralToken(t *testing.T) {
	for _, tok := range []string{"ghp_abcdefghijklmnop", "glpat-xxxxxxxx", "prefix${X}", "${X}suffix", "${1BAD}", "$X"} {
		dir := t.TempDir()
		writeSecret(t, dir, "provider_token: "+tok+"\n")
		_, err := LoadLocal(dir)
		if err == nil || !strings.Contains(err.Error(), "environment variable reference") {
			t.Errorf("%q: err = %v", tok, err)
		}
		if err != nil && strings.Contains(err.Error(), tok) {
			t.Errorf("error echoes the token value: %v", err)
		}
	}
}

func TestLoadLocalRejectsBadPatternsUnknownKeysAndSymlink(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "secrets:\n  patterns:\n    - name: x\n      pattern: '('\n")
	if _, err := LoadLocal(dir); err == nil || !strings.Contains(err.Error(), "secrets.patterns[0].pattern") {
		t.Errorf("bad pattern: %v", err)
	}
	writeSecret(t, dir, "token: abc\n")
	if _, err := LoadLocal(dir); err == nil {
		t.Error("unknown key must be rejected")
	}
	dir2 := t.TempDir()
	target := filepath.Join(dir2, "elsewhere")
	_ = os.WriteFile(target, []byte("provider_token: ${X}\n"), 0o600)
	if err := os.Symlink(target, filepath.Join(dir2, SecretFileName)); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := LoadLocal(dir2); err == nil {
		t.Error("symlink must be rejected")
	}
}

func TestLoadMergesLocalPatternsWithoutExposingThem(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, FileName), []byte("version: 1\nprovider: gitlab\n"), 0o644)
	writeSecret(t, dir, "secrets:\n  patterns:\n    - name: corp\n      pattern: 'corp_[0-9]{4}'\n")
	res, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Provider != "gitlab" {
		t.Errorf("provider = %q", res.Config.Provider)
	}
	if got := len(res.Config.SecretRules()); got != len(domain.Default().SecretRules())+1 {
		t.Errorf("rules = %d", got)
	}
	out, err := Marshal(res.Config)
	if err != nil || strings.Contains(string(out), "corp_") {
		t.Errorf("local pattern leaked into the shared view:\n%s", out)
	}
}

func TestProviderValidation(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, FileName), []byte("version: 1\nprovider: bitbucket\n"), 0o644)
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "provider:") {
		t.Errorf("err = %v", err)
	}
	empty := t.TempDir()
	res, err := Load(empty)
	if err != nil || res.Config.Provider != "auto" {
		t.Errorf("default provider = %q, %v", res.Config.Provider, err)
	}
}
