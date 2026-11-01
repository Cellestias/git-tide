package create

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/testutil"
)

type env struct {
	repo  *repository.Repository
	dir   string
	bare  string
	other string // second clone used to advance origin
}

func setup(t *testing.T) env {
	t.Helper()
	dir, bare := testutil.NewRepoWithRemote(t)
	other := t.TempDir()
	testutil.Run(t, other, "clone", "-q", bare, ".")
	return env{repository.New(executor.Exec{}, dir), dir, bare, other}
}

// advance adds a commit on branch (created from main if needed) in "other" and pushes it.
func (e env) advance(t *testing.T, branch, file string) string {
	t.Helper()
	if branch != "main" {
		testutil.Run(t, e.other, "checkout", "-q", "-B", branch, "origin/main")
	} else {
		testutil.Run(t, e.other, "checkout", "-q", "main")
	}
	testutil.WriteFile(t, e.other, file, file+"\n")
	testutil.Commit(t, e.other, "feat: "+file)
	testutil.Run(t, e.other, "push", "-q", "origin", branch)
	return testutil.Run(t, e.other, "rev-parse", "HEAD")
}

func run(e env, cfg domain.Config, name string, opts Options) (string, error) {
	var out, errOut bytes.Buffer
	err := Run(context.Background(), e.repo, cfg, name, opts, output.Printer{Out: &out, Err: &errOut})
	return out.String(), err
}

func head(t *testing.T, e env, rev string) string {
	t.Helper()
	return testutil.Run(t, e.dir, "rev-parse", rev)
}

func current(t *testing.T, e env) string {
	return testutil.Run(t, e.dir, "rev-parse", "--abbrev-ref", "HEAD")
}

func hasUpstream(e env, t *testing.T, name string) bool {
	t.Helper()
	out, err := testutil.TryRun(e.dir, "config", "--get", "branch."+name+".remote")
	return err == nil && out != ""
}

func TestDefaultStartsFromFreshRemoteBase(t *testing.T) {
	e := setup(t)
	tip := e.advance(t, "main", "up.txt") // local main is now stale

	out, err := run(e, domain.Default(), "feature/two-fa", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if current(t, e) != "feature/two-fa" || head(t, e, "HEAD") != tip {
		t.Errorf("branch=%s head=%s want tip %s", current(t, e), head(t, e, "HEAD"), tip)
	}
	if hasUpstream(e, t, "feature/two-fa") {
		t.Error("new branch must not track the base branch")
	}
	if !strings.Contains(out, "origin/main") {
		t.Errorf("output = %q", out)
	}
	if head(t, e, "main") == tip {
		t.Error("local main must not be moved")
	}
}

func TestBaseComesFromMapping(t *testing.T) {
	e := setup(t)
	stageTip := e.advance(t, "stage", "stage.txt")
	cfg := domain.Default()
	cfg.Branches.Base = map[string]string{"feature/*": "stage"}

	if _, err := run(e, cfg, "feature/x", Options{}); err != nil {
		t.Fatal(err)
	}
	if head(t, e, "HEAD") != stageTip {
		t.Error("mapping to stage ignored")
	}
}

func TestFrom(t *testing.T) {
	e := setup(t)
	oldTip := e.advance(t, "feat/old-two-fa", "old.txt")
	first := head(t, e, "main")
	testutil.Run(t, e.dir, "tag", "v0.1", "HEAD")

	// remote branch
	if _, err := run(e, domain.Default(), "feat/two-fa", Options{From: "feat/old-two-fa"}); err != nil {
		t.Fatal(err)
	}
	if head(t, e, "HEAD") != oldTip {
		t.Error("did not start from the remote --from branch")
	}

	// tag (not a remote branch)
	testutil.Run(t, e.dir, "checkout", "-q", "main")
	if _, err := run(e, domain.Default(), "fix/from-tag", Options{From: "v0.1"}); err != nil {
		t.Fatal(err)
	}
	if head(t, e, "HEAD") != first {
		t.Error("did not start from the tag")
	}

	// local-only branch
	testutil.Run(t, e.dir, "checkout", "-q", "main")
	testutil.Run(t, e.dir, "branch", "wip", "main")
	if _, err := run(e, domain.Default(), "fix/from-wip", Options{From: "wip"}); err != nil {
		t.Fatal(err)
	}

	// nothing by that name anywhere
	testutil.Run(t, e.dir, "checkout", "-q", "main")
	if _, err := run(e, domain.Default(), "fix/none", Options{From: "ghost"}); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("missing --from: %v", err)
	}
}

func TestLocalMakesNoNetworkCall(t *testing.T) {
	e := setup(t)
	e.advance(t, "main", "up.txt")
	staleMain := head(t, e, "main")
	testutil.Run(t, e.dir, "remote", "set-url", "origin", "/nonexistent/remote")

	if _, err := run(e, domain.Default(), "feature/offline", Options{Local: true}); err != nil {
		t.Fatal(err)
	}
	if head(t, e, "HEAD") != staleMain {
		t.Error("--local must start from the local branch as it is")
	}

	// without a local base, fall back to the (stale) remote-tracking ref
	testutil.Run(t, e.dir, "checkout", "-q", "-b", "elsewhere")
	testutil.Run(t, e.dir, "branch", "-D", "main")
	out, err := run(e, domain.Default(), "feature/offline2", Options{Local: true})
	if err != nil || !strings.Contains(out, "not refreshed") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestTrack(t *testing.T) {
	e := setup(t)
	tip := e.advance(t, "feature/shared", "shared.txt")

	if _, err := run(e, domain.Default(), "feature/shared", Options{Track: true}); err != nil {
		t.Fatal(err)
	}
	if head(t, e, "HEAD") != tip || !hasUpstream(e, t, "feature/shared") {
		t.Error("track did not check out the remote branch with upstream")
	}
	if got := testutil.Run(t, e.dir, "rev-parse", "--abbrev-ref", "feature/shared@{upstream}"); got != "origin/feature/shared" {
		t.Errorf("upstream = %s", got)
	}

	testutil.Run(t, e.dir, "checkout", "-q", "main")
	if _, err := run(e, domain.Default(), "feature/absent", Options{Track: true}); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("absent remote branch: %v", err)
	}
}

func TestPush(t *testing.T) {
	e := setup(t)
	if _, err := run(e, domain.Default(), "feature/pushed", Options{Push: true}); err != nil {
		t.Fatal(err)
	}
	if testutil.Run(t, e.bare, "rev-parse", "refs/heads/feature/pushed") != head(t, e, "HEAD") {
		t.Error("branch not on remote")
	}
	if got := testutil.Run(t, e.dir, "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/feature/pushed" {
		t.Errorf("upstream = %s", got)
	}
}

func TestNoSwitchAllowsDirtyTree(t *testing.T) {
	e := setup(t)
	testutil.WriteFile(t, e.dir, "README.md", "dirty\n")
	if _, err := run(e, domain.Default(), "feature/bg", Options{NoSwitch: true}); err != nil {
		t.Fatal(err)
	}
	if current(t, e) != "main" {
		t.Error("must stay on the current branch")
	}
	if ok, _ := e.repo.LocalBranchExists(context.Background(), "feature/bg"); !ok {
		t.Error("branch not created")
	}
}

func TestRefusals(t *testing.T) {
	e := setup(t)
	cfg := domain.Default()
	tests := []struct {
		name string
		opts Options
		code int
	}{
		{"-bad", Options{}, apperr.CodeUsage},
		{"a..b", Options{}, apperr.CodeUsage},
		{"main", Options{}, apperr.CodePolicy},
		{"feature/x", Options{Track: true, From: "main"}, apperr.CodeUsage},
		{"feature/x", Options{Track: true, Local: true}, apperr.CodeUsage},
		{"feature/x", Options{Track: true, Push: true}, apperr.CodeUsage},
		{"feature/x", Options{From: "-x"}, apperr.CodeUsage},
	}
	for _, tt := range tests {
		if _, err := run(e, cfg, tt.name, tt.opts); apperr.CodeOf(err) != tt.code {
			t.Errorf("%q %+v: code %d (%v), want %d", tt.name, tt.opts, apperr.CodeOf(err), err, tt.code)
		}
	}

	testutil.Run(t, e.dir, "branch", "taken")
	if _, err := run(e, cfg, "taken", Options{}); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("existing branch: %v", err)
	}

	testutil.WriteFile(t, e.dir, "README.md", "dirty\n")
	before := head(t, e, "HEAD")
	if _, err := run(e, cfg, "feature/dirty", Options{}); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("dirty tree: %v", err)
	}
	if head(t, e, "HEAD") != before || current(t, e) != "main" {
		t.Error("repository changed despite refusal")
	}
	if ok, _ := e.repo.LocalBranchExists(context.Background(), "feature/dirty"); ok {
		t.Error("branch created despite refusal")
	}
}

func TestUnreachableRemoteIsExternalError(t *testing.T) {
	e := setup(t)
	testutil.Run(t, e.dir, "remote", "set-url", "origin", "/nonexistent/remote")
	if _, err := run(e, domain.Default(), "feature/x", Options{}); apperr.CodeOf(err) != apperr.CodeExternal || !strings.Contains(apperr.HintOf(err), "--local") {
		t.Errorf("err = %v hint=%q", err, apperr.HintOf(err))
	}
	if _, err := run(e, domain.Default(), "feature/x", Options{From: "main"}); apperr.CodeOf(err) != apperr.CodeExternal {
		t.Errorf("--from: %v", err)
	}
}
