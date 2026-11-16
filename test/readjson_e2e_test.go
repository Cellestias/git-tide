package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/testutil"
)

// fakeGitHubPRs serves one open pull request (#42) and its details.
func fakeGitHubPRs(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		const pr = `{"number":42,"title":"Add auth\u001b[31m","body":"Implements login.\nSecond line","state":"open","draft":false,
		  "html_url":"https://github.com/acme/tool/pull/42","created_at":"2026-11-01T10:00:00Z","updated_at":"2026-11-02T11:30:00Z",
		  "user":{"login":"ana"},"assignees":[{"login":"bia"}],"requested_reviewers":[{"login":"caio"}],
		  "head":{"ref":"feature/login","repo":{"full_name":"acme/tool"}},"base":{"ref":"main"}}`
		switch {
		case r.URL.Path == "/repos/acme/tool/pulls" && r.Method == http.MethodGet:
			_, _ = fmt.Fprint(w, "["+pr+"]")
		case r.URL.Path == "/repos/acme/tool/pulls/42":
			_, _ = fmt.Fprint(w, pr)
		case strings.HasSuffix(r.URL.Path, "/pulls/42/reviews"):
			_, _ = fmt.Fprint(w, `[{"state":"APPROVED","user":{"login":"dani"}}]`)
		case strings.HasSuffix(r.URL.Path, "/pulls/42/files"):
			_, _ = fmt.Fprint(w, `[{"filename":"a.go","status":"added","additions":10,"deletions":0}]`)
		case strings.HasSuffix(r.URL.Path, "/pulls/42/commits"):
			_, _ = fmt.Fprint(w, `[{"sha":"abcdef1234567890","commit":{"message":"feat: login\n\nbody"}}]`)
		default:
			w.WriteHeader(404)
			_, _ = fmt.Fprint(w, `{"message":"Not Found"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReadCommandsPrintJSONEndToEnd(t *testing.T) {
	srv := fakeGitHubPRs(t)
	env := []string{"TIDE_GITHUB_API_URL=" + srv.URL, "GITHUB_TOKEN=test-token", "GH_TOKEN="}
	dir, bare := testutil.NewRepoWithRemote(t)
	testutil.Run(t, dir, "remote", "set-url", "origin", "https://github.com/acme/tool.git")
	testutil.Run(t, dir, "config", "url."+bare+".insteadOf", "https://github.com/acme/tool.git")
	if out, code := gtide(t, dir, "init", "--no-hooks"); code != 0 {
		t.Fatalf("init: %d\n%s", code, out)
	}
	git(t, dir, "checkout", "-q", "-b", "feature/login")

	// run is gtide with the fake API in the environment; it returns stdout alone.
	run := func(args ...string) (string, int) {
		t.Helper()
		return stdoutWith(t, dir, env, args...)
	}
	valid := func(out string, kind byte, args ...string) {
		t.Helper()
		if !json.Valid([]byte(out)) || !strings.HasPrefix(strings.TrimSpace(out), string(kind)) {
			t.Errorf("gtide %s: not a JSON %c:\n%s", strings.Join(args, " "), kind, out)
		}
	}

	out, code := run("pr", "list", "--json")
	if code != 0 {
		t.Fatalf("pr list --json: %d\n%s", code, out)
	}
	valid(out, '[', "pr", "list", "--json")
	var list []struct {
		Number int
		Title  string
		Head   string
		Author string
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list) != 1 || list[0].Number != 42 || list[0].Head != "feature/login" || strings.ContainsAny(list[0].Title, "\x1b") {
		t.Errorf("list = %+v (%v)\n%s", list, err, out)
	}

	out, code = run("pr", "status", "--json")
	var status struct {
		Branch  string
		Request *struct{ Number int }
		Review  *struct{ State string }
	}
	if err := json.Unmarshal([]byte(out), &status); code != 0 || err != nil || status.Branch != "feature/login" || status.Request == nil || status.Request.Number != 42 || status.Review == nil || status.Review.State != "approved" {
		t.Errorf("status: exit %d (%v)\n%s", code, err, out)
	}

	out, code = run("pr", "view", "42", "--json")
	var view struct {
		Number  int
		Body    string
		Review  *struct{ State string }
		Files   []struct{ Path string }
		Commits []struct{ SHA, Subject string }
	}
	if err := json.Unmarshal([]byte(out), &view); code != 0 || err != nil || view.Number != 42 || !strings.Contains(view.Body, "Second line") || view.Review == nil ||
		len(view.Files) != 1 || view.Files[0].Path != "a.go" || len(view.Commits) != 1 || view.Commits[0].Subject != "feat: login" {
		t.Errorf("view: exit %d (%v)\n%s", code, err, out)
	}

	// a failure is still the exit code and stderr; stdout stays empty, so nothing half-written can be parsed
	if out, code := run("pr", "view", "999", "--json"); code != 5 || strings.TrimSpace(out) != "" {
		t.Errorf("not found: exit %d, stdout %q", code, out)
	}

	for _, c := range []struct {
		args []string
		kind byte
	}{
		{[]string{"config", "show", "--json"}, '{'},
		{[]string{"config", "--json"}, '{'},
		{[]string{"config", "validate", "--json"}, '{'},
		{[]string{"remote", "show", "--json"}, '{'},
		{[]string{"remote", "--json"}, '{'},
	} {
		out, code := stdout(t, dir, c.args...)
		if code != 0 {
			t.Errorf("gtide %s: exit %d\n%s", strings.Join(c.args, " "), code, out)
		}
		valid(out, c.kind, c.args...)
	}

	var cfg struct {
		Found  bool
		Config struct {
			Remote   string
			Security struct{ Level string }
			Branches struct {
				DefaultBase string `json:"default_base"`
			}
		}
	}
	out, _ = stdout(t, dir, "config", "show", "--json")
	if err := json.Unmarshal([]byte(out), &cfg); err != nil || !cfg.Found || cfg.Config.Remote != "origin" || cfg.Config.Security.Level != "off" || cfg.Config.Branches.DefaultBase != "main" {
		t.Errorf("config = %+v (%v)", cfg, err)
	}

	var remotes struct {
		Configured string
		Remotes    []struct {
			Name, URL, Provider, Repo string
			Used                      bool
		}
	}
	out, _ = stdout(t, dir, "remote", "show", "--json")
	if err := json.Unmarshal([]byte(out), &remotes); err != nil || remotes.Configured != "origin" || len(remotes.Remotes) != 1 || remotes.Remotes[0].Repo != "acme/tool" || remotes.Remotes[0].Provider != "github" || !remotes.Remotes[0].Used {
		t.Errorf("remotes = %+v (%v)\n%s", remotes, err, out)
	}

	// validate: an invalid file still prints the document, and still exits 3
	testutil.WriteFile(t, dir, ".tide.yml", "version: 1\nupdate:\n  strategy: squash\n")
	out, code = stdout(t, dir, "config", "validate", "--json")
	var v struct {
		Valid  bool
		Errors []string
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || code != 3 || v.Valid || len(v.Errors) != 1 || !strings.HasPrefix(v.Errors[0], "update.strategy") {
		t.Errorf("validate: exit %d (%v) %+v\n%s", code, err, v, out)
	}
}

// stdoutWith is stdout (stdout alone, and the exit code) with extra environment variables.
func stdoutWith(t *testing.T, dir string, extra []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), filepath.Join(binDir, "gtide"), args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), extra...),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.invalid",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), code
}

func TestReviewPrintsTheBranchAsJSON(t *testing.T) {
	srv := fakeGitHubPRs(t)
	env := []string{"TIDE_GITHUB_API_URL=" + srv.URL, "GITHUB_TOKEN=test-token", "GH_TOKEN="}
	dir, bare := testutil.NewRepoWithRemote(t)
	testutil.Run(t, dir, "remote", "set-url", "origin", "https://github.com/acme/tool.git")
	testutil.Run(t, dir, "config", "url."+bare+".insteadOf", "https://github.com/acme/tool.git")
	if out, code := gtide(t, dir, "init", "--no-hooks"); code != 0 {
		t.Fatalf("init: %d\n%s", code, out)
	}
	git(t, dir, "checkout", "-q", "-b", "feature/login")
	testutil.WriteFile(t, dir, "a.go", "package a\n")
	git(t, dir, "add", "a.go")
	git(t, dir, "commit", "-q", "-m", "feat: login")
	git(t, dir, "push", "-q", "origin", "HEAD:refs/pull/42/head") // what GitHub keeps for a pull request
	git(t, dir, "checkout", "-q", "main")

	r := act(t, dir, env, "review", "review", "42")
	if r.Branch != "tide/review/pr-42" || r.Details["branch"] != "tide/review/pr-42" || r.Details["request"] != "42" || r.Details["reset"] != false {
		t.Errorf("review = %+v", r)
	}
	if r.Head != testutil.Run(t, bare, "rev-parse", "refs/pull/42/head") {
		t.Errorf("the review branch is on the request's head: %s", r.Head)
	}
}
