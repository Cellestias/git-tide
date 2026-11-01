package repository

import (
	"context"
	"errors"
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
