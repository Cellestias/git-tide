package pr

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/github"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
	"github.com/cellestias/git-tide/internal/testutil"
)

// fakeHost is a hosting service with canned answers. The embedded GitHub client
// supplies parsing; everything that would hit the network is overridden.
type fakeHost struct {
	*github.Client
	token     bool
	prs       []provider.PullRequest
	single    provider.PullRequest
	getErr    error
	listErr   error
	review    provider.Review
	reviewErr error
	files     []provider.ChangedFile
	filesErr  error
	commits   []provider.CommitSummary
	user      string

	gotList provider.ListOptions
	lists   int
}

func (f *fakeHost) HasToken() bool { return f.token }
func (f *fakeHost) CurrentUser(context.Context) (string, error) {
	return f.user, nil
}
func (f *fakeHost) ListPullRequests(_ context.Context, _ provider.Repo, o provider.ListOptions) ([]provider.PullRequest, bool, error) {
	f.gotList = o
	f.lists++
	return f.prs, false, f.listErr
}
func (f *fakeHost) GetPullRequest(context.Context, provider.Repo, int) (provider.PullRequest, error) {
	return f.single, f.getErr
}
func (f *fakeHost) Reviews(context.Context, provider.Repo, int) (provider.Review, error) {
	return f.review, f.reviewErr
}
func (f *fakeHost) ChangedFiles(context.Context, provider.Repo, int) ([]provider.ChangedFile, bool, error) {
	return f.files, false, f.filesErr
}
func (f *fakeHost) Commits(context.Context, provider.Repo, int) ([]provider.CommitSummary, bool, error) {
	return f.commits, false, nil
}

func sample() provider.PullRequest {
	return provider.PullRequest{
		Number: 42, Title: "Add auth\x1b[31m", Body: "Implements login.\n\x1b]0;evil\x07Second line", State: provider.StateOpen, Draft: true,
		URL: "https://github.com/acme/tool/pull/42", Author: "ana", Assignees: []string{"bia"}, Reviewers: []string{"caio"},
		HeadRef: "feature/login-page", BaseRef: "main", HeadRepo: "acme/tool",
		CreatedAt: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 11, 2, 11, 30, 0, 0, time.UTC),
	}
}

func runWith(t *testing.T, fn func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error) (string, string, error) {
	t.Helper()
	repo, _, _ := setup(t)
	var out, errOut bytes.Buffer
	err := fn(context.Background(), repo, domain.Default(), output.Printer{Out: &out, Err: &errOut})
	return out.String(), errOut.String(), err
}

func TestListTableAndFilters(t *testing.T) {
	h := &fakeHost{prs: []provider.PullRequest{sample(), {Number: 41, Title: "Old", State: provider.StateMerged, Author: "bia", HeadRef: "f/old", BaseRef: "develop"}}}
	out, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return List(ctx, repo, cfg, h, ListOptions{State: "all", Author: "ana", Assignee: "bia", Limit: 5}, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ID", "TITLE", "AUTHOR", "STATE", "SOURCE", "TARGET", "#42", "Add auth", "open (draft)", "feature/login-page", "#41", "merged", "develop"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Error("control characters reached the terminal")
	}
	want := provider.ListOptions{State: "all", Author: "ana", Assignee: "bia", Limit: 5}
	if h.gotList != want {
		t.Errorf("filters = %+v, want %+v", h.gotList, want)
	}
}

func TestListDefaultsAndMeResolution(t *testing.T) {
	h := &fakeHost{token: true, user: "ana"}
	_, errOut, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return List(ctx, repo, cfg, h, ListOptions{Author: "me", Assignee: "ME"}, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.gotList.State != provider.StateOpen || h.gotList.Limit != DefaultListLimit || h.gotList.Author != "ana" || h.gotList.Assignee != "ana" {
		t.Errorf("filters = %+v", h.gotList)
	}
	if !strings.Contains(errOut, "No pull requests match") {
		t.Errorf("empty list not reported:\n%s", errOut)
	}
}

func TestListValidation(t *testing.T) {
	cases := []struct {
		name string
		opts ListOptions
		host *fakeHost
		code int
	}{
		{"bad state", ListOptions{State: "bogus"}, &fakeHost{}, apperr.CodeUsage},
		{"negative limit", ListOptions{Limit: -1}, &fakeHost{}, apperr.CodeUsage},
		{"huge limit", ListOptions{Limit: MaxListLimit + 1}, &fakeHost{}, apperr.CodeUsage},
		{"bad login", ListOptions{Author: "a b;c"}, &fakeHost{}, apperr.CodeUsage},
		{"me without token", ListOptions{Author: "me"}, &fakeHost{}, apperr.CodeConfig},
		{"not found", ListOptions{}, &fakeHost{listErr: provider.ErrNotFound}, apperr.CodeExternal},
		{"api error", ListOptions{}, &fakeHost{listErr: errors.New("boom")}, apperr.CodeExternal},
	}
	for _, tc := range cases {
		_, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
			return List(ctx, repo, cfg, tc.host, tc.opts, p)
		})
		if apperr.CodeOf(err) != tc.code {
			t.Errorf("%s: err = %v (code %v), want code %v", tc.name, err, apperr.CodeOf(err), tc.code)
		}
		if tc.host.lists != 0 && tc.code == apperr.CodeUsage {
			t.Errorf("%s: the provider was queried despite the bad input", tc.name)
		}
	}
}

func TestViewShowsEverything(t *testing.T) {
	h := &fakeHost{
		single: sample(),
		review: provider.Review{State: provider.ReviewChangesRequested, ChangesRequested: []string{"dani"}},
		files: []provider.ChangedFile{
			{Path: "a.go", Status: "added", Additions: 10, HasStats: true},
			{Path: "b.go", OldPath: "old.go", Status: "renamed", Additions: 1, Deletions: 2, HasStats: true},
		},
		commits: []provider.CommitSummary{{SHA: "abcdef123456", Subject: "feat: login"}},
	}
	out, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return View(ctx, repo, cfg, h, "42", false, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"#42 Add auth", "pull/42", "open (draft)", "ana", "bia", "feature/login-page → main",
		"2026-11-01 10:00 UTC", "2026-11-02 11:30 UTC", "changes requested by dani", "waiting on caio",
		"Description", "Implements login.", "Second line",
		"Changed files (2, +11 −2)", "A  a.go  +10 −0", "R  old.go → b.go  +1 −2",
		"Commits (1)", "abcdef1  feat: login",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") || strings.Contains(out, "\x07") {
		t.Errorf("control characters reached the terminal:\n%q", out)
	}
	if strings.Contains(out, "(fork") {
		t.Error("same-repository request flagged as a fork")
	}
}

func TestViewDegradesAndFlagsForks(t *testing.T) {
	x := sample()
	x.HeadRepo = "someone/tool"
	h := &fakeHost{single: x, filesErr: errors.New("files down"), reviewErr: errors.New("reviews down")}
	out, errOut, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return View(ctx, repo, cfg, h, "#42", false, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(fork: someone/tool)") {
		t.Errorf("fork not flagged:\n%s", out)
	}
	if !strings.Contains(errOut, "cannot read changed files") || !strings.Contains(errOut, "cannot read reviews") {
		t.Errorf("failures not reported:\n%s", errOut)
	}
	if strings.Contains(out, "review:") || strings.Contains(out, "Changed files") {
		t.Errorf("sections shown although their data failed:\n%s", out)
	}
}

func TestViewErrors(t *testing.T) {
	run := func(h *fakeHost, ref string) error {
		_, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
			return View(ctx, repo, cfg, h, ref, false, p)
		})
		return err
	}
	if c := apperr.CodeOf(run(&fakeHost{}, "abc")); c != apperr.CodeUsage {
		t.Errorf("bad ref: %v", c)
	}
	if c := apperr.CodeOf(run(&fakeHost{}, "https://github.com/other/repo/pull/1")); c != apperr.CodeUsage {
		t.Errorf("foreign repo: %v", c)
	}
	if c := apperr.CodeOf(run(&fakeHost{getErr: provider.ErrNotFound}, "42")); c != apperr.CodeExternal {
		t.Errorf("not found: %v", c)
	}
}

func TestStatusPrefersOpenAndSameRepository(t *testing.T) {
	merged := sample()
	merged.Number, merged.State, merged.Draft = 30, provider.StateMerged, false
	fork := sample()
	fork.Number, fork.HeadRepo, fork.Title = 99, "someone/tool", "From a fork"
	open := sample()
	open.Number, open.Title, open.Draft = 31, "Current work", false
	h := &fakeHost{prs: []provider.PullRequest{fork, merged, open}, review: provider.Review{State: provider.ReviewApproved, Approvals: []string{"dani"}}}
	out, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return Status(ctx, repo, cfg, h, false, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"#31 Current work", "state:     open", "feature/login-page → main", "approved by dani", "requested: caio", "pull/42"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "#99") || strings.Contains(out, "#30") {
		t.Errorf("wrong request picked:\n%s", out)
	}
	if h.gotList.Head != "feature/login-page" || h.gotList.State != provider.StateAll {
		t.Errorf("query = %+v", h.gotList)
	}
}

func TestStatusWithoutRequestAndDetachedHead(t *testing.T) {
	out, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return Status(ctx, repo, cfg, &fakeHost{}, false, p)
	})
	if err != nil || !strings.Contains(out, "No pull request for branch feature/login-page") || !strings.Contains(out, "gtide pr") {
		t.Errorf("no request: err=%v\n%s", err, out)
	}

	repo, dir, _ := setup(t)
	testutil.Run(t, dir, "checkout", "-q", "--detach")
	var o, e bytes.Buffer
	err = Status(context.Background(), repo, domain.Default(), &fakeHost{}, false, output.Printer{Out: &o, Err: &e})
	if apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("detached: %v", err)
	}
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// With colour on, the text is the plain text plus codes, and the states carry
// their colours.
func TestColouredOutputKeepsTheText(t *testing.T) {
	merged := sample()
	merged.Number, merged.State, merged.Draft = 41, provider.StateMerged, false
	closed := sample()
	closed.Number, closed.State, closed.Draft = 40, provider.StateClosed, false
	h := &fakeHost{
		prs:     []provider.PullRequest{sample(), merged, closed},
		single:  sample(),
		review:  provider.Review{State: provider.ReviewChangesRequested, ChangesRequested: []string{"dani"}},
		files:   []provider.ChangedFile{{Path: "a.go", Status: "added", Additions: 3, HasStats: true}},
		commits: []provider.CommitSummary{{SHA: "abcdef123456", Subject: "feat: login"}},
	}
	both := func(fn func(context.Context, *repository.Repository, domain.Config, output.Printer) error) (plain, colour string) {
		for _, on := range []bool{false, true} {
			repo, _, _ := setup(t)
			var out, errOut bytes.Buffer
			if err := fn(context.Background(), repo, domain.Default(), output.Printer{Out: &out, Err: &errOut, ColorOut: on, ColorErr: on}); err != nil {
				t.Fatal(err)
			}
			if on {
				colour = out.String()
			} else {
				plain = out.String()
			}
		}
		return plain, colour
	}

	plain, colour := both(func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return List(ctx, repo, cfg, h, ListOptions{State: "all"}, p)
	})
	if ansiRe.ReplaceAllString(colour, "") != plain {
		t.Errorf("colour changed the list layout:\n%q\nvs\n%q", ansiRe.ReplaceAllString(colour, ""), plain)
	}
	for _, want := range []string{"\x1b[2mopen (draft)", "\x1b[35mmerged", "\x1b[31mclosed"} {
		if !strings.Contains(colour, want) {
			t.Errorf("list lacks %q:\n%q", want, colour)
		}
	}

	plain, colour = both(func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return View(ctx, repo, cfg, h, "42", false, p)
	})
	if ansiRe.ReplaceAllString(colour, "") != plain {
		t.Errorf("colour changed the view text:\n%q\nvs\n%q", ansiRe.ReplaceAllString(colour, ""), plain)
	}
	for _, want := range []string{"\x1b[31mchanges requested by dani", "\x1b[32mA\x1b[0m", "\x1b[32m+3\x1b[0m", "\x1b[1mCommits"} {
		if !strings.Contains(colour, want) {
			t.Errorf("view lacks %q:\n%q", want, colour)
		}
	}
}
