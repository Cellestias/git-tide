package branches

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/github"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/testutil"
)

type fakeGH struct {
	protected    map[string]bool
	prs          []github.PullRequest
	protectedErr error
	prsErr       error
}

func (f fakeGH) ListProtectedBranches(context.Context, github.Repo) (map[string]bool, bool, error) {
	return f.protected, false, f.protectedErr
}
func (f fakeGH) ListOpenPullRequests(context.Context, github.Repo) ([]github.PullRequest, bool, error) {
	return f.prs, false, f.prsErr
}

type fixture struct {
	repo *repository.Repository
	dir  string
	bare string
}

// setup: main (pushed) plus
//
//	feature/new   2 commits ahead of main, pushed, upstream in sync
//	feature/old   1 commit ahead, never pushed, author "Old Dev" 90 days ago
//	fix/done      fully merged into main (main moved on)
func setup(t *testing.T) fixture {
	t.Helper()
	dir, bare := testutil.NewRepoWithRemote(t)
	repo := repository.New(executor.Exec{}, dir)

	testutil.Run(t, dir, "checkout", "-q", "-b", "fix/done")
	testutil.Run(t, dir, "checkout", "-q", "main")
	testutil.WriteFile(t, dir, "m.txt", "m\n")
	testutil.Commit(t, dir, "feat: main moves on")
	testutil.Run(t, dir, "push", "-q", "origin", "main")

	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/old", "main")
	testutil.WriteFile(t, dir, "old.txt", "o\n")
	testutil.CommitAs(t, dir, "Old Dev", "old@example.invalid", "2000-01-01T00:00:00Z", "feat: old")

	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/new", "main")
	for _, f := range []string{"n1", "n2"} {
		testutil.WriteFile(t, dir, f, f)
		testutil.Commit(t, dir, "feat: "+f)
	}
	testutil.Run(t, dir, "push", "-q", "-u", "origin", "feature/new")
	return fixture{repo, dir, bare}
}

func run(t *testing.T, f fixture, cfg domain.Config, gh GitHub, opts Options) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := Run(context.Background(), f.repo, cfg, gh, opts, output.Printer{Out: &out, Err: &errOut})
	return out.String(), errOut.String(), err
}

// rowFor returns the table line that starts with the branch name (after the marker).
func rowFor(out, name string) string {
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 0 && (f[0] == name || (f[0] == "*" && len(f) > 1 && f[1] == name)) {
			return l
		}
	}
	return ""
}

func cfgFor() domain.Config {
	c := domain.Default()
	c.Branches.Protected = []string{"main"}
	return c
}

func TestLocalTable(t *testing.T) {
	f := setup(t)
	out, _, err := run(t, f, cfgFor(), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "BRANCH") || strings.Contains(out, "GITHUB") {
		t.Errorf("header wrong:\n%s", out)
	}
	for name, wants := range map[string][]string{
		"feature/new": {"in sync", "main ↑2", "-"},
		"feature/old": {"Old Dev", "none", "main ↑1", "years ago"},
		"fix/done":    {"none", "main ↓1", "merged"},
		"main":        {"in sync", "protected"},
	} {
		line := rowFor(out, name)
		if line == "" {
			t.Fatalf("no row for %s:\n%s", name, out)
		}
		for _, w := range wants {
			if !strings.Contains(line, w) {
				t.Errorf("%s row missing %q:\n%s", name, w, line)
			}
		}
	}
	if !strings.HasPrefix(rowFor(out, "feature/new"), "* ") {
		t.Error("current branch not marked")
	}
	if strings.Contains(out, "remote-only") {
		t.Error("remote-only rows without --remote")
	}
}

func TestSort(t *testing.T) {
	f := setup(t)
	order := func(sort string) []string {
		out, _, err := run(t, f, cfgFor(), nil, Options{Sort: sort})
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, l := range strings.Split(strings.TrimSpace(out), "\n")[1:] {
			fs := strings.Fields(l)
			if fs[0] == "*" {
				fs = fs[1:]
			}
			names = append(names, fs[0])
		}
		return names
	}
	if got := order(SortName); strings.Join(got, ",") != "feature/new,feature/old,fix/done,main" {
		t.Errorf("name order = %v", got)
	}
	if got := order(SortAhead); got[0] != "feature/new" || got[1] != "feature/old" {
		t.Errorf("ahead order = %v", got)
	}
	if got := order(SortDate); got[len(got)-1] != "feature/old" {
		t.Errorf("date order must put the 2000 commit last: %v", got)
	}
	if _, _, err := run(t, f, cfgFor(), nil, Options{Sort: "bogus"}); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("bogus sort: %v", err)
	}
}

func TestFilters(t *testing.T) {
	f := setup(t)
	names := func(opts Options) string {
		out, _, err := run(t, f, cfgFor(), nil, opts)
		if err != nil {
			t.Fatal(err)
		}
		var ns []string
		for _, l := range strings.Split(strings.TrimSpace(out), "\n")[1:] {
			if l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "*")); l != "" {
				ns = append(ns, strings.Fields(l)[0])
			}
		}
		return strings.Join(ns, ",")
	}
	if got := names(Options{Merged: true, Sort: SortName}); got != "fix/done" {
		t.Errorf("--merged = %q", got)
	}
	if got := names(Options{StaleDays: 30, Sort: SortName}); got != "feature/old" {
		t.Errorf("--stale = %q", got)
	}
	testutil.Run(t, f.dir, "config", "user.email", "old@example.invalid")
	if got := names(Options{Mine: true}); got != "feature/old" {
		t.Errorf("--mine = %q", got)
	}
	if _, _, err := run(t, f, cfgFor(), nil, Options{StaleDays: -1}); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("negative stale: %v", err)
	}
}

func TestMineNeedsEmail(t *testing.T) {
	f := setup(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	_, _, err := run(t, f, cfgFor(), nil, Options{Mine: true})
	if apperr.CodeOf(err) != apperr.CodeConfig {
		t.Errorf("err = %v", err)
	}
}

func TestRemoteOnlyAndGone(t *testing.T) {
	f := setup(t)
	// someone else pushes a branch; another branch is deleted on the remote
	testutil.Run(t, f.bare, "branch", "colleague/x", "main")
	testutil.Run(t, f.bare, "branch", "-q", "-D", "feature/new")

	out, errOut, err := run(t, f, cfgFor(), nil, Options{Remote: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "Fetching origin") {
		t.Errorf("stderr = %q", errOut)
	}
	if line := rowFor(out, "colleague/x"); !strings.Contains(line, "remote-only") || !strings.Contains(line, "-") {
		t.Errorf("remote-only row: %q", line)
	}
	if line := rowFor(out, "feature/new"); !strings.Contains(line, "gone") {
		t.Errorf("gone row: %q", line)
	}
}

func TestRemoteErrors(t *testing.T) {
	f := setup(t)
	cfg := cfgFor()
	cfg.Remote = "nope"
	if _, _, err := run(t, f, cfg, nil, Options{Remote: true}); apperr.CodeOf(err) != apperr.CodeConfig {
		t.Errorf("missing remote: %v", err)
	}
	testutil.Run(t, f.dir, "remote", "set-url", "origin", "/nonexistent/remote")
	if _, _, err := run(t, f, cfgFor(), nil, Options{Remote: true}); apperr.CodeOf(err) != apperr.CodeExternal {
		t.Errorf("unreachable: %v", err)
	}
	// without --remote nothing touches the network
	if _, _, err := run(t, f, cfgFor(), nil, Options{}); err != nil {
		t.Errorf("local listing must work offline: %v", err)
	}
}

func githubRemote(t *testing.T, f fixture) {
	t.Helper()
	testutil.Run(t, f.dir, "remote", "set-url", "origin", "https://github.com/acme/tool.git")
	testutil.Run(t, f.dir, "config", "url."+f.bare+".insteadOf", "https://github.com/acme/tool.git")
}

func TestGitHubColumn(t *testing.T) {
	f := setup(t)
	githubRemote(t, f)
	gh := fakeGH{
		protected: map[string]bool{"main": true, "feature/new": false},
		prs: []github.PullRequest{
			{Number: 12, HeadRef: "feature/new", HeadRepo: "acme/tool"},
			{Number: 99, HeadRef: "feature/old", HeadRepo: "someone/fork"}, // fork: ignored
		},
	}
	out, _, err := run(t, f, cfgFor(), gh, Options{GitHub: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "GITHUB") {
		t.Fatalf("no GITHUB column:\n%s", out)
	}
	if l := rowFor(out, "main"); !strings.HasSuffix(strings.TrimSpace(l), "protected") {
		t.Errorf("main: %q", l)
	}
	if l := rowFor(out, "feature/new"); !strings.Contains(l, "PR #12") {
		t.Errorf("feature/new: %q", l)
	}
	if l := rowFor(out, "feature/old"); strings.Contains(l, "PR") {
		t.Errorf("fork PR leaked into feature/old: %q", l)
	}
}

func TestGitHubDegradesToQuestionMarks(t *testing.T) {
	f := setup(t)
	githubRemote(t, f)
	gh := fakeGH{protectedErr: github.ErrNotFound, prsErr: errors.New("boom\x1b[31m")}
	out, errOut, err := run(t, f, cfgFor(), gh, Options{GitHub: true})
	if err != nil {
		t.Fatalf("GitHub failure must not fail the command: %v", err)
	}
	if l := rowFor(out, "main"); !strings.HasSuffix(strings.TrimSpace(l), "?") {
		t.Errorf("main: %q", l)
	}
	if !strings.Contains(errOut, "cannot read branch protection") || strings.Contains(errOut, "\x1b") {
		t.Errorf("stderr = %q", errOut)
	}

	// a non-GitHub remote also degrades
	testutil.Run(t, f.dir, "remote", "set-url", "origin", "https://gitlab.com/a/b.git")
	out, errOut, err = run(t, f, cfgFor(), gh, Options{GitHub: true})
	if err != nil || !strings.Contains(errOut, "GitHub column unavailable") || !strings.Contains(out, "GITHUB") {
		t.Errorf("err=%v stderr=%q", err, errOut)
	}
}

func TestAgo(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "just now"},
		{time.Minute, "1 minute ago"},
		{5 * time.Hour, "5 hours ago"},
		{24 * time.Hour, "1 day ago"},
		{45 * 24 * time.Hour, "1 month ago"},
		{800 * 24 * time.Hour, "2 years ago"},
	}
	for _, tt := range tests {
		if got := ago(now, now.Add(-tt.d)); got != tt.want {
			t.Errorf("ago(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
	if ago(now, time.Time{}) != "-" {
		t.Error("zero time")
	}
}

func TestOutputStripsControlCharacters(t *testing.T) {
	f := setup(t)
	testutil.Run(t, f.dir, "checkout", "-q", "-b", "weird")
	testutil.WriteFile(t, f.dir, "w", "w")
	testutil.CommitAs(t, f.dir, "Eve\x1b[31m", "eve@example.invalid", "2026-01-01T00:00:00Z", "feat: w")
	out, _, err := run(t, f, cfgFor(), nil, Options{})
	if err != nil || strings.Contains(out, "\x1b") {
		t.Errorf("err=%v out has escape: %q", err, out)
	}
}
