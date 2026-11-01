package push

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/testutil"
)

func setup(t *testing.T) (*repository.Repository, string, string) {
	t.Helper()
	dir, bare := testutil.NewRepoWithRemote(t)
	return repository.New(executor.Exec{}, dir), dir, bare
}

func run(repo *repository.Repository, cfg domain.Config, opts Options) error {
	var out, errOut bytes.Buffer
	return Run(context.Background(), repo, cfg, opts, output.Printer{Out: &out, Err: &errOut})
}

func code(err error) int {
	var e *apperr.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return -1
}

func commitOnBranch(t *testing.T, dir, name string) {
	t.Helper()
	testutil.Run(t, dir, "checkout", "-q", "-b", name)
	testutil.WriteFile(t, dir, name+".txt", "x\n")
	testutil.Commit(t, dir, "feat: "+name)
}

func TestPushesAndSetsUpstream(t *testing.T) {
	repo, dir, bare := setup(t)
	commitOnBranch(t, dir, "feature/a")

	if err := run(repo, domain.Default(), Options{}); err != nil {
		t.Fatal(err)
	}
	if got, want := testutil.Run(t, bare, "rev-parse", "feature/a"), testutil.Run(t, dir, "rev-parse", "HEAD"); got != want {
		t.Errorf("remote tip = %s, want %s", got, want)
	}
	if up := testutil.Run(t, dir, "rev-parse", "--abbrev-ref", "feature/a@{upstream}"); up != "origin/feature/a" {
		t.Errorf("upstream = %q", up)
	}
}

func TestRefusesProtectedBranch(t *testing.T) {
	repo, _, bare := setup(t) // clone is on main
	before := testutil.Run(t, bare, "rev-parse", "main")

	err := run(repo, domain.Default(), Options{})
	if code(err) != apperr.CodePolicy {
		t.Fatalf("err = %v, want policy error", err)
	}
	if testutil.Run(t, bare, "rev-parse", "main") != before {
		t.Error("protected branch was modified")
	}
}

func TestRefusesDetachedHead(t *testing.T) {
	repo, dir, _ := setup(t)
	testutil.Run(t, dir, "checkout", "-q", "--detach")
	if err := run(repo, domain.Default(), Options{}); code(err) != apperr.CodeUnsafeRepo {
		t.Fatalf("err = %v, want unsafe repo", err)
	}
}

func TestMissingRemoteIsConfigError(t *testing.T) {
	repo, dir, _ := setup(t)
	commitOnBranch(t, dir, "feature/a")
	cfg := domain.Default()
	cfg.Remote = "nope"
	if err := run(repo, cfg, Options{}); code(err) != apperr.CodeConfig {
		t.Fatalf("err = %v, want config error", err)
	}
}

func rewrite(t *testing.T, dir string) {
	t.Helper()
	testutil.WriteFile(t, dir, "feature/a.txt", "rewritten\n")
	testutil.Run(t, dir, "add", "feature/a.txt")
	testutil.Run(t, dir, "commit", "-q", "--amend", "-m", "feat: rewritten")
}

func TestNonFastForwardNeedsForce(t *testing.T) {
	repo, dir, bare := setup(t)
	commitOnBranch(t, dir, "feature/a")
	if err := run(repo, domain.Default(), Options{}); err != nil {
		t.Fatal(err)
	}
	remoteTip := testutil.Run(t, bare, "rev-parse", "feature/a")
	rewrite(t, dir)

	if err := run(repo, domain.Default(), Options{}); code(err) != apperr.CodeExternal {
		t.Fatalf("plain push of rewritten history: err = %v, want external error", err)
	}
	if testutil.Run(t, bare, "rev-parse", "feature/a") != remoteTip {
		t.Fatal("remote moved without --force")
	}

	if err := run(repo, domain.Default(), Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	if testutil.Run(t, bare, "rev-parse", "feature/a") != testutil.Run(t, dir, "rev-parse", "HEAD") {
		t.Error("forced push did not update the remote")
	}
}

func TestForceDeclinedPushesNothing(t *testing.T) {
	repo, dir, bare := setup(t)
	commitOnBranch(t, dir, "feature/a")
	if err := run(repo, domain.Default(), Options{}); err != nil {
		t.Fatal(err)
	}
	remoteTip := testutil.Run(t, bare, "rev-parse", "feature/a")
	rewrite(t, dir)

	err := run(repo, domain.Default(), Options{Force: true, Confirm: func(string) (bool, error) { return false, nil }})
	if err == nil {
		t.Fatal("expected abort")
	}
	if testutil.Run(t, bare, "rev-parse", "feature/a") != remoteTip {
		t.Error("remote moved although the user declined")
	}
}

func TestForceKeepsLeaseOverUnseenWork(t *testing.T) {
	repo, dir, bare := setup(t)
	commitOnBranch(t, dir, "feature/a")
	if err := run(repo, domain.Default(), Options{}); err != nil {
		t.Fatal(err)
	}
	// a teammate pushes to the same branch; we never fetch it
	other := t.TempDir()
	testutil.Run(t, other, "clone", "-q", "-b", "feature/a", bare, ".")
	testutil.WriteFile(t, other, "theirs.txt", "t\n")
	testutil.Commit(t, other, "feat: theirs")
	testutil.Run(t, other, "push", "-q", "origin", "feature/a")
	theirs := testutil.Run(t, bare, "rev-parse", "feature/a")
	rewrite(t, dir)

	if err := run(repo, domain.Default(), Options{Force: true}); code(err) != apperr.CodeExternal {
		t.Fatalf("err = %v, want rejection by the lease", err)
	}
	if testutil.Run(t, bare, "rev-parse", "feature/a") != theirs {
		t.Error("force overwrote commits we had not fetched")
	}
}
