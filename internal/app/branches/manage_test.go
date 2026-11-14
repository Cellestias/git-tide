package branches

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

func printer() output.Printer {
	var out, errOut bytes.Buffer
	return output.Printer{Out: &out, Err: &errOut}
}

// manageSetup has main, feature/merged (already in main), feature/open (one
// commit main lacks) and the clone is on main.
func manageSetup(t *testing.T) (*repository.Repository, string, string) {
	t.Helper()
	dir, bare := testutil.NewRepoWithRemote(t)
	testutil.Run(t, dir, "branch", "feature/merged") // same tip as main: merged
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/open")
	testutil.WriteFile(t, dir, "o.txt", "o\n")
	testutil.Commit(t, dir, "feat: open")
	testutil.Run(t, dir, "checkout", "-q", "main")
	return repository.New(executor.Exec{}, dir), dir, bare
}

func branchList(t *testing.T, dir string) string {
	return testutil.Run(t, dir, "branch", "--format=%(refname:short)")
}

func code(err error) int { return apperr.CodeOf(err) }

func TestDeleteMergedOnlyByName(t *testing.T) {
	repo, dir, _ := manageSetup(t)
	ctx := context.Background()
	cfg := domain.Default()
	if err := Delete(ctx, repo, cfg, []string{"feature/merged"}, DeleteOptions{}, printer()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(branchList(t, dir), "feature/merged") {
		t.Error("feature/merged should be gone")
	}
	if err := Delete(ctx, repo, cfg, []string{"feature/open"}, DeleteOptions{}, printer()); code(err) != apperr.CodeUnsafeRepo {
		t.Errorf("unmerged branch: %v", err)
	}
	if !strings.Contains(branchList(t, dir), "feature/open") {
		t.Error("an unmerged branch must survive without --force")
	}
	if err := Delete(ctx, repo, cfg, []string{"feature/open"}, DeleteOptions{Force: true}, printer()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(branchList(t, dir), "feature/open") {
		t.Error("--force should delete it")
	}
}

func TestDeleteRefusals(t *testing.T) {
	repo, dir, _ := manageSetup(t)
	ctx := context.Background()
	cfg := domain.Default()
	if err := Delete(ctx, repo, cfg, []string{"main"}, DeleteOptions{Force: true}, printer()); code(err) != apperr.CodePolicy {
		t.Errorf("protected: %v", err)
	}
	testutil.Run(t, dir, "checkout", "-q", "feature/merged")
	if err := Delete(ctx, repo, cfg, []string{"feature/merged"}, DeleteOptions{}, printer()); code(err) != apperr.CodeUnsafeRepo {
		t.Errorf("checked out: %v", err)
	}
	if err := Delete(ctx, repo, cfg, []string{"nope"}, DeleteOptions{}, printer()); code(err) != apperr.CodeUsage {
		t.Errorf("unknown: %v", err)
	}
	if err := Delete(ctx, repo, cfg, []string{"--all"}, DeleteOptions{}, printer()); code(err) != apperr.CodeUsage {
		t.Errorf("option-like name: %v", err)
	}
	if err := Delete(ctx, repo, cfg, nil, DeleteOptions{}, printer()); code(err) != apperr.CodeUsage {
		t.Errorf("no names: %v", err)
	}
	// refusing one branch of several deletes none
	err := Delete(ctx, repo, cfg, []string{"feature/open", "main"}, DeleteOptions{Force: true}, printer())
	if code(err) != apperr.CodePolicy || !strings.Contains(branchList(t, dir), "feature/open") {
		t.Errorf("all-or-nothing: err=%v\n%s", err, branchList(t, dir))
	}
}

func TestDeleteMergedFlagAndConfirmation(t *testing.T) {
	repo, dir, _ := manageSetup(t)
	ctx := context.Background()
	cfg := domain.Default()
	no := func(string) (bool, error) { return false, nil }
	if err := Delete(ctx, repo, cfg, nil, DeleteOptions{Merged: true, Confirm: no}, printer()); err == nil {
		t.Fatal("declined confirmation must abort")
	}
	if !strings.Contains(branchList(t, dir), "feature/merged") {
		t.Fatal("a declined delete removed a branch")
	}
	var asked string
	yes := func(p string) (bool, error) { asked = p; return true, nil }
	if err := Delete(ctx, repo, cfg, nil, DeleteOptions{Merged: true, Confirm: yes}, printer()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asked, "feature/merged") || strings.Contains(asked, "feature/open") {
		t.Errorf("prompt should list only merged branches: %q", asked)
	}
	list := branchList(t, dir)
	if strings.Contains(list, "feature/merged") || !strings.Contains(list, "feature/open") || !strings.Contains(list, "main") {
		t.Errorf("branches left:\n%s", list)
	}
}

func TestDeleteAlsoOnTheRemote(t *testing.T) {
	repo, dir, bare := manageSetup(t)
	testutil.Run(t, dir, "push", "-q", "origin", "feature/merged")
	if err := Delete(context.Background(), repo, domain.Default(), []string{"feature/merged"}, DeleteOptions{Remote: true}, printer()); err != nil {
		t.Fatal(err)
	}
	if out := testutil.Run(t, bare, "branch", "--list", "feature/merged"); out != "" {
		t.Errorf("remote branch still there: %q", out)
	}
	// remote-only branch
	testutil.Run(t, dir, "push", "-q", "origin", "main:refs/heads/feature/remote-only")
	if err := Delete(context.Background(), repo, domain.Default(), []string{"feature/remote-only"}, DeleteOptions{}, printer()); code(err) != apperr.CodeUsage {
		t.Errorf("remote-only without --remote: %v", err)
	}
	if err := Delete(context.Background(), repo, domain.Default(), []string{"feature/remote-only"}, DeleteOptions{Remote: true}, printer()); err != nil {
		t.Fatal(err)
	}
	if out := testutil.Run(t, bare, "branch", "--list", "feature/remote-only"); out != "" {
		t.Errorf("remote-only branch still there: %q", out)
	}
}

func TestRename(t *testing.T) {
	repo, dir, _ := manageSetup(t)
	ctx := context.Background()
	cfg := domain.Default()
	if err := Rename(ctx, repo, cfg, "feature/open", "feature/renamed", printer()); err != nil {
		t.Fatal(err)
	}
	list := branchList(t, dir)
	if !strings.Contains(list, "feature/renamed") || strings.Contains(list, "feature/open") {
		t.Errorf("branches:\n%s", list)
	}
	for _, tc := range [][2]string{{"main", "trunk"}, {"feature/renamed", "main"}, {"nope", "x"}, {"feature/renamed", "feature/merged"}, {"feature/renamed", "feature/renamed"}, {"feature/renamed", "--bad"}} {
		if err := Rename(ctx, repo, cfg, tc[0], tc[1], printer()); err == nil {
			t.Errorf("Rename(%q, %q) accepted", tc[0], tc[1])
		}
	}
}
