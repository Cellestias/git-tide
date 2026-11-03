package dotenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse(t *testing.T) {
	in := "# comment\n\nOTHER=ignored\nGITHUB_TOKEN = ghp_abc  # inline\nexport GH_TOKEN=\"quoted value\"\nEMPTY=\nSQ='single # kept'\nGITHUB_TOKEN=ghp_last\nnot a pair\n"
	got := Parse([]byte(in), "GITHUB_TOKEN", "GH_TOKEN", "EMPTY", "SQ", "MISSING")
	want := map[string]string{"GITHUB_TOKEN": "ghp_last", "GH_TOKEN": "quoted value", "EMPTY": "", "SQ": "single # kept"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["OTHER"]; ok {
		t.Error("keys that were not asked for must not be returned")
	}
}

func TestParseCRLF(t *testing.T) {
	if got := Parse([]byte("GITHUB_TOKEN=ghp_x\r\n"), "GITHUB_TOKEN")["GITHUB_TOKEN"]; got != "ghp_x" {
		t.Errorf("got %q", got)
	}
}

func TestReadMissingFileIsEmpty(t *testing.T) {
	got, err := Read(filepath.Join(t.TempDir(), ".env"), "GITHUB_TOKEN")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestReadRejectsNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("GITHUB_TOKEN=ghp_x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".env")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := Read(link, "GITHUB_TOKEN"); err == nil {
		t.Error("a symlink must be rejected")
	}
	if _, err := Read(dir, "GITHUB_TOKEN"); err == nil {
		t.Error("a directory must be rejected")
	}
}

func TestReadIsBounded(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	big := make([]byte, 0, maxSize*2)
	for len(big) < maxSize+10 {
		big = append(big, "PAD=x\n"...)
	}
	big = append(big, "GITHUB_TOKEN=ghp_late\n"...)
	if err := os.WriteFile(p, big, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Read(p, "GITHUB_TOKEN")
	if err != nil || got["GITHUB_TOKEN"] != "" {
		t.Errorf("content past the size cap must be ignored: %v, %v", got, err)
	}
}
