package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cellestias/git-tide/internal/testutil"
)

type dryDoc struct {
	OK     bool   `json:"ok"`
	Action string `json:"action"`
	Branch string `json:"branch"`
	Head   string `json:"head"`
	DryRun *struct {
		Would   []string `json:"would"`
		Stopped string   `json:"stopped"`
	} `json:"dry_run"`
}

func dryRepo(t *testing.T) (dir, bare string) {
	t.Helper()
	dir, bare = testutil.NewRepoWithRemote(t)
	if out, code := gtide(t, dir, "init", "--no-hooks"); code != 0 {
		t.Fatalf("init: %d\n%s", code, out)
	}
	testutil.Run(t, dir, "config", "user.email", "t@example.invalid")
	return dir, bare
}

func TestDryRunChangesNothingLocally(t *testing.T) {
	dir, _ := dryRepo(t)
	git(t, dir, "checkout", "-q", "-b", "feature/x")
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	head := testutil.Run(t, dir, "rev-parse", "HEAD")

	out, errOut, code := both(t, dir, nil, "save", "-m", "feat: a", "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{"nothing was changed", "git add", "git commit"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if got := testutil.Run(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved from %s to %s", head, got)
	}
	if status := testutil.Run(t, dir, "status", "--porcelain"); !strings.Contains(status, "?? a.txt") {
		t.Errorf("the file must stay untracked, status = %q", status)
	}
}

func TestDryRunJSONSaysWhatWouldHappen(t *testing.T) {
	dir, _ := dryRepo(t)
	git(t, dir, "checkout", "-q", "-b", "feature/x")
	testutil.WriteFile(t, dir, "a.txt", "a\n")

	out, _, code := both(t, dir, nil, "save", "-m", "feat: a", "--dry-run", "--json")
	var d dryDoc
	if err := json.Unmarshal([]byte(out), &d); code != 0 || err != nil {
		t.Fatalf("exit %d, not one JSON document: %v\n%s", code, err, out)
	}
	if !d.OK || d.DryRun == nil || len(d.DryRun.Would) < 2 || !strings.HasPrefix(d.DryRun.Would[len(d.DryRun.Would)-1], "git commit") {
		t.Errorf("doc = %+v\n%s", d, out)
	}
	// a real run has no dry_run key
	out, _, _ = both(t, dir, nil, "status", "--json")
	if strings.Contains(out, "dry_run") {
		t.Errorf("a real run must not carry dry_run:\n%s", out)
	}
}

func TestDryRunStillRefusesWhatWouldBeRefused(t *testing.T) {
	dir, _ := dryRepo(t)
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	// main is protected: the same refusal as a real run, and no commit
	out, errOut, code := both(t, dir, nil, "save", "-m", "feat: a", "--dry-run")
	if code != 4 {
		t.Fatalf("exit %d, want 4 (policy)\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "refused the same way") {
		t.Errorf("stderr should say it is the same refusal:\n%s", errOut)
	}
}

func TestDryRunPushLeavesTheRemoteAlone(t *testing.T) {
	dir, bare := dryRepo(t)
	git(t, dir, "checkout", "-q", "-b", "feature/x")
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	git(t, dir, "add", "a.txt")
	git(t, dir, "commit", "-q", "-m", "feat: a")

	out, _, code := both(t, dir, nil, "push", "--dry-run")
	if code != 0 || !strings.Contains(out, "git push") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if branches := testutil.Run(t, bare, "branch", "--list", "feature/x"); strings.TrimSpace(branches) != "" {
		t.Errorf("the branch reached the remote: %q", branches)
	}
}

func TestDryRunKeepsBranchesAndFiles(t *testing.T) {
	dir, _ := dryRepo(t)
	git(t, dir, "branch", "feature/old")
	if _, _, code := both(t, dir, nil, "branch", "delete", "feature/old", "--force", "--yes", "--dry-run"); code != 0 {
		t.Fatalf("delete --dry-run: exit %d", code)
	}
	if got := testutil.Run(t, dir, "branch", "--list", "feature/old"); !strings.Contains(got, "feature/old") {
		t.Errorf("the branch was deleted")
	}

	fresh := t.TempDir()
	testutil.Run(t, fresh, "init", "-q", "-b", "main")
	out, _, code := both(t, fresh, nil, "init", "--dry-run")
	if code != 0 || !strings.Contains(out, "create") {
		t.Fatalf("init --dry-run: exit %d\n%s", code, out)
	}
	entries, _ := os.ReadDir(fresh)
	for _, e := range entries {
		if e.Name() != ".git" {
			t.Errorf("init --dry-run wrote %s", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(fresh, ".git", "hooks", "pre-commit")); err == nil {
		t.Errorf("init --dry-run installed a hook")
	}
}

func TestDryRunInAFolderWithoutARepository(t *testing.T) {
	empty := t.TempDir()
	out, _, code := both(t, empty, nil, "init", "--dry-run")
	if code != 0 || !strings.Contains(out, "git init") || !strings.Contains(out, "Stopped here") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if entries, _ := os.ReadDir(empty); len(entries) != 0 {
		t.Errorf("a repository was created: %v", entries)
	}
}

func TestDryRunOpensNoPullRequest(t *testing.T) {
	var writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":42,"html_url":"https://github.com/acme/tool/pull/42"}`))
	}))
	defer srv.Close()
	env := []string{"TIDE_GITHUB_API_URL=" + srv.URL, "GITHUB_TOKEN=test-token", "GH_TOKEN="}
	dir, bare := dryRepo(t)
	testutil.Run(t, dir, "remote", "set-url", "origin", "https://github.com/acme/tool.git")
	testutil.Run(t, dir, "config", "url."+bare+".insteadOf", "https://github.com/acme/tool.git")
	git(t, dir, "checkout", "-q", "-b", "feature/pay")
	testutil.WriteFile(t, dir, "pay.txt", "p\n")
	git(t, dir, "add", "pay.txt")
	git(t, dir, "commit", "-q", "-m", "feat: add pay")

	out, _, code := both(t, dir, env, "pr", "--yes", "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "git push") || !strings.Contains(out, "POST ") {
		t.Errorf("the plan should hold the push and the request:\n%s", out)
	}
	if writes.Load() != 0 {
		t.Errorf("the API got %d writes", writes.Load())
	}
	if branches := testutil.Run(t, bare, "branch", "--list", "feature/pay"); strings.TrimSpace(branches) != "" {
		t.Errorf("the branch was pushed")
	}
}

func TestDryRunOnAReadingCommandStillPrintsIt(t *testing.T) {
	dir, _ := dryRepo(t)
	out, _, code := both(t, dir, nil, "status", "--dry-run")
	if code != 0 || !strings.Contains(out, "main") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, args := range [][]string{{"branch"}, {"history"}, {"doctor"}} {
		if _, _, code := both(t, dir, nil, append(args, "--dry-run")...); code != 0 {
			t.Errorf("%v --dry-run: exit %d", args, code)
		}
	}
}
