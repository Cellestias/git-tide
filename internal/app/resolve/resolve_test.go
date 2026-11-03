package resolve

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

func printer() output.Printer { return output.Printer{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}} }

func cont(repo *repository.Repository) error {
	return Continue(context.Background(), repo, domain.Default(), printer())
}

func abort(repo *repository.Repository, confirm func(string) (bool, error)) error {
	return Abort(context.Background(), repo, Options{Confirm: confirm}, printer())
}

// conflictRepo returns a repo on feature/a that conflicts with feature/b on f.txt.
func conflictRepo(t *testing.T) (*repository.Repository, string) {
	t.Helper()
	dir := testutil.NewRepo(t)
	testutil.WriteFile(t, dir, "f.txt", "base\n")
	testutil.Commit(t, dir, "feat: base")
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/b")
	testutil.WriteFile(t, dir, "f.txt", "from b\n")
	testutil.Commit(t, dir, "feat: b")
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/a", "main")
	testutil.WriteFile(t, dir, "f.txt", "from a\n")
	testutil.Commit(t, dir, "feat: a")
	return repository.New(executor.Exec{}, dir), dir
}

func startMerge(t *testing.T, dir string) {
	t.Helper()
	if _, err := testutil.TryRun(dir, "merge", "--no-edit", "feature/b"); err == nil {
		t.Fatal("merge was expected to conflict")
	}
}

func resolveFile(t *testing.T, dir, content string) {
	t.Helper()
	testutil.WriteFile(t, dir, "f.txt", content)
	testutil.Run(t, dir, "add", "f.txt")
}

func opOf(t *testing.T, repo *repository.Repository) string {
	t.Helper()
	op, err := repo.InProgressOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func TestNothingInProgress(t *testing.T) {
	repo, _ := conflictRepo(t)
	for name, err := range map[string]error{"continue": cont(repo), "abort": abort(repo, nil)} {
		if apperr.CodeOf(err) != apperr.CodeUnsafeRepo || !strings.Contains(err.Error(), "nothing to "+name) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestContinueRefusesWhileConflictsRemain(t *testing.T) {
	repo, dir := conflictRepo(t)
	startMerge(t, dir)

	err := cont(repo)
	if apperr.CodeOf(err) != apperr.CodeUnsafeRepo || !strings.Contains(err.Error(), "f.txt") {
		t.Fatalf("err = %v", err)
	}
	if opOf(t, repo) != "merge" {
		t.Error("the merge must still be in progress")
	}
}

func TestContinueFinishesMerge(t *testing.T) {
	repo, dir := conflictRepo(t)
	startMerge(t, dir)
	resolveFile(t, dir, "resolved\n")

	if err := cont(repo); err != nil {
		t.Fatal(err)
	}
	if opOf(t, repo) != "" {
		t.Error("merge still in progress")
	}
	if parents := strings.Fields(testutil.Run(t, dir, "log", "-1", "--format=%P")); len(parents) != 2 {
		t.Errorf("HEAD has %d parent(s), want a merge commit", len(parents))
	}
	if got := testutil.Run(t, dir, "show", "HEAD:f.txt"); got != "resolved" {
		t.Errorf("f.txt = %q", got)
	}
}

func TestContinueFinishesRebase(t *testing.T) {
	repo, dir := conflictRepo(t)
	if _, err := testutil.TryRun(dir, "rebase", "feature/b"); err == nil {
		t.Fatal("rebase was expected to conflict")
	}
	resolveFile(t, dir, "resolved\n")

	if err := cont(repo); err != nil {
		t.Fatal(err)
	}
	if opOf(t, repo) != "" {
		t.Error("rebase still in progress")
	}
	if got := testutil.Run(t, dir, "log", "-1", "--format=%s"); got != "feat: a" {
		t.Errorf("subject = %q", got)
	}
}

func TestContinueBlocksLeftoverMarkers(t *testing.T) {
	repo, dir := conflictRepo(t)
	startMerge(t, dir)
	testutil.Run(t, dir, "add", "f.txt") // staged with the markers still in it

	err := cont(repo)
	if apperr.CodeOf(err) != apperr.CodePolicy || !strings.Contains(err.Error(), "f.txt") {
		t.Fatalf("err = %v", err)
	}
	if opOf(t, repo) != "merge" {
		t.Error("the merge must still be in progress")
	}
}

func TestAbortRestoresTheBranch(t *testing.T) {
	repo, dir := conflictRepo(t)
	head := testutil.Run(t, dir, "rev-parse", "HEAD")
	startMerge(t, dir)

	if err := abort(repo, func(string) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	if opOf(t, repo) != "" || testutil.Run(t, dir, "rev-parse", "HEAD") != head {
		t.Error("branch not restored")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "f.txt")); string(b) != "from a\n" {
		t.Errorf("f.txt = %q", b)
	}
}

func TestAbortDeclinedChangesNothing(t *testing.T) {
	repo, dir := conflictRepo(t)
	startMerge(t, dir)

	err := abort(repo, func(string) (bool, error) { return false, nil })
	if err == nil || opOf(t, repo) != "merge" {
		t.Fatalf("declined abort must leave the merge in progress; err = %v", err)
	}
}
