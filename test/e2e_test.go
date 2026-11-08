// Package e2e runs the compiled gtide binary against disposable repositories.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cellestias/git-tide/internal/testutil"
)

var binDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tide-e2e-")
	if err != nil {
		panic(err)
	}
	build := exec.CommandContext(context.Background(), "go", "build", "-o", filepath.Join(dir, "gtide"), "../cmd/gtide")
	if out, err := build.CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	binDir = dir
	// Hooks call `gtide` from PATH; make sure every git call in the tests sees the
	// freshly built binary and not one installed on the machine.
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// gtide runs the binary in dir and returns combined output and exit code.
// PATH includes the built binary so installed hooks can find it.
func gtide(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	return runIn(t, dir, filepath.Join(binDir, "gtide"), args...)
}

func runIn(t *testing.T, dir, bin string, args ...string) (string, int) {
	t.Helper()
	return runEnv(t, dir, nil, "", bin, args...)
}

// runEnv is runIn with extra environment variables and optional stdin.
func runEnv(t *testing.T, dir string, extra []string, stdin, bin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), bin, args...)
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
	var ee *exec.ExitError
	if errors.As(err, &ee) {
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
			out, code := gtide(t, tt.dir, tt.args...)
			if code != tt.want {
				t.Fatalf("exit = %d, want %d\n%s", code, tt.want, out)
			}
		})
	}
}

func TestHooksEnforcePolicy(t *testing.T) {
	dir, bare := testutil.NewRepoWithRemote(t)
	if out, code := gtide(t, dir, "init"); code != 0 {
		t.Fatalf("init: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "doctor"); code != 0 || strings.Contains(out, "✗") {
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
	if out, code := gtide(t, dir, "init"); code != 0 || !strings.Contains(out, "preserved") {
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

	if out, code := gtide(t, dir, "init", "--no-hooks"); code != 0 {
		t.Fatalf("init: %s", out)
	}
	git(t, dir, "checkout", "-q", "-b", "feature/z")
	out, code := gtide(t, dir, "update")
	if code != 0 || !strings.Contains(out, "Updated") {
		t.Fatalf("update: %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "up.txt")); err != nil {
		t.Error("upstream change missing")
	}

	out, code = gtide(t, dir, "config")
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
	if out, code := gtide(t, dir, "init"); code != 0 {
		t.Fatalf("init: %s", out)
	}
	git(t, dir, "checkout", "-q", "-b", "feature/pay")
	testutil.WriteFile(t, dir, "pay.txt", "p\n")
	git(t, dir, "add", "pay.txt")
	if out, code := git(t, dir, "commit", "-q", "-m", "feat: add pay"); code != 0 {
		t.Fatalf("commit: %s", out)
	}
	bin := filepath.Join(binDir, "gtide")

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

	if out, code := gtide(t, dir, "init"); code != 0 {
		t.Fatalf("init: %s", out)
	}
	out, code := gtide(t, dir, "create", "feature/two-fa", "--push")
	if code != 0 || !strings.Contains(out, "Created and switched to feature/two-fa") {
		t.Fatalf("create: %d\n%s", code, out)
	}
	if testutil.Run(t, dir, "rev-parse", "HEAD") != tip {
		t.Error("branch not started from the fresh remote base")
	}
	if testutil.Run(t, bare, "rev-parse", "refs/heads/feature/two-fa") != tip {
		t.Error("branch not pushed")
	}
	if out, code := gtide(t, dir, "create", "feature/two-fa"); code != 6 {
		t.Fatalf("duplicate: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "create", "main"); code != 4 {
		t.Fatalf("protected name: %d\n%s", code, out)
	}
}

func TestBranchEndToEnd(t *testing.T) {
	dir, bare := testutil.NewRepoWithRemote(t)
	testutil.Run(t, bare, "branch", "colleague/x", "main")
	if out, code := gtide(t, dir, "init", "--no-hooks"); code != 0 {
		t.Fatalf("init: %s", out)
	}
	git(t, dir, "checkout", "-q", "-b", "feature/z")

	out, code := gtide(t, dir, "branch")
	if code != 0 || !strings.HasPrefix(out, "BRANCH") || !strings.Contains(out, "* feature/z") || strings.Contains(out, "colleague/x") {
		t.Fatalf("branch: %d\n%s", code, out)
	}
	out, code = gtide(t, dir, "branch", "--remote", "--sort", "name")
	if code != 0 || !strings.Contains(out, "colleague/x") || !strings.Contains(out, "remote-only") {
		t.Fatalf("branch --remote: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "branch", "--sort", "bogus"); code != 2 {
		t.Fatalf("bad sort: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "branches", "--merged"); code != 0 {
		t.Fatalf("alias: %d\n%s", code, out)
	}
}

func TestChangeEndToEnd(t *testing.T) {
	dir, _ := testutil.NewRepoWithRemote(t)
	if out, code := gtide(t, dir, "init", "--no-hooks"); code != 0 {
		t.Fatalf("init: %s", out)
	}
	git(t, dir, "branch", "feature/two-fa")
	git(t, dir, "branch", "feature/login")

	if out, code := gtide(t, dir, "change", "two-fa"); code != 0 || !strings.Contains(out, "Switched to feature/two-fa") {
		t.Fatalf("partial: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "switch", "-"); code != 0 || !strings.Contains(out, "Switched to main") {
		t.Fatalf("alias and previous: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "change", "feature"); code != 6 || !strings.Contains(out, "matches 2 branches") {
		t.Fatalf("ambiguous: %d\n%s", code, out)
	}
	testutil.WriteFile(t, dir, "README.md", "dirty\n")
	if out, code := gtide(t, dir, "change", "login"); code != 6 || !strings.Contains(out, "--carry") {
		t.Fatalf("dirty: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "change", "login", "--carry"); code != 0 {
		t.Fatalf("carry: %d\n%s", code, out)
	}
}

func TestPushEndToEnd(t *testing.T) {
	dir, bare := testutil.NewRepoWithRemote(t)
	if out, code := gtide(t, dir, "init"); code != 0 {
		t.Fatalf("init: %s", out)
	}
	if out, code := gtide(t, dir, "push"); code != 4 {
		t.Fatalf("push on protected main: %d\n%s", code, out)
	}

	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/a")
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	testutil.Commit(t, dir, "feat: a")
	out, code := gtide(t, dir, "push")
	if code != 0 || !strings.Contains(out, "Pushed feature/a") {
		t.Fatalf("push: %d\n%s", code, out)
	}
	if testutil.Run(t, bare, "rev-parse", "refs/heads/feature/a") != testutil.Run(t, dir, "rev-parse", "HEAD") {
		t.Error("branch not pushed")
	}

	testutil.Run(t, dir, "commit", "-q", "--amend", "-m", "feat: a again")
	if out, code := gtide(t, dir, "push"); code != 5 {
		t.Fatalf("push of rewritten history: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "push", "--force", "--yes"); code != 0 {
		t.Fatalf("force push: %d\n%s", code, out)
	}
}

func TestSaveAndCommitEndToEnd(t *testing.T) {
	dir, _ := testutil.NewRepoWithRemote(t)
	if out, code := gtide(t, dir, "init"); code != 0 {
		t.Fatalf("init: %s", out)
	}
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	if out, code := gtide(t, dir, "save", "-m", "feat: add a", "-f", "a.txt"); code != 4 {
		t.Fatalf("save on protected main: %d\n%s", code, out)
	}

	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/a")
	if out, code := gtide(t, dir, "save", "-m", "added a", "-f", "a.txt"); code != 4 {
		t.Fatalf("invalid message: %d\n%s", code, out)
	}
	out, code := gtide(t, dir, "save", "--type", "feat", "--scope", "a", "-m", "add a", "-f", "a.txt")
	if code != 0 || !strings.Contains(out, "Committed") {
		t.Fatalf("save: %d\n%s", code, out)
	}
	if got := testutil.Run(t, dir, "log", "-1", "--format=%s"); got != "feat(a): add a" {
		t.Errorf("subject = %q", got)
	}

	testutil.WriteFile(t, dir, "b.txt", "b\n")
	if out, code := gtide(t, dir, "save", "-m", "feat: add b"); code != 0 || !strings.Contains(out, "b.txt") {
		t.Fatalf("save without -f: %d\n%s", code, out)
	}

	testutil.WriteFile(t, dir, "a.txt", "changed\n")
	if out, code := gtide(t, dir, "commit", "-m", "fix: change a"); code != 6 {
		t.Fatalf("commit with nothing staged: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "commit", "-a", "-m", "fix: change a"); code != 0 {
		t.Fatalf("commit -a: %d\n%s", code, out)
	}
}

func TestContinueAndAbortEndToEnd(t *testing.T) {
	dir := testutil.NewRepo(t)
	if out, code := gtide(t, dir, "init", "--no-hooks"); code != 0 {
		t.Fatalf("init: %s", out)
	}
	if out, code := gtide(t, dir, "continue"); code != 6 {
		t.Fatalf("continue with nothing in progress: %d\n%s", code, out)
	}
	testutil.WriteFile(t, dir, "f.txt", "base\n")
	testutil.Commit(t, dir, "feat: base")
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/b")
	testutil.WriteFile(t, dir, "f.txt", "from b\n")
	testutil.Commit(t, dir, "feat: b")
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/a", "main")
	testutil.WriteFile(t, dir, "f.txt", "from a\n")
	testutil.Commit(t, dir, "feat: a")

	git(t, dir, "merge", "--no-edit", "feature/b") // conflicts
	if out, code := gtide(t, dir, "continue"); code != 6 || !strings.Contains(out, "f.txt") {
		t.Fatalf("continue with conflicts left: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "abort", "--yes"); code != 0 {
		t.Fatalf("abort: %d\n%s", code, out)
	}
	if _, code := git(t, dir, "rev-parse", "-q", "--verify", "MERGE_HEAD"); code == 0 {
		t.Error("merge still in progress after abort")
	}

	git(t, dir, "merge", "--no-edit", "feature/b") // conflicts again
	testutil.WriteFile(t, dir, "f.txt", "resolved\n")
	testutil.Run(t, dir, "add", "f.txt")
	if out, code := gtide(t, dir, "continue"); code != 0 || !strings.Contains(out, "Finished the merge") {
		t.Fatalf("continue: %d\n%s", code, out)
	}
}

func TestPRTokenFromDotenv(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"number":7,"html_url":"https://github.com/acme/tool/pull/7"}`))
	}))
	defer srv.Close()
	noToken := []string{"TIDE_GITHUB_API_URL=" + srv.URL, "GITHUB_TOKEN=", "GH_TOKEN="}

	dir, bare := testutil.NewRepoWithRemote(t)
	testutil.Run(t, dir, "remote", "set-url", "origin", "https://github.com/acme/tool.git")
	testutil.Run(t, dir, "config", "url."+bare+".insteadOf", "https://github.com/acme/tool.git")
	if out, code := gtide(t, dir, "init"); code != 0 {
		t.Fatalf("init: %s", out)
	}
	git(t, dir, "checkout", "-q", "-b", "feature/pay")
	testutil.WriteFile(t, dir, "pay.txt", "p\n")
	git(t, dir, "add", "pay.txt")
	git(t, dir, "commit", "-q", "-m", "feat: add pay")
	bin := filepath.Join(binDir, "gtide")

	// a tracked .env is never used
	testutil.WriteFile(t, dir, ".env", "GITHUB_TOKEN=from-dotenv\n")
	git(t, dir, "add", "-f", ".env")
	git(t, dir, "commit", "-q", "-m", "chore: oops")
	out, code := runEnv(t, dir, noToken, "", bin, "pr", "--yes")
	if code != 3 || !strings.Contains(out, "tracked by Git") || strings.Contains(out, "from-dotenv") {
		t.Fatalf("tracked .env: %d\n%s", code, out)
	}
	git(t, dir, "rm", "-q", "--cached", ".env")
	git(t, dir, "commit", "-q", "-m", "chore: untrack env")
	testutil.WriteFile(t, dir, ".gitignore", ".env\n")
	git(t, dir, "add", ".gitignore")
	git(t, dir, "commit", "-q", "-m", "chore: ignore env")

	// an ignored .env supplies the token
	out, code = runEnv(t, dir, noToken, "", bin, "pr", "--yes")
	if code != 0 || auth != "Bearer from-dotenv" {
		t.Fatalf("dotenv token: %d auth=%q\n%s", code, auth, out)
	}
}

// gitlabRepo returns a clone whose origin looks like a gitlab.com project but
// really points at a local bare repository, already initialised with provider auto.
func gitlabRepo(t *testing.T) (dir, bare string) {
	t.Helper()
	dir, bare = testutil.NewRepoWithRemote(t)
	testutil.Run(t, dir, "remote", "set-url", "origin", "git@gitlab.com:acme/group/tool.git")
	testutil.Run(t, dir, "config", "url."+bare+".insteadOf", "git@gitlab.com:acme/group/tool.git")
	out, code := gtide(t, dir, "init")
	if code != 0 {
		t.Fatalf("init: %s", out)
	}
	for _, want := range []string{"provider: auto", "created", "secret.tide.yml", ".gitignore"} {
		if !strings.Contains(out, want) {
			t.Fatalf("init output lacks %q:\n%s", want, out)
		}
	}
	git(t, dir, "checkout", "-q", "-b", "feature/pay")
	testutil.WriteFile(t, dir, "pay.txt", "p\n")
	git(t, dir, "add", "pay.txt")
	if out, code := git(t, dir, "commit", "-q", "-m", "feat: add pay"); code != 0 {
		t.Fatalf("commit: %s", out)
	}
	return dir, bare
}

func TestPRGitLabEndToEnd(t *testing.T) {
	var got map[string]any
	var token, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, path = r.Header.Get("PRIVATE-TOKEN"), r.URL.EscapedPath()
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"iid":5,"web_url":"https://gitlab.com/acme/group/tool/-/merge_requests/5"}`))
	}))
	defer srv.Close()
	dir, bare := gitlabRepo(t)
	bin := filepath.Join(binDir, "gtide")

	// init generated the local file, ignored by Git, referencing the GitLab variable
	secret, err := os.ReadFile(filepath.Join(dir, "secret.tide.yml"))
	if err != nil || !strings.Contains(string(secret), "provider_token: ${GITLAB_TOKEN}") {
		t.Fatalf("secret.tide.yml: %v\n%s", err, secret)
	}
	if o, _ := git(t, dir, "status", "--porcelain", "--", "secret.tide.yml"); strings.TrimSpace(o) != "" {
		t.Fatalf("secret.tide.yml is not ignored:\n%s", o)
	}

	apiEnv := []string{"TIDE_GITLAB_API_URL=" + srv.URL + "/api/v4", "GITLAB_TOKEN=glpat-test", "GH_TOKEN=", "GITHUB_TOKEN="}
	noToken := []string{"TIDE_GITLAB_API_URL=" + srv.URL + "/api/v4", "GITLAB_TOKEN=", "GH_TOKEN=", "GITHUB_TOKEN="}

	// no token: the message names the GitLab variable, nothing is pushed
	out, code := runEnv(t, dir, noToken, "", bin, "pr", "--yes")
	if code != 3 || !strings.Contains(out, "GITLAB_TOKEN") {
		t.Fatalf("no token: %d\n%s", code, out)
	}

	out, code = runEnv(t, dir, apiEnv, "y\n", bin, "pr", "--draft")
	if code != 0 || !strings.Contains(out, "merge_requests/5") || !strings.Contains(out, "!5") || !strings.Contains(out, "Merge request") {
		t.Fatalf("pr: %d\n%s", code, out)
	}
	if token != "glpat-test" || path != "/api/v4/projects/acme%2Fgroup%2Ftool/merge_requests" {
		t.Fatalf("request: token=%q path=%q", token, path)
	}
	if got["source_branch"] != "feature/pay" || got["target_branch"] != "main" || got["title"] != "Draft: feat: add pay" {
		t.Fatalf("body: %v", got)
	}
	if o, _ := git(t, bare, "branch", "--list", "feature/pay"); !strings.Contains(o, "feature/pay") {
		t.Fatal("branch not pushed")
	}
}

func TestPRTokenFromSecretFileReference(t *testing.T) {
	var token string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token = r.Header.Get("PRIVATE-TOKEN")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"iid":6,"web_url":"https://gitlab.com/acme/group/tool/-/merge_requests/6"}`))
	}))
	defer srv.Close()
	dir, _ := gitlabRepo(t)
	bin := filepath.Join(binDir, "gtide")
	// explicit provider overrides detection, and the file may reference any variable
	testutil.WriteFile(t, dir, ".tide.yml", strings.Replace(read(t, filepath.Join(dir, ".tide.yml")), "provider: auto", "provider: gitlab", 1))
	testutil.WriteFile(t, dir, "secret.tide.yml", "provider_token: ${CORP_GITLAB_PAT}\n")
	env := []string{"TIDE_GITLAB_API_URL=" + srv.URL + "/api/v4", "GITLAB_TOKEN=", "GH_TOKEN=", "GITHUB_TOKEN=", "CORP_GITLAB_PAT=glpat-corp"}
	if out, code := runEnv(t, dir, env, "", bin, "pr", "--yes"); code != 0 || token != "glpat-corp" {
		t.Fatalf("pr: %d token=%q\n%s", code, token, out)
	}

	// a literal token in the file is refused and never echoed
	testutil.WriteFile(t, dir, "secret.tide.yml", "provider_token: glpat-literalliteral\n")
	out, code := runEnv(t, dir, env, "", bin, "pr", "--yes")
	if code == 0 || strings.Contains(out, "glpat-literalliteral") {
		t.Fatalf("literal token: %d\n%s", code, out)
	}
}

func TestSecretFilePatternsBlockCommit(t *testing.T) {
	dir, _ := testutil.NewRepoWithRemote(t)
	if out, code := gtide(t, dir, "init"); code != 0 {
		t.Fatalf("init: %s", out)
	}
	testutil.WriteFile(t, dir, "secret.tide.yml", "secrets:\n  patterns:\n    - name: Corp Key\n      pattern: 'corp_[0-9]{6}'\n")
	git(t, dir, "checkout", "-q", "-b", "feature/x")
	testutil.WriteFile(t, dir, "x.txt", "key=corp_123456\n")
	git(t, dir, "add", "x.txt")
	out, code := git(t, dir, "commit", "-q", "-m", "feat: add x")
	if code == 0 || !strings.Contains(out, "Corp Key") || strings.Contains(out, "corp_123456") {
		t.Fatalf("local pattern not enforced (or the secret was printed): %d\n%s", code, out)
	}
	// the secret file itself is ignored, so it can never be committed by accident
	git(t, dir, "add", "-A")
	if o, _ := git(t, dir, "diff", "--cached", "--name-only"); strings.Contains(o, "secret.tide.yml") {
		t.Fatalf("secret.tide.yml got staged:\n%s", o)
	}
}

func TestInitIsIdempotentAndHonoursProvider(t *testing.T) {
	dir, _ := testutil.NewRepoWithRemote(t)
	if out, code := gtide(t, dir, "init", "--provider", "gitlab", "--no-hooks"); code != 0 || !strings.Contains(out, "provider: gitlab") {
		t.Fatalf("init: %d\n%s", code, out)
	}
	before := read(t, filepath.Join(dir, ".gitignore"))
	out, code := gtide(t, dir, "init", "--no-hooks")
	if code != 0 || strings.Count(out, "preserved") != 3 {
		t.Fatalf("second init: %d\n%s", code, out)
	}
	if read(t, filepath.Join(dir, ".gitignore")) != before || strings.Count(before, "secret.tide.yml") != 1 {
		t.Fatalf(".gitignore changed or duplicated:\n%q", before)
	}
	if out, code := gtide(t, dir, "init", "--provider", "bitbucket"); code != 2 {
		t.Fatalf("bad provider: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "config"); code != 0 || !strings.Contains(out, "provider: gitlab") {
		t.Fatalf("config: %d\n%s", code, out)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPRWorkflowGitLabEndToEnd(t *testing.T) {
	var mu sync.Mutex
	queries := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.EscapedPath()
		mu.Lock()
		queries[strings.TrimPrefix(path, "/api/v4/projects/acme%2Fgroup%2Ftool")] = r.URL.RawQuery
		mu.Unlock()
		const mr = `{"iid":5,"title":"Add pay\u001b[31m","description":"Pays things","state":"opened","draft":false,"web_url":"https://gitlab.com/acme/group/tool/-/merge_requests/5",
			"source_branch":"feature/pay","target_branch":"main","source_project_id":1,"target_project_id":1,"author":{"username":"ana"},
			"assignees":[{"username":"bia"}],"reviewers":[{"username":"caio"}],"created_at":"2026-11-01T10:00:00Z","updated_at":"2026-11-02T10:00:00Z"}`
		switch strings.TrimPrefix(path, "/api/v4") {
		case "/user":
			w.Write([]byte(`{"username":"ana"}`))
		case "/projects/acme%2Fgroup%2Ftool/merge_requests":
			w.Write([]byte("[" + mr + "]"))
		case "/projects/acme%2Fgroup%2Ftool/merge_requests/5":
			w.Write([]byte(mr))
		case "/projects/acme%2Fgroup%2Ftool/merge_requests/5/diffs":
			w.Write([]byte(`[{"old_path":"pay.txt","new_path":"pay.txt","new_file":true}]`))
		case "/projects/acme%2Fgroup%2Ftool/merge_requests/5/commits":
			w.Write([]byte(`[{"id":"abcdef1234567","title":"feat: add pay"}]`))
		case "/projects/acme%2Fgroup%2Ftool/merge_requests/5/approvals":
			w.Write([]byte(`{"approved_by":[{"user":{"username":"dani"}}],"approvals_left":0}`))
		default:
			t.Errorf("unexpected request %s", path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	dir, _ := gitlabRepo(t)
	bin := filepath.Join(binDir, "gtide")
	env := []string{"TIDE_GITLAB_API_URL=" + srv.URL + "/api/v4", "GITLAB_TOKEN=glpat-test", "GH_TOKEN=", "GITHUB_TOKEN="}
	// the merge request's head as GitLab publishes it
	git(t, dir, "push", "-q", "origin", "feature/pay:refs/merge-requests/5/head")

	out, code := runEnv(t, dir, env, "", bin, "pr", "list", "--state", "all", "--author", "me", "--limit", "5")
	if code != 0 || !strings.Contains(out, "!5") || !strings.Contains(out, "ana") || !strings.Contains(out, "feature/pay") {
		t.Fatalf("pr list: %d\n%s", code, out)
	}
	if strings.Contains(out, "\x1b") {
		t.Fatalf("control characters in list output:\n%q", out)
	}
	mu.Lock()
	q := queries["/merge_requests"]
	mu.Unlock()
	if !strings.Contains(q, "author_username=ana") || !strings.Contains(q, "state=all") {
		t.Fatalf("list query = %q", q)
	}

	out, code = runEnv(t, dir, env, "", bin, "pr", "view", "5")
	for _, want := range []string{"!5 Add pay", "Pays things", "feature/pay → main", "bia", "approved by dani", "waiting on caio", "A  pay.txt", "abcdef1  feat: add pay"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("pr view lacks %q (exit %d):\n%s", want, code, out)
		}
	}

	out, code = runEnv(t, dir, env, "", bin, "pr", "status")
	if code != 0 || !strings.Contains(out, "!5") || !strings.Contains(out, "approved by dani") {
		t.Fatalf("pr status: %d\n%s", code, out)
	}
	mu.Lock()
	q = queries["/merge_requests"]
	mu.Unlock()
	if !strings.Contains(q, "source_branch=feature%2Fpay") {
		t.Fatalf("status query = %q", q)
	}

	// review from the base branch: review branch, summary and full patch
	git(t, dir, "checkout", "-q", "main")
	out, code = runEnv(t, dir, env, "", bin, "review", "5", "--diff")
	for _, want := range []string{"tide/review/pr-5", "approved by dani", "pay.txt", "diff --git", "+p"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("review --diff lacks %q (exit %d):\n%s", want, code, out)
		}
	}

	// --reset warns, asks, and only discards on a yes
	testutil.WriteFile(t, dir, "mine.txt", "mine\n")
	git(t, dir, "add", "mine.txt")
	if o, c := git(t, dir, "commit", "-q", "-m", "chore: my review notes"); c != 0 {
		t.Fatalf("commit: %s", o)
	}
	mine, _ := git(t, dir, "rev-parse", "HEAD")

	out, code = runEnv(t, dir, env, "n\n", bin, "review", "5", "--reset")
	if code == 0 || !strings.Contains(out, "chore: my review notes") || !strings.Contains(out, "discard 1 local commit") {
		t.Fatalf("declined reset: %d\n%s", code, out)
	}
	if now, _ := git(t, dir, "rev-parse", "HEAD"); now != mine {
		t.Fatal("commit discarded although the user said no")
	}
	if out, code = runEnv(t, dir, env, "", bin, "review", "5", "--reset", "--yes"); code != 0 {
		t.Fatalf("--reset --yes: %d\n%s", code, out)
	}
	if now, _ := git(t, dir, "rev-parse", "HEAD"); now == mine {
		t.Fatal("--reset --yes did not reset")
	}
}
