package pr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
)

type reqDoc struct {
	Number    int
	Title     string
	State     string
	Draft     bool
	URL       string
	Author    string
	Assignees []string
	Requested []string `json:"requested_reviewers"`
	Head      string
	Base      string
	HeadRepo  string `json:"head_repo"`
	CreatedAt string `json:"created_at"`
}

func TestListJSON(t *testing.T) {
	merged := sample()
	merged.Number, merged.State, merged.Draft, merged.Assignees, merged.Reviewers = 30, provider.StateMerged, false, nil, nil
	h := &fakeHost{prs: []provider.PullRequest{sample(), merged}}
	out, errOut, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return List(ctx, repo, cfg, h, ListOptions{State: "all", JSON: true}, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []reqDoc
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(got) != 2 {
		t.Fatalf("requests = %+v", got)
	}
	a := got[0]
	if a.Number != 42 || a.Title != "Add auth [31m" || a.State != "open" || !a.Draft || a.Author != "ana" ||
		a.Head != "feature/login-page" || a.Base != "main" || a.HeadRepo != "acme/tool" || len(a.Assignees) != 1 || a.Requested[0] != "caio" || a.CreatedAt != "2026-11-01T10:00:00Z" {
		t.Errorf("first = %+v", a)
	}
	if strings.ContainsAny(a.Title, "\x1b\x07") {
		t.Errorf("control characters in a title: %q", a.Title)
	}
	if got[1].State != "merged" || got[1].Assignees == nil || got[1].Requested == nil {
		t.Errorf("a request with no assignees has [] and not null: %+v", got[1])
	}
	if !strings.Contains(out, `"assignees": []`) {
		t.Errorf("empty lists must print as []:\n%s", out)
	}
	if strings.Contains(out, "ID  TITLE") || errOut != "" && strings.Contains(out, errOut) {
		t.Errorf("the table or a note reached stdout:\n%s", out)
	}
	if !strings.Contains(errOut, "Querying") {
		t.Errorf("notes stay on stderr: %q", errOut)
	}

	// nothing found is an empty array, not a sentence
	out, _, err = runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return List(ctx, repo, cfg, &fakeHost{}, ListOptions{JSON: true}, p)
	})
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Errorf("empty list: err=%v %q", err, out)
	}
}

func TestStatusJSON(t *testing.T) {
	open := sample()
	open.Number, open.Draft = 31, false
	h := &fakeHost{prs: []provider.PullRequest{open}, review: provider.Review{State: provider.ReviewApproved, Approvals: []string{"dani"}}}
	out, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return Status(ctx, repo, cfg, h, true, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Provider string
		Noun     string
		Repo     string
		Branch   string
		Request  *reqDoc
		Review   *struct {
			State            string
			Approvals        []string
			ChangesRequested []string `json:"changes_requested"`
		}
		Warnings []string
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.Provider != "github" || got.Noun != "pull request" || got.Repo != "acme/tool" || got.Branch != "feature/login-page" ||
		got.Request == nil || got.Request.Number != 31 || got.Review == nil || got.Review.State != "approved" || got.Review.Approvals[0] != "dani" || len(got.Review.ChangesRequested) != 0 || len(got.Warnings) != 0 {
		t.Errorf("status = %+v", got)
	}
	for state, want := range map[string]string{provider.ReviewPending: "pending", provider.ReviewChangesRequested: "changes_requested"} {
		h.review = provider.Review{State: state}
		out, _, _ := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
			return Status(ctx, repo, cfg, h, true, p)
		})
		if !strings.Contains(out, `"state": "`+want+`"`) {
			t.Errorf("review %q should be %q:\n%s", state, want, out)
		}
	}

	// no request: request and review are null, and it is still a document
	out, _, err = runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return Status(ctx, repo, cfg, &fakeHost{}, true, p)
	})
	if err != nil || !strings.Contains(out, `"request": null`) || !strings.Contains(out, `"review": null`) || !strings.Contains(out, `"branch": "feature/login-page"`) {
		t.Errorf("no request: err=%v\n%s", err, out)
	}

	// reviews that cannot be read: the request is still there, the review is null, and the warning is data
	h = &fakeHost{prs: []provider.PullRequest{open}, reviewErr: errors.New("reviews down\x1b[31m")}
	out, errOut, _ := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return Status(ctx, repo, cfg, h, true, p)
	})
	if !strings.Contains(out, `"review": null`) || !strings.Contains(out, "cannot read reviews: reviews down") || strings.ContainsAny(out, "\x1b") {
		t.Errorf("reviews down:\n%s", out)
	}
	if !strings.Contains(errOut, "cannot read reviews") {
		t.Errorf("the warning is also on stderr: %q", errOut)
	}
}

func TestViewJSON(t *testing.T) {
	x := sample()
	x.HeadRepo = "someone/tool"
	h := &fakeHost{
		single: x,
		review: provider.Review{State: provider.ReviewChangesRequested, ChangesRequested: []string{"dani"}},
		files: []provider.ChangedFile{
			{Path: "a.go", Status: "added", Additions: 10, HasStats: true},
			{Path: "b.go", OldPath: "old.go", Status: "renamed", Additions: 1, Deletions: 2, HasStats: true},
		},
		commits: []provider.CommitSummary{{SHA: "abcdef123456", Subject: "feat: login\x1b[31m"}},
	}
	out, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return View(ctx, repo, cfg, h, "42", true, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		reqDoc
		Provider      string
		Repo          string
		Fork          bool
		Body          string
		BodyTruncated bool `json:"body_truncated"`
		Review        struct{ State string }
		Files         []struct {
			Path    string
			OldPath string `json:"old_path"`
			Status  string
		}
		FilesTruncated   bool `json:"files_truncated"`
		Commits          []struct{ SHA, Subject string }
		CommitsTruncated bool `json:"commits_truncated"`
		Warnings         []string
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.Number != 42 || got.State != "open" || !got.Fork || got.Repo != "acme/tool" || got.HeadRepo != "someone/tool" || got.Review.State != "changes_requested" {
		t.Errorf("view = %+v", got)
	}
	if !strings.Contains(got.Body, "Implements login.\n") || !strings.Contains(got.Body, "Second line") || strings.ContainsAny(got.Body, "\x1b\x07") {
		t.Errorf("the body keeps its lines and loses its control characters: %q", got.Body)
	}
	if len(got.Files) != 2 || len(got.Commits) != 1 || got.Commits[0].SHA != "abcdef123456" || strings.ContainsAny(got.Commits[0].Subject, "\x1b") {
		t.Errorf("files=%d commits=%+v", len(got.Files), got.Commits)
	}
	for _, want := range []string{`"old_path": "old.go"`, `"additions": 10`, `"has_stats": true`, `"deletions": 2`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Changed files") || strings.Contains(out, "Description") {
		t.Errorf("the text form leaked into the JSON:\n%s", out)
	}
}

func TestViewJSONDegradesAndCutsAHugeBody(t *testing.T) {
	x := sample()
	x.Body = strings.Repeat("é", maxJSONBody+500)
	h := &fakeHost{single: x, filesErr: errors.New("files down"), reviewErr: errors.New("reviews down")}
	out, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return View(ctx, repo, cfg, h, "42", true, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Body          string
		BodyTruncated bool `json:"body_truncated"`
		Review        *struct{}
		Files         []any
		Commits       []any
		Warnings      []string
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if !got.BodyTruncated || len([]rune(got.Body)) != maxJSONBody {
		t.Errorf("body: truncated=%v len=%d, want a cut at %d runes (a rune is never split)", got.BodyTruncated, len([]rune(got.Body)), maxJSONBody)
	}
	if got.Review != nil || got.Files == nil || len(got.Files) != 0 || got.Commits == nil {
		t.Errorf("what failed is null (review) or [] (lists): review=%v files=%v commits=%v", got.Review, got.Files, got.Commits)
	}
	if len(got.Warnings) != 2 || !strings.Contains(strings.Join(got.Warnings, "|"), "cannot read changed files: files down") || !strings.Contains(strings.Join(got.Warnings, "|"), "cannot read reviews: reviews down") {
		t.Errorf("warnings = %v", got.Warnings)
	}
}
