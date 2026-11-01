package pr

import (
	"bytes"
	"context"
	"errors"
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

type fakeCreator struct {
	got   github.NewPullRequest
	repo  github.Repo
	calls int
	err   error
}

func (f *fakeCreator) CreatePullRequest(_ context.Context, r github.Repo, n github.NewPullRequest) (github.PullRequest, error) {
	f.calls++
	f.repo, f.got = r, n
	if f.err != nil {
		return github.PullRequest{}, f.err
	}
	return github.PullRequest{Number: 7, URL: "https://github.com/acme/tool/pull/7"}, nil
}

// setup returns a clone (GitHub-shaped remote URL rewritten to a local bare
// repo) on feature/login-page with one commit ahead of main, plus the bare repo.
func setup(t *testing.T) (*repository.Repository, string, string) {
	t.Helper()
	dir, bare := testutil.NewRepoWithRemote(t)
	testutil.Run(t, dir, "remote", "set-url", "origin", "https://github.com/acme/tool.git")
	testutil.Run(t, dir, "config", "url."+bare+".insteadOf", "https://github.com/acme/tool.git")
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/login-page")
	testutil.WriteFile(t, dir, "login.txt", "l\n")
	testutil.Commit(t, dir, "feat: add login page")
	return repository.New(executor.Exec{}, dir), dir, bare
}

func run(repo *repository.Repository, c Creator, opts Options) (string, string, error) {
	var out, errOut bytes.Buffer
	err := Run(context.Background(), repo, domain.Default(), c, opts, output.Printer{Out: &out, Err: &errOut})
	return out.String(), errOut.String(), err
}

func TestPushesAndCreates(t *testing.T) {
	repo, dir, bare := setup(t)
	c := &fakeCreator{}
	out, _, err := run(repo, c, Options{Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	if c.repo != (github.Repo{Owner: "acme", Name: "tool"}) {
		t.Errorf("repo = %+v", c.repo)
	}
	want := github.NewPullRequest{Title: "feat: add login page", Body: "- feat: add login page\n", Head: "feature/login-page", Base: "main", Draft: true}
	if c.got != want {
		t.Errorf("request = %+v, want %+v", c.got, want)
	}
	if !strings.Contains(out, "pull/7") {
		t.Errorf("url missing:\n%s", out)
	}
	if testutil.Run(t, bare, "rev-parse", "refs/heads/feature/login-page") != testutil.Run(t, dir, "rev-parse", "HEAD") {
		t.Error("branch not pushed")
	}
	if testutil.Run(t, dir, "config", "branch.feature/login-page.remote") != "origin" {
		t.Error("upstream not set")
	}
}

func TestMultipleCommitsAndOverrides(t *testing.T) {
	repo, dir, _ := setup(t)
	testutil.WriteFile(t, dir, "b.txt", "b\n")
	testutil.Commit(t, dir, "fix: handle empty password")

	c := &fakeCreator{}
	if _, _, err := run(repo, c, Options{}); err != nil {
		t.Fatal(err)
	}
	if c.got.Title != "Login page" || c.got.Body != "- feat: add login page\n- fix: handle empty password\n" {
		t.Errorf("request = %+v", c.got)
	}

	c = &fakeCreator{}
	if _, _, err := run(repo, c, Options{Title: "Mine", Body: "Custom", Base: "main"}); err != nil {
		t.Fatal(err)
	}
	if c.got.Title != "Mine" || c.got.Body != "Custom" {
		t.Errorf("overrides ignored: %+v", c.got)
	}
}

func TestConfirmDecline(t *testing.T) {
	repo, _, bare := setup(t)
	c := &fakeCreator{}
	var asked string
	_, _, err := run(repo, c, Options{Confirm: func(p string) (bool, error) { asked = p; return false, nil }})
	if err == nil || !strings.Contains(err.Error(), "nothing was pushed") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(asked, "feature/login-page") || c.calls != 0 {
		t.Errorf("asked=%q calls=%d", asked, c.calls)
	}
	if out, _ := (executor.Exec{}).Run(context.Background(), executor.Command{Args: []string{"branch", "--list", "feature/*"}, Dir: bare}); strings.TrimSpace(out.Stdout) != "" {
		t.Error("branch pushed despite refusal")
	}
	if _, _, err := run(repo, &fakeCreator{}, Options{Confirm: func(string) (bool, error) { return true, nil }}); err != nil {
		t.Fatalf("accepted: %v", err)
	}
}

func TestRefusals(t *testing.T) {
	repo, dir, _ := setup(t)
	c := &fakeCreator{}

	testutil.Run(t, dir, "checkout", "-q", "main")
	if _, _, err := run(repo, c, Options{}); apperr.CodeOf(err) != apperr.CodePolicy && apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("on main: %v", err)
	}
	testutil.Run(t, dir, "checkout", "-q", "-b", "empty")
	if _, _, err := run(repo, c, Options{}); apperr.CodeOf(err) != apperr.CodeUnsafeRepo || !strings.Contains(err.Error(), "no commits ahead") {
		t.Errorf("no commits: %v", err)
	}
	testutil.Run(t, dir, "checkout", "-q", "--detach")
	if _, _, err := run(repo, c, Options{}); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("detached: %v", err)
	}
	testutil.Run(t, dir, "checkout", "-q", "feature/login-page")
	if _, _, err := run(repo, c, Options{Base: "-bad"}); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("bad base: %v", err)
	}
	testutil.Run(t, dir, "remote", "set-url", "origin", "https://gitlab.com/a/b.git")
	if _, _, err := run(repo, c, Options{}); apperr.CodeOf(err) != apperr.CodeConfig {
		t.Errorf("non-github: %v", err)
	}
	if c.calls != 0 {
		t.Error("PR created despite refusals")
	}
}

func TestGitHubErrorsKeepBranchPushed(t *testing.T) {
	repo, _, _ := setup(t)
	_, _, err := run(repo, &fakeCreator{err: &github.ErrValidation{Message: "A pull request already exists\x1b[0m"}}, Options{})
	if apperr.CodeOf(err) != apperr.CodeExternal || strings.Contains(err.Error(), "\x1b") || !strings.Contains(apperr.HintOf(err), "pushed") {
		t.Fatalf("err = %v hint=%q", err, apperr.HintOf(err))
	}
	_, _, err = run(repo, &fakeCreator{err: github.ErrNotFound}, Options{})
	if apperr.CodeOf(err) != apperr.CodeExternal {
		t.Errorf("not found: %v", err)
	}
	_, _, err = run(repo, &fakeCreator{err: errors.New("boom")}, Options{})
	if apperr.CodeOf(err) != apperr.CodeExternal {
		t.Errorf("generic: %v", err)
	}
}

func TestDefaultTitle(t *testing.T) {
	tests := []struct {
		branch   string
		subjects []string
		want     string
	}{
		{"feature/x", []string{"feat: one"}, "feat: one"},
		{"feature/login-page", []string{"a", "b"}, "Login page"},
		{"fix/null_pointer-crash", []string{"a", "b"}, "Null pointer crash"},
		{"hotfix", []string{"a", "b"}, "Hotfix"},
		{"feature/", []string{"a", "b"}, "feature/"},
	}
	for _, tt := range tests {
		if got := DefaultTitle(tt.branch, tt.subjects); got != tt.want {
			t.Errorf("DefaultTitle(%q) = %q, want %q", tt.branch, got, tt.want)
		}
	}
	if got := DefaultTitle("b", []string{strings.Repeat("é", 500)}); len([]rune(got)) != maxTitle {
		t.Errorf("title length = %d", len([]rune(got)))
	}
}

func TestDefaultBodyCapsList(t *testing.T) {
	subjects := make([]string, 60)
	for i := range subjects {
		subjects[i] = "feat: c"
	}
	body := DefaultBody(subjects)
	if strings.Count(body, "\n") != maxBodyCommits+1 || !strings.Contains(body, "and 10 more") {
		t.Errorf("body = %q", body)
	}
}
