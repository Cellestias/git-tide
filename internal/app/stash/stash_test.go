package stash

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/testutil"
)

func setup(t *testing.T) (*repository.Repository, string) {
	t.Helper()
	dir := testutil.NewRepo(t)
	return repository.New(executor.Exec{}, dir), dir
}

func printer() (output.Printer, *bytes.Buffer) {
	var buf bytes.Buffer
	return output.Printer{Out: &buf, Err: &buf}, &buf
}

func TestParseRef(t *testing.T) {
	for in, want := range map[string]string{"": "stash@{0}", "2": "stash@{2}", "stash@{3}": "stash@{3}"} {
		if got, err := ParseRef(in); err != nil || got != want {
			t.Errorf("ParseRef(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"-1", "stash@{x}", "HEAD", "0; rm", "--all"} {
		if _, err := ParseRef(bad); err == nil {
			t.Errorf("ParseRef(%q) accepted", bad)
		}
	}
}

func TestPushListPopDrop(t *testing.T) {
	repo, dir := setup(t)
	ctx := context.Background()
	p, buf := printer()

	if err := Push(ctx, repo, "", false, p); err != nil || !strings.Contains(buf.String(), "No local changes") {
		t.Fatalf("nothing to stash: err=%v\n%s", err, buf)
	}

	testutil.WriteFile(t, dir, "README.md", "work in progress\n")
	testutil.WriteFile(t, dir, "untracked.txt", "u\n")
	p, buf = printer()
	if err := Push(ctx, repo, "half done", false, p); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Run(t, dir, "status", "--porcelain"); got != "?? untracked.txt" {
		t.Errorf("tracked work should be stashed, untracked left: %q", got)
	}
	if !strings.Contains(buf.String(), "untracked file") {
		t.Errorf("should mention the untracked file:\n%s", buf)
	}

	p, buf = printer()
	if err := List(ctx, repo, false, p); err != nil || !strings.Contains(buf.String(), "half done") || !strings.Contains(buf.String(), "stash@{0}") {
		t.Fatalf("list: err=%v\n%s", err, buf)
	}

	p, _ = printer()
	if err := Pop(ctx, repo, "stash@{0}", p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "README.md")); string(b) != "work in progress\n" {
		t.Errorf("README.md = %q", b)
	}
	if err := Pop(ctx, repo, "stash@{0}", p); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("pop of a missing stash: err = %v, want usage error", err)
	}

	testutil.Run(t, dir, "stash", "push", "--include-untracked", "-m", "second")
	no := func(string) (bool, error) { return false, nil }
	yes := func(string) (bool, error) { return true, nil }
	if err := Drop(ctx, repo, "stash@{0}", no, p); err == nil {
		t.Error("declined drop must fail and delete nothing")
	}
	if err := Drop(ctx, repo, "stash@{0}", yes, p); err != nil {
		t.Fatal(err)
	}
	if list, _ := repo.StashList(ctx); len(list) != 0 {
		t.Errorf("stashes left: %v", list)
	}
}

func TestUntrackedOnlyAndFlag(t *testing.T) {
	repo, dir := setup(t)
	ctx := context.Background()
	testutil.WriteFile(t, dir, "untracked.txt", "u\n")
	p, buf := printer()
	if err := Push(ctx, repo, "", false, p); err != nil || !strings.Contains(buf.String(), "--untracked") {
		t.Fatalf("untracked only: err=%v\n%s", err, buf)
	}
	if err := Push(ctx, repo, "", true, p); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Run(t, dir, "status", "--porcelain"); got != "" {
		t.Errorf("work tree not clean: %q", got)
	}
}

func TestRefusesDuringAMergeAndConflictOnPop(t *testing.T) {
	repo, dir := setup(t)
	ctx := context.Background()
	p, _ := printer()
	testutil.WriteFile(t, dir, "README.md", "stashed\n")
	if err := Push(ctx, repo, "", false, p); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, dir, "README.md", "committed meanwhile\n")
	testutil.Commit(t, dir, "feat: meanwhile")
	if err := Pop(ctx, repo, "stash@{0}", p); apperr.CodeOf(err) != apperr.CodeGeneral {
		t.Fatalf("conflicting pop: err = %v, want a general error", err)
	}
	if list, _ := repo.StashList(ctx); len(list) != 1 {
		t.Errorf("a stash that did not apply must be kept, have %d", len(list))
	}
}

func TestListJSON(t *testing.T) {
	repo, dir := setup(t)
	ctx := context.Background()
	var out bytes.Buffer
	p := output.Printer{Out: &out, Err: &out}
	if err := List(ctx, repo, true, p); err != nil || strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("empty: err=%v %q", err, out.String())
	}

	testutil.WriteFile(t, dir, "README.md", "work in progress\n")
	if err := Push(ctx, repo, "half done", false, p); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := List(ctx, repo, true, p); err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Ref     string    `json:"ref"`
		Date    time.Time `json:"date"`
		Message string    `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, &out)
	}
	if len(got) != 1 || got[0].Ref != "stash@{0}" || !strings.Contains(got[0].Message, "half done") || time.Since(got[0].Date) > time.Minute || got[0].Date.IsZero() {
		t.Fatalf("stashes = %+v", got)
	}
}
