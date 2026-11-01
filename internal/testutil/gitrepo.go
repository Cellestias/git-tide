// Package testutil builds disposable Git repositories for tests.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run executes git in dir with an isolated identity and fails the test on error.
func Run(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := TryRun(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// TryRun is Run without failing the test; it returns the combined output and the error.
func TryRun(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Git Tide Test", "GIT_AUTHOR_EMAIL=git-tide-test@example.invalid",
		"GIT_COMMITTER_NAME=Git Tide Test", "GIT_COMMITTER_EMAIL=git-tide-test@example.invalid",
	)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// WriteFile creates path (relative to dir) with content.
func WriteFile(t testing.TB, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Commit stages everything and commits (hooks are not bypassed on purpose
// only when callers install them; tests that need none do not).
func Commit(t testing.TB, dir, msg string) {
	t.Helper()
	Run(t, dir, "add", "-A")
	Run(t, dir, "commit", "-q", "-m", msg)
}

// NewRepo creates a repository on branch main with one commit.
func NewRepo(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	Run(t, dir, "init", "-q", "-b", "main")
	WriteFile(t, dir, "README.md", "hello\n")
	Commit(t, dir, "feat: initial commit")
	return dir
}

// NewRepoWithRemote creates a bare "origin", a clone of it with a main branch
// already pushed, and returns (clone, bare).
func NewRepoWithRemote(t testing.TB) (clone, bare string) {
	t.Helper()
	bare = t.TempDir()
	Run(t, bare, "init", "-q", "--bare", "-b", "main")
	clone = NewRepo(t)
	Run(t, clone, "remote", "add", "origin", bare)
	Run(t, clone, "push", "-q", "-u", "origin", "main")
	return clone, bare
}
