package review

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
	"github.com/cellestias/git-tide/internal/github"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/testutil"
)

type fakePRs struct {
	pr  github.PullRequest
	err error
	got github.Repo
}

func (f *fakePRs) GetPullRequest(_ context.Context, r github.Repo, n int) (github.PullRequest, error) {
	f.got = r
	return f.pr, f.err
}

// setup returns a clone whose "origin" is a local bare repo exposing refs/pull/1/head.
// The remote URL is replaced by a GitHub-style URL via url.insteadOf so ParseRemoteURL
// succeeds while Git still talks to the local bare repository.
func setup(t *testing.T) (*repository.Repository, string, string) {
	t.Helper()
	dir, bare := testutil.NewRepoWithRemote(t)

	pr := t.TempDir()
	testutil.Run(t, pr, "clone", "-q", bare, ".")
	testutil.Run(t, pr, "checkout", "-q", "-b", "feature/pr")
	testutil.WriteFile(t, pr, "pr.txt", "from pr\n")
	testutil.Commit(t, pr, "feat: pr change")
	testutil.Run(t, pr, "push", "-q", "origin", "HEAD:refs/pull/1/head")

	testutil.Run(t, dir, "remote", "set-url", "origin", "https://github.com/acme/tool.git")
	testutil.Run(t, dir, "config", "url."+bare+".insteadOf", "https://github.com/acme/tool.git")
	return repository.New(executor.Exec{}, dir), dir, pr
}

func fake() *fakePRs {
	return &fakePRs{pr: github.PullRequest{Number: 1, Title: "Add pr\x1b[31m", State: "open", Author: "ana", HeadRef: "feature/pr", BaseRef: "main"}}
}

func run(repo *repository.Repository, prs PullRequests, opts Options) (string, error) {
	var out, errOut bytes.Buffer
	err := Run(context.Background(), repo, domain.Default(), prs, opts, output.Printer{Out: &out, Err: &errOut})
	return out.String(), err
}

func TestReviewCreatesBranch(t *testing.T) {
	repo, dir, _ := setup(t)
	prs := fake()
	out, err := run(repo, prs, Options{Ref: "#1"})
	if err != nil {
		t.Fatal(err)
	}
	if prs.got != (github.Repo{Owner: "acme", Name: "tool"}) {
		t.Errorf("repo = %+v", prs.got)
	}
	if testutil.Run(t, dir, "rev-parse", "--abbrev-ref", "HEAD") != "tide/review/pr-1" {
		t.Error("not on review branch")
	}
	if _, err := os.Stat(filepath.Join(dir, "pr.txt")); err != nil {
		t.Error("PR content missing")
	}
	for _, want := range []string{"tide/review/pr-1", "Add pr", "ana", "feature/pr", "main"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Error("control characters from PR title reached the terminal")
	}
}

func TestReviewUpdatesExistingBranchFastForward(t *testing.T) {
	repo, dir, pr := setup(t)
	if _, err := run(repo, fake(), Options{Ref: "1"}); err != nil {
		t.Fatal(err)
	}
	testutil.Run(t, dir, "checkout", "-q", "main")

	testutil.WriteFile(t, pr, "more.txt", "m\n")
	testutil.Commit(t, pr, "feat: more")
	testutil.Run(t, pr, "push", "-q", "origin", "HEAD:refs/pull/1/head")

	if _, err := run(repo, fake(), Options{Ref: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "more.txt")); err != nil {
		t.Error("review branch not updated")
	}
}

func TestReviewRefusesToDiscardLocalCommitsUnlessReset(t *testing.T) {
	repo, dir, _ := setup(t)
	if _, err := run(repo, fake(), Options{Ref: "1"}); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, dir, "mine.txt", "mine\n")
	testutil.Commit(t, dir, "feat: my review fix")
	mine := testutil.Run(t, dir, "rev-parse", "HEAD")

	_, err := run(repo, fake(), Options{Ref: "1"})
	if apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Fatalf("err = %v", err)
	}
	if testutil.Run(t, dir, "rev-parse", "HEAD") != mine {
		t.Error("local commit lost")
	}

	if _, err := run(repo, fake(), Options{Ref: "1", Reset: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mine.txt")); err == nil {
		t.Error("--reset should drop local commit")
	}
}

func TestReviewErrors(t *testing.T) {
	repo, dir, _ := setup(t)

	if _, err := run(repo, fake(), Options{Ref: "abc"}); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("bad ref: %v", err)
	}
	if _, err := run(repo, fake(), Options{Ref: "https://github.com/other/repo/pull/1"}); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("foreign repo: %v", err)
	}
	nf := &fakePRs{err: github.ErrNotFound}
	if _, err := run(repo, nf, Options{Ref: "1"}); apperr.CodeOf(err) != apperr.CodeExternal {
		t.Errorf("not found: %v", err)
	}
	if _, err := run(repo, fake(), Options{Ref: "404"}); apperr.CodeOf(err) != apperr.CodeExternal {
		t.Errorf("missing pull ref: %v", err)
	}

	testutil.WriteFile(t, dir, "README.md", "dirty\n")
	if _, err := run(repo, fake(), Options{Ref: "1"}); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("dirty: %v", err)
	}

	testutil.Run(t, dir, "checkout", "-q", "--", ".")
	testutil.Run(t, dir, "remote", "set-url", "origin", "https://gitlab.com/a/b.git")
	if _, err := run(repo, fake(), Options{Ref: "1"}); apperr.CodeOf(err) != apperr.CodeConfig {
		t.Errorf("non-github: %v", err)
	}
}
