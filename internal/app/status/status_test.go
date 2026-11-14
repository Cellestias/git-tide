package status

import (
	"bytes"
	"context"
	"strings"
	"testing"

	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/testutil"
)

func printer() (output.Printer, *bytes.Buffer) {
	var buf bytes.Buffer
	return output.Printer{Out: &buf, Err: &buf}, &buf
}

func setup(t *testing.T) (*repository.Repository, string) {
	t.Helper()
	dir := testutil.NewRepo(t)
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/x")
	return repository.New(executor.Exec{}, dir), dir
}

func TestStatusGroupsTheChanges(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "staged.txt", "s\n")
	testutil.Run(t, dir, "add", "staged.txt")
	testutil.WriteFile(t, dir, "README.md", "changed\n")
	testutil.WriteFile(t, dir, "new.txt", "n\n")
	p, buf := printer()
	if err := Run(context.Background(), repo, domain.Default(), p); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"On feature/x", "Staged (1)", "A  staged.txt", "Not staged (1)", "M  README.md", "Untracked (1)", "?  new.txt", "base", "main: up to date", "none yet"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing:\n%s", want, out)
		}
	}
}

func TestStatusCleanAndInProgress(t *testing.T) {
	repo, dir := setup(t)
	p, buf := printer()
	if err := Run(context.Background(), repo, domain.Default(), p); err != nil || !strings.Contains(buf.String(), "working tree clean") {
		t.Fatalf("clean: err=%v\n%s", err, buf)
	}
	testutil.Run(t, dir, "checkout", "-q", "main")
	testutil.WriteFile(t, dir, "c.txt", "main\n")
	testutil.Commit(t, dir, "feat: main side")
	testutil.Run(t, dir, "checkout", "-q", "feature/x")
	testutil.WriteFile(t, dir, "c.txt", "branch\n")
	testutil.Commit(t, dir, "feat: branch side")
	_, _ = testutil.TryRun(dir, "merge", "main")
	p, buf = printer()
	if err := Run(context.Background(), repo, domain.Default(), p); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Conflicts (1)", "a merge is in progress", "gtide continue"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("%q missing:\n%s", want, buf)
		}
	}
}

func TestStatusShowsRenames(t *testing.T) {
	repo, dir := setup(t)
	testutil.Run(t, dir, "mv", "README.md", "READ.md")
	p, buf := printer()
	if err := Run(context.Background(), repo, domain.Default(), p); err != nil || !strings.Contains(buf.String(), "README.md -> READ.md") {
		t.Fatalf("err=%v\n%s", err, buf)
	}
}

func TestDiff(t *testing.T) {
	repo, dir := setup(t)
	ctx := context.Background()
	p, buf := printer()
	if err := Diff(ctx, repo, DiffOptions{Cwd: dir}, p); err != nil || !strings.Contains(buf.String(), "No changes") {
		t.Fatalf("no changes: err=%v\n%s", err, buf)
	}
	testutil.WriteFile(t, dir, "README.md", "hello\nmore\n")
	testutil.WriteFile(t, dir, "other.txt", "o\n")
	testutil.Run(t, dir, "add", "other.txt")

	p, buf = printer()
	if err := Diff(ctx, repo, DiffOptions{Cwd: dir}, p); err != nil || !strings.Contains(buf.String(), "README.md") || strings.Contains(buf.String(), "other.txt") || !strings.Contains(buf.String(), "+1 -0") {
		t.Fatalf("work tree: err=%v\n%s", err, buf)
	}
	p, buf = printer()
	if err := Diff(ctx, repo, DiffOptions{Staged: true, Cwd: dir}, p); err != nil || !strings.Contains(buf.String(), "other.txt") || strings.Contains(buf.String(), "README.md") {
		t.Fatalf("staged: err=%v\n%s", err, buf)
	}
	p, buf = printer()
	if err := Diff(ctx, repo, DiffOptions{File: "README.md", Patch: true, Cwd: dir}, p); err != nil || !strings.Contains(buf.String(), "+more") || !strings.Contains(buf.String(), "diff --git") {
		t.Fatalf("patch: err=%v\n%s", err, buf)
	}
	if err := Diff(ctx, repo, DiffOptions{File: "../escape", Cwd: dir}, p); err == nil {
		t.Error("a path outside the repository must be refused")
	}
}
