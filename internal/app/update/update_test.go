package update

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/testutil"
)

// setup returns a clone on feature/x plus a second clone used to advance origin/main.
func setup(t *testing.T) (repo *repository.Repository, dir, other string) {
	t.Helper()
	dir, bare := testutil.NewRepoWithRemote(t)
	other = t.TempDir()
	testutil.Run(t, other, "clone", "-q", bare, ".")
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/x")
	testutil.WriteFile(t, dir, "feature.txt", "f\n")
	testutil.Commit(t, dir, "feat: feature work")
	return repository.New(executor.Exec{}, dir), dir, other
}

func advanceOrigin(t *testing.T, other, file string) {
	t.Helper()
	testutil.WriteFile(t, other, file, "upstream\n")
	testutil.Commit(t, other, "feat: upstream "+file)
	testutil.Run(t, other, "push", "-q", "origin", "main")
}

func run(t *testing.T, repo *repository.Repository, cfg domain.Config, opts Options) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := Run(context.Background(), repo, cfg, opts, output.Printer{Out: &out, Err: &errOut})
	return out.String(), err
}

func TestMergeStrategy(t *testing.T) {
	repo, dir, other := setup(t)
	advanceOrigin(t, other, "upstream.txt")

	out, err := run(t, repo, domain.Default(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Updated") {
		t.Errorf("output = %q", out)
	}
	if testutil.Run(t, dir, "rev-parse", "--abbrev-ref", "HEAD") != "feature/x" {
		t.Error("did not stay on the working branch")
	}
	if _, err := os.Stat(filepath.Join(dir, "upstream.txt")); err != nil {
		t.Error("upstream change not integrated")
	}
	if testutil.Run(t, dir, "rev-parse", "main") != testutil.Run(t, dir, "rev-parse", "origin/main") {
		t.Error("local main not fast-forwarded")
	}
	if n := testutil.Run(t, dir, "rev-list", "--merges", "--count", "HEAD"); n != "1" {
		t.Errorf("merge commits = %s", n)
	}
}

func TestRebaseStrategy(t *testing.T) {
	repo, dir, other := setup(t)
	advanceOrigin(t, other, "upstream.txt")
	cfg := domain.Default()
	cfg.Update.Strategy = domain.StrategyRebase

	if _, err := run(t, repo, cfg, Options{}); err != nil {
		t.Fatal(err)
	}
	if n := testutil.Run(t, dir, "rev-list", "--merges", "--count", "HEAD"); n != "0" {
		t.Errorf("rebase left merges: %s", n)
	}
	if testutil.Run(t, dir, "merge-base", "main", "HEAD") != testutil.Run(t, dir, "rev-parse", "main") {
		t.Error("branch not on top of main")
	}
}

func TestAlreadyUpToDate(t *testing.T) {
	repo, _, _ := setup(t)
	out, err := run(t, repo, domain.Default(), Options{})
	if err != nil || !strings.Contains(out, "Already up to date") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestOnBaseBranchFastForwards(t *testing.T) {
	repo, dir, other := setup(t)
	testutil.Run(t, dir, "checkout", "-q", "main")
	advanceOrigin(t, other, "upstream.txt")
	if _, err := run(t, repo, domain.Default(), Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "upstream.txt")); err != nil {
		t.Error("main not fast-forwarded")
	}
}

func TestExplicitTargetAndMapping(t *testing.T) {
	repo, dir, other := setup(t)
	testutil.Run(t, other, "checkout", "-q", "-b", "stage")
	testutil.WriteFile(t, other, "stage.txt", "s\n")
	testutil.Commit(t, other, "feat: stage")
	testutil.Run(t, other, "push", "-q", "origin", "stage")

	cfg := domain.Default()
	cfg.Branches.Base = map[string]string{"feature/*": "stage"}
	if _, err := run(t, repo, cfg, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stage.txt")); err != nil {
		t.Error("mapping to stage ignored")
	}

	// explicit argument overrides the mapping
	testutil.Run(t, other, "checkout", "-q", "main")
	advanceOrigin(t, other, "again.txt")
	if _, err := run(t, repo, cfg, Options{Target: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "again.txt")); err != nil {
		t.Error("explicit target ignored")
	}
}

func TestRefusesDirtyTree(t *testing.T) {
	repo, dir, other := setup(t)
	advanceOrigin(t, other, "upstream.txt")
	testutil.WriteFile(t, dir, "feature.txt", "dirty\n")
	before := testutil.Run(t, dir, "rev-parse", "HEAD")

	_, err := run(t, repo, domain.Default(), Options{})
	if apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Fatalf("err = %v (code %d)", err, apperr.CodeOf(err))
	}
	if testutil.Run(t, dir, "rev-parse", "HEAD") != before {
		t.Error("repository changed despite refusal")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "feature.txt")); string(got) != "dirty\n" {
		t.Error("user changes lost")
	}
	if testutil.Run(t, dir, "rev-parse", "origin/main") == testutil.Run(t, other, "rev-parse", "HEAD") {
		t.Error("network fetch happened before safety checks")
	}
}

func TestRefusesDetachedHeadAndMissingRemote(t *testing.T) {
	repo, dir, _ := setup(t)
	cfg := domain.Default()
	cfg.Remote = "nope"
	if _, err := run(t, repo, cfg, Options{}); apperr.CodeOf(err) != apperr.CodeConfig {
		t.Errorf("missing remote: %v", err)
	}
	testutil.Run(t, dir, "checkout", "-q", "--detach")
	if _, err := run(t, repo, domain.Default(), Options{}); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("detached: %v", err)
	}
}

func TestConflictLeavesNormalGitState(t *testing.T) {
	repo, dir, other := setup(t)
	testutil.WriteFile(t, other, "feature.txt", "upstream version\n")
	testutil.Commit(t, other, "feat: clash")
	testutil.Run(t, other, "push", "-q", "origin", "main")

	_, err := run(t, repo, domain.Default(), Options{})
	if err == nil || !strings.Contains(apperr.HintOf(err), "git merge --continue") {
		t.Fatalf("err = %v, hint = %q", err, apperr.HintOf(err))
	}
	if op, _ := repo.InProgressOperation(context.Background()); op != "merge" {
		t.Errorf("op = %q", op)
	}
	// a second run must refuse while the merge is unfinished
	if _, err := run(t, repo, domain.Default(), Options{}); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("second run: %v", err)
	}
	_ = dir
}

func TestDivergedLocalBaseIsRefused(t *testing.T) {
	repo, dir, other := setup(t)
	testutil.Run(t, dir, "checkout", "-q", "main")
	testutil.WriteFile(t, dir, "local.txt", "l\n")
	testutil.Commit(t, dir, "feat: local only")
	testutil.Run(t, dir, "checkout", "-q", "feature/x")
	advanceOrigin(t, other, "upstream.txt")

	_, err := run(t, repo, domain.Default(), Options{})
	if apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Fatalf("err = %v", err)
	}
}
