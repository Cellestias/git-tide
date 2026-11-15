package repository

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/testutil"
)

func TestParseAdditions(t *testing.T) {
	diff := `diff --git a/a.txt b/a.txt
index 111..222 100644
--- a/a.txt
+++ b/a.txt
@@ -1,0 +2,2 @@
+one
+two
@@ -10 +12 @@
-old
+new
diff --git a/dir/b c.txt b/dir/b c.txt
new file mode 100644
--- /dev/null
+++ "b/dir/b c.txt"
@@ -0,0 +1 @@
+x
`
	got, err := parseAdditions(diff)
	if err != nil {
		t.Fatal(err)
	}
	want := []AddedLine{
		{"a.txt", 2, "one"}, {"a.txt", 3, "two"}, {"a.txt", 12, "new"}, {"dir/b c.txt", 1, "x"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestOpenNotRepository(t *testing.T) {
	_, err := Open(context.Background(), executor.Exec{}, t.TempDir())
	if !errors.Is(err, ErrNotRepository) {
		t.Fatalf("err = %v", err)
	}
}

func TestRepositoryState(t *testing.T) {
	ctx := context.Background()
	dir := testutil.NewRepo(t)
	repo, err := Open(ctx, executor.Exec{}, dir)
	if err != nil {
		t.Fatal(err)
	}

	if b, err := repo.CurrentBranch(ctx); err != nil || b != "main" {
		t.Fatalf("branch = %q, %v", b, err)
	}
	if ch, _ := repo.TrackedChanges(ctx); len(ch) != 0 {
		t.Fatalf("expected clean: %v", ch)
	}

	testutil.WriteFile(t, dir, "README.md", "changed\n")
	testutil.WriteFile(t, dir, "untracked.txt", "u\n")
	ch, _ := repo.TrackedChanges(ctx)
	if len(ch) != 1 || ch[0] != "README.md" {
		t.Fatalf("changes = %v (untracked must be ignored)", ch)
	}

	if op, _ := repo.InProgressOperation(ctx); op != "" {
		t.Fatalf("op = %q", op)
	}

	testutil.Run(t, dir, "checkout", "-q", "--detach")
	if _, err := repo.CurrentBranch(ctx); !errors.Is(err, ErrDetachedHead) {
		t.Fatalf("err = %v", err)
	}
}

func TestInProgressMerge(t *testing.T) {
	ctx := context.Background()
	dir := testutil.NewRepo(t)
	testutil.Run(t, dir, "checkout", "-q", "-b", "other")
	testutil.WriteFile(t, dir, "README.md", "other\n")
	testutil.Commit(t, dir, "feat: other")
	testutil.Run(t, dir, "checkout", "-q", "main")
	testutil.WriteFile(t, dir, "README.md", "main\n")
	testutil.Commit(t, dir, "feat: main")
	repo, _ := Open(ctx, executor.Exec{}, dir)
	if err := repo.Merge(ctx, "other"); err == nil {
		t.Fatal("expected conflict")
	}
	if op, _ := repo.InProgressOperation(ctx); op != "merge" {
		t.Fatalf("op = %q", op)
	}
}

func TestStagedAdditions(t *testing.T) {
	ctx := context.Background()
	dir := testutil.NewRepo(t)
	repo, _ := Open(ctx, executor.Exec{}, dir)
	testutil.WriteFile(t, dir, "README.md", "hello\nsecond\n")
	testutil.WriteFile(t, dir, "sub/new file.txt", "a\nb\n")
	testutil.Run(t, dir, "add", "-A")
	got, err := repo.StagedAdditions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Path != "README.md" || got[0].Line != 2 || got[1].Path != "sub/new file.txt" {
		t.Fatalf("got %+v", got)
	}
}

func TestUpdateBranchFromRef(t *testing.T) {
	ctx := context.Background()
	dir := testutil.NewRepo(t)
	repo, _ := Open(ctx, executor.Exec{}, dir)
	testutil.Run(t, dir, "checkout", "-q", "-b", "work")
	testutil.WriteFile(t, dir, "a", "a")
	testutil.Commit(t, dir, "feat: a")
	// creates a missing branch, fast-forwards an existing one
	if err := repo.UpdateBranchFromRef(ctx, "copy", "refs/heads/work"); err != nil {
		t.Fatal(err)
	}
	// diverged (copy has "a", main has "b") → refused
	testutil.Run(t, dir, "checkout", "-q", "main")
	testutil.WriteFile(t, dir, "b", "b")
	testutil.Commit(t, dir, "feat: b")
	if err := repo.UpdateBranchFromRef(ctx, "copy", "refs/heads/main"); err == nil {
		t.Fatal("non-fast-forward update must be refused")
	}
}

func TestPushedPaths(t *testing.T) {
	dir, bare := testutil.NewRepoWithRemote(t)
	repo := New(executor.Exec{}, dir)
	ctx := context.Background()
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/x")
	testutil.WriteFile(t, dir, "a b.txt", "1\n")
	testutil.WriteFile(t, dir, "sub/é.go", "1\n")
	testutil.Commit(t, dir, "feat: first")
	testutil.WriteFile(t, dir, "a b.txt", "2\n")
	testutil.WriteFile(t, dir, "go.mod", "module x\n")
	testutil.Commit(t, dir, "fix: second")

	got, err := repo.PushedPaths(ctx, "origin", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if want := []string{"a b.txt", "go.mod", "sub/é.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a branch that is not published: %q, want %q (each path once, spaces and accents kept)", got, want)
	}

	// once the branch is on the remote, only what comes after counts
	testutil.Run(t, dir, "push", "-q", "-u", "origin", "feature/x")
	if got, err := repo.PushedPaths(ctx, "origin", "HEAD"); err != nil || len(got) != 0 {
		t.Errorf("nothing left to push: %q, %v", got, err)
	}
	testutil.WriteFile(t, dir, "package.json", "{}\n")
	testutil.Commit(t, dir, "feat: third")
	if got, _ := repo.PushedPaths(ctx, "origin", "HEAD"); !reflect.DeepEqual(got, []string{"package.json"}) {
		t.Errorf("after a push: %q", got)
	}
	// without a remote name every remote counts
	if got, _ := repo.PushedPaths(ctx, "", "HEAD"); !reflect.DeepEqual(got, []string{"package.json"}) {
		t.Errorf("any remote: %q", got)
	}
	_ = bare

	// a rename shows both paths
	testutil.Run(t, dir, "mv", "go.mod", "backend.mod")
	testutil.Commit(t, dir, "refactor: rename")
	got, _ = repo.PushedPaths(ctx, "origin", "HEAD")
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"backend.mod", "go.mod", "package.json"}) {
		t.Errorf("rename: %q", got)
	}

	for _, bad := range [][2]string{{"origin", ""}, {"origin", "--output=/tmp/x"}, {"-x", "HEAD"}, {"a*", "HEAD"}, {"a b", "HEAD"}} {
		if _, err := repo.PushedPaths(ctx, bad[0], bad[1]); err == nil {
			t.Errorf("PushedPaths(%q, %q) should be refused", bad[0], bad[1])
		}
	}
}

func TestChangedSince(t *testing.T) {
	dir, _ := testutil.NewRepoWithRemote(t)
	repo := New(executor.Exec{}, dir)
	ctx := context.Background()
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/x")
	testutil.WriteFile(t, dir, "a.txt", "1\n")
	testutil.Commit(t, dir, "feat: a")
	// main moves on: its own change must not show as the branch's
	testutil.Run(t, dir, "checkout", "-q", "main")
	testutil.WriteFile(t, dir, "main-only.txt", "m\n")
	testutil.Commit(t, dir, "feat: on main")
	testutil.Run(t, dir, "checkout", "-q", "feature/x")
	testutil.WriteFile(t, dir, "sub/é b.go", "2\n")
	testutil.Commit(t, dir, "feat: b")

	got, err := repo.ChangedSince(ctx, "refs/heads/main", "refs/heads/feature/x")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if want := []string{"a.txt", "sub/é b.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("changed = %q, want %q (what the branch adds since the fork, not what main did)", got, want)
	}
	if got, err := repo.ChangedSince(ctx, "refs/heads/feature/x", "refs/heads/feature/x"); err != nil || len(got) != 0 {
		t.Errorf("a branch against itself: %q, %v", got, err)
	}
	for _, bad := range [][2]string{{"", "HEAD"}, {"HEAD", ""}, {"--output=/tmp/x", "HEAD"}, {"HEAD", "-x"}} {
		if _, err := repo.ChangedSince(ctx, bad[0], bad[1]); err == nil {
			t.Errorf("ChangedSince(%q, %q) should be refused", bad[0], bad[1])
		}
	}
	if _, err := repo.ChangedSince(ctx, "refs/heads/nope", "HEAD"); err == nil {
		t.Error("an unknown base must be an error")
	}
}
