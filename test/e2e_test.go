// Package e2e runs the compiled tide binary against disposable repositories.
package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	return runEnv(t, dir, nil, "", bin, args...)
}

// runEnv is runIn with extra environment variables and optional stdin.
func runEnv(t *testing.T, dir string, extra []string, stdin, bin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.Env = append(append(os.Environ(), extra...),
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
		{"pr without config", repo, []string{"pr"}, 3},
		{"create needs arg", repo, []string{"create"}, 2},
		{"branch without config", repo, []string{"branch"}, 3},
		{"change needs arg", repo, []string{"change"}, 2},
		{"change without config", repo, []string{"change", "x"}, 3},
		{"create without config", repo, []string{"create", "feature/x"}, 3},
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

func TestPREndToEnd(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"number":42,"html_url":"https://github.com/acme/tool/pull/42"}`))
	}))
	defer srv.Close()
	apiEnv := []string{"TIDE_GITHUB_API_URL=" + srv.URL, "GITHUB_TOKEN=test-token", "GH_TOKEN="}

	dir, bare := testutil.NewRepoWithRemote(t)
	testutil.Run(t, dir, "remote", "set-url", "origin", "https://github.com/acme/tool.git")
	testutil.Run(t, dir, "config", "url."+bare+".insteadOf", "https://github.com/acme/tool.git")
	if out, code := tide(t, dir, "init"); code != 0 {
		t.Fatalf("init: %s", out)
	}
	git(t, dir, "checkout", "-q", "-b", "feature/pay")
	testutil.WriteFile(t, dir, "pay.txt", "p\n")
	git(t, dir, "add", "pay.txt")
	if out, code := git(t, dir, "commit", "-q", "-m", "feat: add pay"); code != 0 {
		t.Fatalf("commit: %s", out)
	}
	bin := filepath.Join(binDir, "tide")

	// no token → configuration error, nothing pushed
	out, code := runEnv(t, dir, []string{"GITHUB_TOKEN=", "GH_TOKEN="}, "", bin, "pr", "--yes")
	if code != 3 || !strings.Contains(out, "GITHUB_TOKEN") {
		t.Fatalf("no token: %d\n%s", code, out)
	}

	// declined → exit 1, nothing pushed
	out, code = runEnv(t, dir, apiEnv, "n\n", bin, "pr")
	if code != 1 || !strings.Contains(out, "nothing was pushed") || got != nil {
		t.Fatalf("declined: %d\n%s", code, out)
	}
	if o, _ := git(t, bare, "branch", "--list", "feature/*"); strings.TrimSpace(o) != "" {
		t.Fatal("branch pushed despite refusal")
	}

	// confirmed
	out, code = runEnv(t, dir, apiEnv, "y\n", bin, "pr", "--draft")
	if code != 0 || !strings.Contains(out, "pull/42") {
		t.Fatalf("confirmed: %d\n%s", code, out)
	}
	if auth != "Bearer test-token" || got["head"] != "feature/pay" || got["base"] != "main" || got["draft"] != true || got["title"] != "feat: add pay" {
		t.Fatalf("request: auth=%q body=%v", auth, got)
	}
	if o, _ := git(t, bare, "branch", "--list", "feature/pay"); !strings.Contains(o, "feature/pay") {
		t.Fatal("branch not pushed")
	}
}

func TestCreateEndToEnd(t *testing.T) {
	dir, bare := testutil.NewRepoWithRemote(t)
	other := t.TempDir()
	testutil.Run(t, other, "clone", "-q", bare, ".")
	testutil.WriteFile(t, other, "up.txt", "u\n")
	testutil.Commit(t, other, "feat: upstream")
	testutil.Run(t, other, "push", "-q", "origin", "main")
	tip := testutil.Run(t, other, "rev-parse", "HEAD")

	if out, code := tide(t, dir, "init"); code != 0 {
		t.Fatalf("init: %s", out)
	}
	out, code := tide(t, dir, "create", "feature/two-fa", "--push")
	if code != 0 || !strings.Contains(out, "Created and switched to feature/two-fa") {
		t.Fatalf("create: %d\n%s", code, out)
	}
	if testutil.Run(t, dir, "rev-parse", "HEAD") != tip {
		t.Error("branch not started from the fresh remote base")
	}
	if testutil.Run(t, bare, "rev-parse", "refs/heads/feature/two-fa") != tip {
		t.Error("branch not pushed")
	}
	if out, code := tide(t, dir, "create", "feature/two-fa"); code != 6 {
		t.Fatalf("duplicate: %d\n%s", code, out)
	}
	if out, code := tide(t, dir, "create", "main"); code != 4 {
		t.Fatalf("protected name: %d\n%s", code, out)
	}
}

func TestBranchEndToEnd(t *testing.T) {
	dir, bare := testutil.NewRepoWithRemote(t)
	testutil.Run(t, bare, "branch", "colleague/x", "main")
	if out, code := tide(t, dir, "init", "--no-hooks"); code != 0 {
		t.Fatalf("init: %s", out)
	}
	git(t, dir, "checkout", "-q", "-b", "feature/z")

	out, code := tide(t, dir, "branch")
	if code != 0 || !strings.HasPrefix(out, "BRANCH") || !strings.Contains(out, "* feature/z") || strings.Contains(out, "colleague/x") {
		t.Fatalf("branch: %d\n%s", code, out)
	}
	out, code = tide(t, dir, "branch", "--remote", "--sort", "name")
	if code != 0 || !strings.Contains(out, "colleague/x") || !strings.Contains(out, "remote-only") {
		t.Fatalf("branch --remote: %d\n%s", code, out)
	}
	if out, code := tide(t, dir, "branch", "--sort", "bogus"); code != 2 {
		t.Fatalf("bad sort: %d\n%s", code, out)
	}
	if out, code := tide(t, dir, "branches", "--merged"); code != 0 {
		t.Fatalf("alias: %d\n%s", code, out)
	}
}

func TestChangeEndToEnd(t *testing.T) {
	dir, _ := testutil.NewRepoWithRemote(t)
	if out, code := tide(t, dir, "init", "--no-hooks"); code != 0 {
		t.Fatalf("init: %s", out)
	}
	git(t, dir, "branch", "feature/two-fa")
	git(t, dir, "branch", "feature/login")

	if out, code := tide(t, dir, "change", "two-fa"); code != 0 || !strings.Contains(out, "Switched to feature/two-fa") {
		t.Fatalf("partial: %d\n%s", code, out)
	}
	if out, code := tide(t, dir, "switch", "-"); code != 0 || !strings.Contains(out, "Switched to main") {
		t.Fatalf("alias and previous: %d\n%s", code, out)
	}
	if out, code := tide(t, dir, "change", "feature"); code != 6 || !strings.Contains(out, "matches 2 branches") {
		t.Fatalf("ambiguous: %d\n%s", code, out)
	}
	testutil.WriteFile(t, dir, "README.md", "dirty\n")
	if out, code := tide(t, dir, "change", "login"); code != 6 || !strings.Contains(out, "--carry") {
		t.Fatalf("dirty: %d\n%s", code, out)
	}
	if out, code := tide(t, dir, "change", "login", "--carry"); code != 0 {
		t.Fatalf("carry: %d\n%s", code, out)
	}
}
