// Package e2e runs the compiled tide binary against disposable repositories.
package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/testutil"
)

var binDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tide-e2e-")
	if err != nil {
		panic(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "tide"), "../cmd/tide")
	if out, err := build.CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	binDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// tide runs the binary in dir and returns combined output and exit code.
// PATH includes the built binary so installed hooks can find it.
func tide(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	return runIn(t, dir, filepath.Join(binDir, "tide"), args...)
}

func runIn(t *testing.T, dir, bin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.invalid",
	)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return buf.String(), code
}

func git(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	return runIn(t, dir, "git", args...)
}

func TestExitCodes(t *testing.T) {
	notRepo := t.TempDir()
	repo := testutil.NewRepo(t)

	tests := []struct {
		name string
		dir  string
		args []string
		want int
	}{
		{"version", repo, []string{"--version"}, 0},
		{"unknown command", repo, []string{"bogus"}, 2},
		{"unknown flag", repo, []string{"init", "--nope"}, 2},
		{"review needs arg", repo, []string{"review"}, 2},
		{"outside repo", notRepo, []string{"init"}, 6},
		{"update without config", repo, []string{"update"}, 3},
		{"config validate without file", repo, []string{"config", "validate"}, 3},
		{"doctor outside repo", notRepo, []string{"doctor"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, code := tide(t, tt.dir, tt.args...)
			if code != tt.want {
				t.Fatalf("exit = %d, want %d\n%s", code, tt.want, out)
			}
		})
	}
}

func TestHooksEnforcePolicy(t *testing.T) {
	dir, bare := testutil.NewRepoWithRemote(t)
	if out, code := tide(t, dir, "init"); code != 0 {
		t.Fatalf("init: %d\n%s", code, out)
	}
	if out, code := tide(t, dir, "doctor"); code != 0 || strings.Contains(out, "✗") {
		t.Fatalf("doctor: %d\n%s", code, out)
	}

	// protected branch
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	git(t, dir, "add", "a.txt")
	if out, code := git(t, dir, "commit", "-m", "feat: on main"); code == 0 || !strings.Contains(out, "protected branch") {
		t.Fatalf("commit on main allowed:\n%s", out)
	}

	git(t, dir, "checkout", "-q", "-b", "feature/x")

	// bad message
	if out, code := git(t, dir, "commit", "-m", "added a file"); code == 0 || !strings.Contains(out, "Conventional Commits") || !strings.Contains(out, "example:") {
		t.Fatalf("bad message accepted:\n%s", out)
	}

	// secret
	fake := "AKIA" + strings.Repeat("B", 16)
	testutil.WriteFile(t, dir, "creds.txt", "id="+fake+"\n")
	git(t, dir, "add", "creds.txt")
	out, code := git(t, dir, "commit", "-m", "feat: add creds")
	if code == 0 || !strings.Contains(out, "creds.txt:1 (aws_access_key_id)") {
		t.Fatalf("secret commit allowed:\n%s", out)
	}
	if strings.Contains(out, fake) {
		t.Fatalf("secret printed:\n%s", out)
	}
	git(t, dir, "reset", "-q", "creds.txt")
	os.Remove(filepath.Join(dir, "creds.txt"))

	// good commit passes
	if out, code := git(t, dir, "commit", "-q", "-m", "feat: add a"); code != 0 {
		t.Fatalf("valid commit rejected:\n%s", out)
	}

	// push to protected branch rejected, feature push allowed
	if out, code := git(t, dir, "push", "origin", "HEAD:refs/heads/main"); code == 0 || !strings.Contains(out, "protected branch") {
		t.Fatalf("push to main allowed:\n%s", out)
	}
	if out, code := git(t, dir, "push", "-q", "origin", "feature/x"); code != 0 {
		t.Fatalf("feature push rejected:\n%s", out)
	}
	_ = bare
}

func TestHookChainingKeepsExistingHook(t *testing.T) {
	dir := testutil.NewRepo(t)
	marker := filepath.Join(dir, "mine-ran")
	hook := "#!/bin/sh\ntouch '" + marker + "'\n"
	os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, ".git", "hooks", "pre-commit"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, code := tide(t, dir, "init"); code != 0 || !strings.Contains(out, "preserved") {
		t.Fatalf("init: %d\n%s", code, out)
	}
	git(t, dir, "checkout", "-q", "-b", "feature/y")
	testutil.WriteFile(t, dir, "b.txt", "b\n")
	git(t, dir, "add", "b.txt")
	if out, code := git(t, dir, "commit", "-q", "-m", "feat: b"); code != 0 {
		t.Fatalf("commit: %s", out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("pre-existing hook did not run")
	}
}

func TestUpdateEndToEnd(t *testing.T) {
	dir, bare := testutil.NewRepoWithRemote(t)
	other := t.TempDir()
	testutil.Run(t, other, "clone", "-q", bare, ".")
	testutil.WriteFile(t, other, "up.txt", "u\n")
	testutil.Commit(t, other, "feat: upstream")
	testutil.Run(t, other, "push", "-q", "origin", "main")

	if out, code := tide(t, dir, "init", "--no-hooks"); code != 0 {
		t.Fatalf("init: %s", out)
	}
	git(t, dir, "checkout", "-q", "-b", "feature/z")
	out, code := tide(t, dir, "update")
	if code != 0 || !strings.Contains(out, "Updated") {
		t.Fatalf("update: %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "up.txt")); err != nil {
		t.Error("upstream change missing")
	}

	out, code = tide(t, dir, "config")
	if code != 0 || !strings.Contains(out, "default_base: main") {
		t.Fatalf("config: %d\n%s", code, out)
	}
}
