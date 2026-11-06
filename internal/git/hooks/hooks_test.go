package hooks

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallFresh(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hooks")
	changes, err := Install(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != len(Managed) {
		t.Fatalf("changes = %+v", changes)
	}
	for _, h := range Managed {
		path := filepath.Join(dir, h.Name)
		info, err := os.Stat(path)
		if err != nil || info.Mode()&0o111 == 0 {
			t.Fatalf("%s not executable: %v", h.Name, err)
		}
		data, _ := os.ReadFile(path)
		if !bytes.Contains(data, []byte(Marker)) || !bytes.Contains(data, []byte("gtide hook "+h.Name)) {
			t.Errorf("%s content unexpected:\n%s", h.Name, data)
		}
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	changes, err := Install(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		if c.Action != Unchanged {
			t.Errorf("%s: %s", c.Hook, c.Action)
		}
	}
}

func TestInstallPreservesForeignHook(t *testing.T) {
	dir := t.TempDir()
	mine := "#!/bin/sh\necho mine\n"
	if err := os.WriteFile(filepath.Join(dir, "pre-commit"), []byte(mine), 0o755); err != nil {
		t.Fatal(err)
	}
	changes, err := Install(dir)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range changes {
		if c.Hook == "pre-commit" {
			found = true
			if c.Action != Chained || !strings.Contains(c.Describe(), "pre-commit"+ChainSuffix) {
				t.Errorf("change = %+v / %s", c, c.Describe())
			}
		}
	}
	if !found {
		t.Fatal("pre-commit not reported")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pre-commit"+ChainSuffix))
	if string(got) != mine {
		t.Fatalf("original hook not preserved: %q", got)
	}
}

func TestInstallRefusesToClobberChain(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pre-commit"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "pre-commit"+ChainSuffix), []byte("#!/bin/sh\nold\n"), 0o755)
	if _, err := Install(dir); err == nil {
		t.Fatal("expected error")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pre-commit"))
	if string(got) != "#!/bin/sh\n" {
		t.Fatalf("user hook modified: %q", got)
	}
}

func TestInspect(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "commit-msg"), []byte("#!/bin/sh\n# tide-managed-hook v0\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "pre-push"), []byte("#!/bin/sh\nmine\n"), 0o755)
	st, err := Inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]State{}
	for _, s := range st {
		got[s.Hook] = s.State
	}
	if got["pre-commit"] != Missing || got["commit-msg"] != Outdated || got["pre-push"] != Foreign {
		t.Fatalf("states = %v", got)
	}
	Install(t.TempDir())
}
