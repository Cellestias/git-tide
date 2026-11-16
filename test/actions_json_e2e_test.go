package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/testutil"
)

// both runs gtide and returns stdout, stderr and the exit code apart.
func both(t *testing.T, dir string, extra []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), filepath.Join(binDir, "gtide"), args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), extra...),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.invalid",
	)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return o.String(), e.String(), code
}

type actionResult struct {
	OK      bool
	Action  string
	Branch  string
	Head    string
	Details map[string]any
}

// act runs `gtide <args> --json` and checks the envelope: exit 0, stdout is one JSON document that says
// which action it was, and the human log is on stderr, not mixed into the document.
func act(t *testing.T, dir string, extra []string, wantAction string, args ...string) actionResult {
	t.Helper()
	out, errOut, code := both(t, dir, extra, append(args, "--json")...)
	if code != 0 {
		t.Fatalf("gtide %s --json: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, out, errOut)
	}
	var r actionResult
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("gtide %s --json: stdout is not one JSON document: %v\n%s", strings.Join(args, " "), err, out)
	}
	if !r.OK || r.Action != wantAction {
		t.Fatalf("gtide %s --json: ok=%v action=%q, want %q\n%s", strings.Join(args, " "), r.OK, r.Action, wantAction, out)
	}
	if r.Details == nil {
		t.Errorf("details is always an object:\n%s", out)
	}
	if strings.TrimSpace(errOut) == "" {
		t.Errorf("gtide %s --json: the human log must go to stderr, which is empty", strings.Join(args, " "))
	}
	return r
}

func TestActionsPrintJSONEndToEnd(t *testing.T) {
	dir, bare := testutil.NewRepoWithRemote(t)
	if out, code := gtide(t, dir, "init", "--no-hooks"); code != 0 {
		t.Fatalf("init: %d\n%s", code, out)
	}
	testutil.Run(t, dir, "config", "user.email", "t@example.invalid")
	files := func(r actionResult) []string {
		var names []string
		list, _ := r.Details["files"].([]any)
		for _, f := range list {
			names = append(names, f.(map[string]any)["path"].(string))
		}
		return names
	}
	head := func() string { return testutil.Run(t, dir, "rev-parse", "HEAD") }

	// create, save, commit, amend, undo
	r := act(t, dir, nil, "create", "create", "feature/x")
	if r.Branch != "feature/x" || r.Details["branch"] != "feature/x" || r.Details["from"] != "main" || r.Details["switched"] != true || r.Details["pushed"] != false {
		t.Errorf("create = %+v", r)
	}

	testutil.WriteFile(t, dir, "a.txt", "a\n")
	r = act(t, dir, nil, "save", "save", "-m", "feat: a", "-f", "a.txt")
	if r.Head != head() || r.Details["commit"] != r.Head || r.Details["subject"] != "feat: a" || strings.Join(files(r), ",") != "a.txt" {
		t.Errorf("save = %+v", r)
	}

	testutil.WriteFile(t, dir, "b.txt", "b\n")
	git(t, dir, "add", "b.txt")
	r = act(t, dir, nil, "commit", "commit", "-m", "feat: b")
	if r.Details["subject"] != "feat: b" || strings.Join(files(r), ",") != "b.txt" {
		t.Errorf("commit = %+v", r)
	}
	previous := r.Head

	r = act(t, dir, nil, "amend", "amend", "-m", "feat: b, reworded")
	if r.Details["previous"] != previous || r.Head == previous || r.Details["subject"] != "feat: b, reworded" {
		t.Errorf("amend = %+v", r)
	}

	amended := r.Head
	r = act(t, dir, nil, "undo", "undo")
	undone := r.Details["undone"].(map[string]any)
	staged := r.Details["staged"].([]any)
	if undone["commit"] != amended || undone["subject"] != "feat: b, reworded" || len(staged) != 1 || staged[0].(map[string]any)["path"] != "b.txt" || r.Head == amended {
		t.Errorf("undo = %+v", r)
	}
	act(t, dir, nil, "save", "save", "-m", "feat: b again", "-f", "b.txt")

	// push, and a commit from someone else, pull
	r = act(t, dir, nil, "push", "push")
	if r.Details["upstream"] != "origin/feature/x" || r.Details["remote"] != "origin" || r.Details["forced"] != false {
		t.Errorf("push = %+v", r)
	}
	other := t.TempDir()
	testutil.Run(t, other, "clone", "-q", "--branch", "feature/x", bare, ".")
	testutil.Run(t, other, "config", "user.email", "o@example.invalid")
	testutil.Run(t, other, "config", "user.name", "Other")
	testutil.WriteFile(t, other, "c.txt", "c\n")
	testutil.Commit(t, other, "feat: c")
	testutil.Run(t, other, "push", "-q")
	before := head()
	r = act(t, dir, nil, "pull", "pull")
	if r.Details["before"] != before || r.Details["after"] != r.Head || r.Details["changed"] != true || r.Details["upstream"] != "origin/feature/x" {
		t.Errorf("pull = %+v", r)
	}
	if r = act(t, dir, nil, "update", "update"); r.Details["base"] != "main" || r.Details["changed"] != false {
		t.Errorf("update = %+v", r)
	}

	// stash
	testutil.WriteFile(t, dir, "a.txt", "changed\n")
	r = act(t, dir, nil, "stash", "stash", "-m", "wip")
	if r.Details["ref"] != "stash@{0}" || !strings.Contains(r.Details["message"].(string), "wip") || r.Details["stashes"] != float64(1) {
		t.Errorf("stash = %+v", r)
	}
	if r = act(t, dir, nil, "stash pop", "stash", "pop"); r.Details["ref"] != "stash@{0}" || r.Details["stashes"] != float64(0) {
		t.Errorf("stash pop = %+v", r)
	}
	git(t, dir, "stash", "push", "-q", "-m", "again")
	if r = act(t, dir, nil, "stash drop", "stash", "drop", "--yes"); r.Details["stashes"] != float64(0) {
		t.Errorf("stash drop = %+v", r)
	}

	// change, tags, branches
	r = act(t, dir, nil, "change", "change", "main")
	if r.Branch != "main" || r.Details["branch"] != "main" || r.Details["previous"] != "feature/x" || r.Details["changed"] != true {
		t.Errorf("change = %+v", r)
	}
	r = act(t, dir, nil, "tag create", "tag", "create", "v1.0.0", "-m", "First")
	if r.Details["name"] != "v1.0.0" || r.Details["commit"] != r.Head || r.Details["pushed"] != false || r.Details["message"] != "First" {
		t.Errorf("tag create = %+v", r)
	}
	if r = act(t, dir, nil, "tag push", "tag", "push", "v1.0.0", "--yes"); r.Details["name"] != "v1.0.0" || r.Details["commit"] != r.Head || r.Details["remote"] != "origin" {
		t.Errorf("tag push = %+v", r)
	}
	if r = act(t, dir, nil, "tag delete", "tag", "delete", "v1.0.0", "--remote", "--yes"); r.Details["name"] != "v1.0.0" || r.Details["remote"] != true {
		t.Errorf("tag delete = %+v", r)
	}
	if r = act(t, dir, nil, "branch rename", "branch", "rename", "feature/x", "feature/y"); r.Details["old"] != "feature/x" || r.Details["new"] != "feature/y" {
		t.Errorf("branch rename = %+v", r)
	}
	r = act(t, dir, nil, "branch delete", "branch", "delete", "feature/y", "--force", "--yes")
	deleted, _ := r.Details["deleted"].([]any)
	if len(deleted) != 1 || deleted[0] != "feature/y" || r.Details["forced"] != true || r.Details["remote"] != false {
		t.Errorf("branch delete = %+v", r)
	}

	// remote: a URL with a password is refused (and nothing is printed), a good one is shown
	if out, errOut, code := both(t, dir, nil, "remote", "set-url", "https://user:s3cret@github.com/acme/tool.git", "--json"); code != 2 || strings.TrimSpace(out) != "" || strings.Contains(out+errOut, "s3cret") && !strings.Contains(errOut, "holds a password") {
		t.Errorf("a URL with a password: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	r = act(t, dir, nil, "remote set-url", "remote", "set-url", "git@github.com:acme/tool.git")
	if r.Details["name"] != "origin" || r.Details["provider"] != "github" || r.Details["url"] != "git@github.com:acme/tool.git" {
		t.Errorf("remote set-url = %+v", r)
	}
	testutil.Run(t, dir, "remote", "set-url", "origin", bare)
	if r = act(t, dir, nil, "remote add", "remote", "add", "--name", "fork", "git@gitlab.com:team/app.git"); r.Details["name"] != "fork" || r.Details["provider"] != "gitlab" {
		t.Errorf("remote add = %+v", r)
	}

	// a refusal prints nothing on stdout: the exit code and stderr say what happened
	out, errOut, code := both(t, dir, nil, "push", "--json")
	if code != 4 || strings.TrimSpace(out) != "" || !strings.Contains(errOut, "✗") {
		t.Errorf("a refused push: exit %d, stdout %q, stderr %q", code, out, errOut)
	}

	// abort of a merge that stopped on a conflict
	git(t, dir, "checkout", "-q", "-b", "one", "main")
	testutil.WriteFile(t, dir, "x.txt", "one\n")
	git(t, dir, "add", "x.txt")
	git(t, dir, "commit", "-q", "-m", "feat: one")
	git(t, dir, "checkout", "-q", "-b", "two", "main")
	testutil.WriteFile(t, dir, "x.txt", "two\n")
	git(t, dir, "add", "x.txt")
	git(t, dir, "commit", "-q", "-m", "feat: two")
	_, _ = git(t, dir, "merge", "one")
	if r = act(t, dir, nil, "abort", "abort", "--yes"); r.Details["operation"] != "merge" || r.Branch != "two" {
		t.Errorf("abort = %+v", r)
	}
	_, _ = git(t, dir, "merge", "one")
	testutil.WriteFile(t, dir, "x.txt", "resolved\n")
	git(t, dir, "add", "x.txt")
	if r = act(t, dir, nil, "continue", "continue"); r.Details["operation"] != "merge" || r.Details["in_progress"] != false {
		t.Errorf("continue = %+v", r)
	}
}

func TestPRCreatePrintsTheRequestAsJSON(t *testing.T) {
	var gotHead string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotHead, _ = body["head"].(string)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":42,"title":"feat: add pay","html_url":"https://github.com/acme/tool/pull/42","head":{"ref":"feature/pay"},"base":{"ref":"main"}}`))
	}))
	defer srv.Close()
	env := []string{"TIDE_GITHUB_API_URL=" + srv.URL, "GITHUB_TOKEN=test-token", "GH_TOKEN="}
	dir, bare := testutil.NewRepoWithRemote(t)
	testutil.Run(t, dir, "remote", "set-url", "origin", "https://github.com/acme/tool.git")
	testutil.Run(t, dir, "config", "url."+bare+".insteadOf", "https://github.com/acme/tool.git")
	if out, code := gtide(t, dir, "init", "--no-hooks"); code != 0 {
		t.Fatalf("init: %d\n%s", code, out)
	}
	git(t, dir, "checkout", "-q", "-b", "feature/pay")
	testutil.WriteFile(t, dir, "pay.txt", "p\n")
	git(t, dir, "add", "pay.txt")
	git(t, dir, "commit", "-q", "-m", "feat: add pay")

	r := act(t, dir, env, "pr", "pr", "--yes", "--draft")
	if r.Branch != "feature/pay" || r.Details["number"] != float64(42) || r.Details["url"] != "https://github.com/acme/tool/pull/42" || r.Details["noun"] != "pull request" ||
		r.Details["provider"] != "github" || r.Details["head"] != "feature/pay" || r.Details["base"] != "main" || r.Details["draft"] != true || r.Details["title"] != "feat: add pay" {
		t.Errorf("pr = %+v", r)
	}
	if gotHead != "feature/pay" {
		t.Errorf("the API got head %q", gotHead)
	}
}

func TestInitAndCloneImplyJSON(t *testing.T) {
	_, bare := testutil.NewRepoWithRemote(t)

	// init in a folder that is not a repository yet: with --yes it makes one
	fresh := t.TempDir()
	r := act(t, fresh, nil, "init", "init", "--yes", "--remote", "https://github.com/acme/tool.git")
	cfg := r.Details["config"].(map[string]any)
	secret := r.Details["secret_file"].(map[string]any)
	remote := r.Details["remote"].(map[string]any)
	hooks, _ := r.Details["hook_changes"].([]any)
	if cfg["created"] != true || !strings.HasSuffix(cfg["path"].(string), ".tide.yml") || secret["created"] != true || r.Details["provider"] != "auto" ||
		remote["name"] != "origin" || remote["added"] != true || len(hooks) != 3 || r.Details["hooks_skipped"] != false || r.Details["base"] != "main" {
		t.Errorf("init = %+v", r.Details)
	}
	if r.Branch != "main" {
		t.Errorf("branch = %q", r.Branch)
	}
	// running it again keeps what exists, and says so in the data
	again := act(t, fresh, nil, "init", "init")
	if again.Details["config"].(map[string]any)["created"] != false || again.Details["remote"] != nil && again.Details["remote"].(map[string]any)["added"] == true {
		t.Errorf("a second init must keep everything: %+v", again.Details)
	}

	// clone
	parent := t.TempDir()
	c := act(t, parent, nil, "clone", "clone", bare, "copy")
	if !strings.HasSuffix(c.Details["path"].(string), "copy") || c.Details["branch"] != "main" || len(c.Details["head"].(string)) != 40 {
		t.Errorf("clone = %+v", c.Details)
	}
	if setup, _ := c.Details["setup"].(map[string]any); setup == nil || setup["config"].(map[string]any)["created"] != true {
		t.Errorf("setup = %+v", c.Details["setup"])
	}
	if _, err := os.Stat(filepath.Join(parent, "copy", ".tide.yml")); err != nil {
		t.Errorf("the clone was made: %v", err)
	}
	// the folder exists and is not empty: refused, and stdout stays empty
	if out, _, code := both(t, parent, nil, "clone", bare, "copy", "--json"); code == 0 || strings.TrimSpace(out) != "" {
		t.Errorf("a second clone into the same folder: exit %d, stdout %q", code, out)
	}
}
