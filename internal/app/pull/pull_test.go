package pull

import (
	"bytes"
	"context"
	"testing"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/testutil"
)

// setup returns a clone on feature/x (pushed, with an upstream) and a second
// clone of the same remote, on feature/x too, to make the remote move.
func setup(t *testing.T) (repo *repository.Repository, dir, other string) {
	t.Helper()
	dir, bare := testutil.NewRepoWithRemote(t)
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/x")
	testutil.WriteFile(t, dir, "x.txt", "x\n")
	testutil.Commit(t, dir, "feat: x")
	testutil.Run(t, dir, "push", "-q", "-u", "origin", "feature/x")
	other = t.TempDir()
	testutil.Run(t, other, "clone", "-q", "--branch", "feature/x", bare, ".")
	testutil.Run(t, other, "config", "user.email", "o@example.invalid")
	testutil.Run(t, other, "config", "user.name", "Other")
	return repository.New(executor.Exec{}, dir), dir, other
}

func run(repo *repository.Repository, cfg domain.Config) (string, error) {
	var out, errOut bytes.Buffer
	err := Run(context.Background(), repo, cfg, output.Printer{Out: &out, Err: &errOut})
	return out.String() + errOut.String(), err
}

func remoteCommit(t *testing.T, other, file, msg string) {
	t.Helper()
	testutil.WriteFile(t, other, file, msg+"\n")
	testutil.Commit(t, other, msg)
	testutil.Run(t, other, "push", "-q", "origin", "feature/x")
}

func TestUpToDateAndAhead(t *testing.T) {
	repo, dir, _ := setup(t)
	if out, err := run(repo, domain.Default()); err != nil {
		t.Fatalf("up to date: %v\n%s", err, out)
	}
	testutil.WriteFile(t, dir, "y.txt", "y\n")
	testutil.Commit(t, dir, "feat: y")
	head := testutil.Run(t, dir, "rev-parse", "HEAD")
	if out, err := run(repo, domain.Default()); err != nil {
		t.Fatalf("ahead: %v\n%s", err, out)
	}
	if testutil.Run(t, dir, "rev-parse", "HEAD") != head {
		t.Error("an ahead branch must be left alone")
	}
}

func TestFastForwardsWhenBehind(t *testing.T) {
	repo, dir, other := setup(t)
	remoteCommit(t, other, "r.txt", "feat: remote work")
	if out, err := run(repo, domain.Default()); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	if got := testutil.Run(t, dir, "log", "-1", "--format=%s"); got != "feat: remote work" {
		t.Errorf("HEAD = %q", got)
	}
}

func TestIntegratesWhenDiverged(t *testing.T) {
	for _, strategy := range []string{domain.StrategyMerge, domain.StrategyRebase} {
		t.Run(strategy, func(t *testing.T) {
			repo, dir, other := setup(t)
			remoteCommit(t, other, "r.txt", "feat: remote work")
			testutil.WriteFile(t, dir, "l.txt", "l\n")
			testutil.Commit(t, dir, "feat: local work")
			cfg := domain.Default()
			cfg.Update.Strategy = strategy
			if out, err := run(repo, cfg); err != nil {
				t.Fatalf("pull: %v\n%s", err, out)
			}
			log := testutil.Run(t, dir, "log", "--format=%s")
			for _, want := range []string{"feat: remote work", "feat: local work"} {
				if !bytes.Contains([]byte(log), []byte(want)) {
					t.Errorf("%q missing from the log:\n%s", want, log)
				}
			}
		})
	}
}

func TestConflictLeavesTheOperationForTheUser(t *testing.T) {
	repo, dir, other := setup(t)
	remoteCommit(t, other, "x.txt", "feat: remote edit")
	testutil.WriteFile(t, dir, "x.txt", "local edit\n")
	testutil.Commit(t, dir, "feat: local edit")
	_, err := run(repo, domain.Default())
	if apperr.CodeOf(err) != apperr.CodeGeneral {
		t.Fatalf("err = %v, want a general error (conflicts)", err)
	}
	if op, _ := repo.InProgressOperation(context.Background()); op == "" {
		t.Error("the merge should be left in progress")
	}
}

func TestRefusesUncommittedWork(t *testing.T) {
	repo, dir, other := setup(t)
	remoteCommit(t, other, "r.txt", "feat: remote work")
	testutil.WriteFile(t, dir, "x.txt", "dirty\n")
	if _, err := run(repo, domain.Default()); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Fatalf("err = %v, want unsafe repo", err)
	}
	if got := testutil.Run(t, dir, "log", "-1", "--format=%s"); got != "feat: x" {
		t.Errorf("nothing may change, HEAD = %q", got)
	}
}

func TestBranchNotOnTheRemoteSaysSo(t *testing.T) {
	repo, dir, _ := setup(t)
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/local")
	_, err := run(repo, domain.Default())
	if apperr.CodeOf(err) != apperr.CodeUsage {
		t.Fatalf("err = %v, want usage error pointing to gtide push", err)
	}
}

func TestMissingRemote(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/x")
	if _, err := run(repository.New(executor.Exec{}, dir), domain.Default()); apperr.CodeOf(err) != apperr.CodeConfig {
		t.Fatalf("err = %v, want config error", err)
	}
}
