package repository

import (
	"context"
	"testing"

	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/testutil"
)

func TestListBranches(t *testing.T) {
	ctx := context.Background()
	dir, bare := testutil.NewRepoWithRemote(t)
	repo, _ := Open(ctx, executor.Exec{}, dir)

	// feature/a: pushed, then one local commit ahead
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/a")
	testutil.Run(t, dir, "push", "-q", "-u", "origin", "feature/a")
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	testutil.Commit(t, dir, "feat: a")
	// feature/gone: pushed, then deleted on the remote
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/gone", "main")
	testutil.Run(t, dir, "push", "-q", "-u", "origin", "feature/gone")
	testutil.Run(t, bare, "branch", "-q", "-D", "feature/gone")
	// remote-only branch
	testutil.Run(t, bare, "branch", "remote-only", "main")
	testutil.Run(t, dir, "fetch", "-q", "--prune", "origin")

	refs, err := repo.ListBranches(ctx, "origin", true)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]BranchRef{}
	for _, r := range refs {
		key := r.Name
		if r.Remote {
			key = "remote:" + r.Name
		}
		by[key] = r
	}

	a := by["feature/a"]
	if a.Upstream != "origin/feature/a" || a.UpAhead != 1 || a.UpBehind != 0 || a.UpGone {
		t.Errorf("feature/a = %+v", a)
	}
	if a.AuthorName != "Git Tide Test" || a.AuthorEmail != "git-tide-test@example.invalid" || a.Updated.IsZero() {
		t.Errorf("feature/a author/date = %+v", a)
	}
	if g := by["feature/gone"]; !g.UpGone {
		t.Errorf("feature/gone = %+v", g)
	}
	if !by["feature/gone"].Current && by["feature/a"].Current {
		t.Error("current marker wrong")
	}
	if cur := by["feature/gone"]; !cur.Current {
		t.Errorf("feature/gone should be current: %+v", cur)
	}
	if _, ok := by["remote:remote-only"]; !ok {
		t.Error("remote-only branch missing")
	}
	if _, ok := by["remote:HEAD"]; ok {
		t.Error("symbolic HEAD must be skipped")
	}
	if m := by["main"]; m.Upstream != "origin/main" || m.UpAhead != 0 {
		t.Errorf("main = %+v", m)
	}

	local, err := repo.ListBranches(ctx, "origin", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range local {
		if r.Remote {
			t.Errorf("remote ref listed without includeRemote: %+v", r)
		}
	}
}

func TestAheadBehind(t *testing.T) {
	ctx := context.Background()
	dir := testutil.NewRepo(t)
	repo, _ := Open(ctx, executor.Exec{}, dir)
	testutil.Run(t, dir, "checkout", "-q", "-b", "work")
	for _, f := range []string{"1", "2", "3"} {
		testutil.WriteFile(t, dir, f, f)
		testutil.Commit(t, dir, "feat: "+f)
	}
	testutil.Run(t, dir, "checkout", "-q", "main")
	testutil.WriteFile(t, dir, "m", "m")
	testutil.Commit(t, dir, "feat: m")

	a, b, err := repo.AheadBehind(ctx, "refs/heads/main", "refs/heads/work")
	if err != nil || a != 3 || b != 1 {
		t.Fatalf("ahead=%d behind=%d err=%v", a, b, err)
	}
}
