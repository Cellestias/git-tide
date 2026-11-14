package undo

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

func printer() (output.Printer, *bytes.Buffer) {
	var buf bytes.Buffer
	return output.Printer{Out: &buf, Err: &buf}, &buf
}

// setup returns a clone on feature/x with one local commit that is not pushed.
func setup(t *testing.T) (*repository.Repository, string, string) {
	t.Helper()
	dir, bare := testutil.NewRepoWithRemote(t)
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/x")
	testutil.WriteFile(t, dir, "x.txt", "x\n")
	testutil.Commit(t, dir, "feat: x")
	return repository.New(executor.Exec{}, dir), dir, bare
}

func TestUndoKeepsTheChangesStaged(t *testing.T) {
	repo, dir, _ := setup(t)
	p, buf := printer()
	if err := Undo(context.Background(), repo, p); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Run(t, dir, "log", "-1", "--format=%s"); got != "feat: initial commit" {
		t.Errorf("HEAD = %q", got)
	}
	if got := testutil.Run(t, dir, "diff", "--cached", "--name-only"); got != "x.txt" {
		t.Errorf("staged = %q, want x.txt", got)
	}
	if !strings.Contains(buf.String(), "feat: x") {
		t.Errorf("should name the undone commit:\n%s", buf)
	}
}

func TestUndoAndAmendRefuseWhatCouldBeShared(t *testing.T) {
	repo, dir, _ := setup(t)
	ctx := context.Background()
	p, _ := printer()
	testutil.Run(t, dir, "push", "-q", "-u", "origin", "feature/x")
	if err := Undo(ctx, repo, p); apperr.CodeOf(err) != apperr.CodePolicy {
		t.Errorf("undo of a pushed commit: %v", err)
	}
	if err := Amend(ctx, repo, domain.Default(), AmendOptions{Message: "feat: y"}, p); apperr.CodeOf(err) != apperr.CodePolicy {
		t.Errorf("amend of a pushed commit: %v", err)
	}

	testutil.Run(t, dir, "checkout", "-q", "-b", "side", "main")
	testutil.WriteFile(t, dir, "s.txt", "s\n")
	testutil.Commit(t, dir, "feat: side")
	testutil.Run(t, dir, "checkout", "-q", "feature/x")
	testutil.Run(t, dir, "merge", "-q", "--no-ff", "-m", "merge: side", "side")
	if err := Undo(ctx, repo, p); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("undo of a merge: %v", err)
	}
}

func TestUndoRefusesTheFirstCommit(t *testing.T) {
	dir := testutil.NewEmptyRepo(t)
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	testutil.Commit(t, dir, "chore: initial commit")
	p, _ := printer()
	if err := Undo(context.Background(), repository.New(executor.Exec{}, dir), p); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("err = %v, want unsafe repo", err)
	}
	empty := testutil.NewEmptyRepo(t)
	if err := Undo(context.Background(), repository.New(executor.Exec{}, empty), p); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("empty repo: err = %v, want unsafe repo", err)
	}
}

func TestAmendFoldsFilesAndRewords(t *testing.T) {
	repo, dir, _ := setup(t)
	ctx := context.Background()
	cfg := domain.Default()
	p, _ := printer()

	testutil.WriteFile(t, dir, "forgotten.txt", "f\n")
	if err := Amend(ctx, repo, cfg, AmendOptions{Files: []string{"forgotten.txt"}, Cwd: dir}, p); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Run(t, dir, "show", "--name-only", "--format=%s", "HEAD"); got != "feat: x\n\nforgotten.txt\nx.txt" {
		t.Errorf("amended commit:\n%s", got)
	}

	if err := Amend(ctx, repo, cfg, AmendOptions{Message: "bad message"}, p); apperr.CodeOf(err) != apperr.CodePolicy {
		t.Errorf("non-conventional message: %v", err)
	}
	if err := Amend(ctx, repo, cfg, AmendOptions{Type: "fix", Scope: "auth", Message: "reject empty token"}, p); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Run(t, dir, "log", "-1", "--format=%s"); got != "fix(auth): reject empty token" {
		t.Errorf("subject = %q", got)
	}
	if err := Amend(ctx, repo, cfg, AmendOptions{}, p); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("nothing to amend: %v", err)
	}
	if err := Amend(ctx, repo, cfg, AmendOptions{Type: "fix"}, p); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("--type without -m: %v", err)
	}
}

func TestAmendScansForSecretsAndRestoresTheIndex(t *testing.T) {
	repo, dir, _ := setup(t)
	p, _ := printer()
	testutil.WriteFile(t, dir, "creds.txt", "aws_key = AKIAIOSFODNN7EXAMPLE\n")
	err := Amend(context.Background(), repo, domain.Default(), AmendOptions{Files: []string{"creds.txt"}, Cwd: dir}, p)
	if apperr.CodeOf(err) != apperr.CodePolicy {
		t.Fatalf("err = %v, want policy error", err)
	}
	if got := testutil.Run(t, dir, "diff", "--cached", "--name-only"); got != "" {
		t.Errorf("staging was not undone: %q", got)
	}
}

func TestAmendRefusesAProtectedBranch(t *testing.T) {
	dir := testutil.NewEmptyRepo(t)
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	testutil.Commit(t, dir, "chore: initial commit") // a plain git commit, hooks are not installed here
	repo := repository.New(executor.Exec{}, dir)
	p, _ := printer()
	if err := Amend(context.Background(), repo, domain.Default(), AmendOptions{Message: "chore: first commit"}, p); apperr.CodeOf(err) != apperr.CodePolicy {
		t.Fatalf("amend on main: err = %v, want policy error", err)
	}
}
