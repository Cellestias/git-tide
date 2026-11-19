package pr

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
)

// reviewHost is a fakeHost that can also be reviewed.
type reviewHost struct {
	*fakeHost
	comments []provider.Comment
	diff     provider.Diff
	added    []provider.NewComment
	reviews  []provider.ReviewSubmission
}

func (h *reviewHost) Comments(context.Context, provider.Repo, int) ([]provider.Comment, bool, error) {
	return h.comments, false, nil
}
func (h *reviewHost) PullDiff(context.Context, provider.Repo, int) (provider.Diff, error) {
	return h.diff, nil
}
func (h *reviewHost) AddComment(_ context.Context, _ provider.Repo, _ int, c provider.NewComment) (provider.Comment, error) {
	h.added = append(h.added, c)
	return provider.Comment{ID: 99, URL: "https://github.com/acme/tool/pull/42#c99"}, nil
}
func (h *reviewHost) SubmitReview(_ context.Context, _ provider.Repo, _ int, r provider.ReviewSubmission) error {
	h.reviews = append(h.reviews, r)
	return nil
}

const patch = `diff --git a/a.go b/a.go
index 1..2 100644
--- a/a.go
+++ b/a.go
@@ -1,2 +1,3 @@
 keep
-old
+new
+more
diff --git a/new.txt b/new.txt
new file mode 100644
index 0..3
--- /dev/null
+++ b/new.txt
@@ -0,0 +1 @@
+hello
diff --git a/gone.txt b/gone.txt
deleted file mode 100644
index 3..0
--- a/gone.txt
+++ /dev/null
@@ -1 +0,0 @@
-bye
diff --git a/old name.go b/new name.go
similarity index 90%
rename from old name.go
rename to new name.go
index 1..2 100644
--- a/old name.go
+++ b/new name.go
@@ -1 +1 @@
-x
+y
diff --git a/img.png b/img.png
index 1..2 100644
Binary files a/img.png and b/img.png differ
`

func TestSplitPatchReadsEachFileWithItsStatus(t *testing.T) {
	files := splitPatch(patch)
	if len(files) != 5 {
		t.Fatalf("files = %+v", files)
	}
	want := []struct {
		path, old, status string
		binary            bool
		starts            string
	}{
		{"a.go", "", "modified", false, "@@ -1,2 +1,3 @@"},
		{"new.txt", "", "added", false, "@@ -0,0 +1 @@"},
		{"gone.txt", "", "deleted", false, "@@ -1 +0,0 @@"},
		{"new name.go", "old name.go", "renamed", false, "@@ -1 +1 @@"},
		{"img.png", "", "modified", true, ""},
	}
	for i, w := range want {
		f := files[i]
		if f.Path != w.path || f.OldPath != w.old || f.Status != w.status || f.Binary != w.binary || !strings.HasPrefix(f.Patch, w.starts) {
			t.Errorf("file %d = %+v, want %+v", i, f, w)
		}
	}
	if strings.Contains(files[0].Patch, "diff --git") || strings.Contains(files[0].Patch, "+++") {
		t.Errorf("the patch of a file is its hunks only: %q", files[0].Patch)
	}
	if files[4].Patch != "" {
		t.Errorf("a binary file has no hunks: %q", files[4].Patch)
	}
}

func TestSplitPatchQuotedPath(t *testing.T) {
	files := splitPatch("diff --git \"a/caf\\303\\251.txt\" \"b/caf\\303\\251.txt\"\nindex 1..2 100644\n--- \"a/caf\\303\\251.txt\"\n+++ \"b/caf\\303\\251.txt\"\n@@ -1 +1 @@\n-a\n+b\n")
	if len(files) != 1 || files[0].Path != "café.txt" {
		t.Errorf("files = %+v", files)
	}
}

func TestDiffJSONAndFileFilter(t *testing.T) {
	h := &reviewHost{fakeHost: &fakeHost{}, diff: provider.Diff{Patch: patch, HeadSHA: "deadbeef"}}
	out, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return Diff(ctx, repo, cfg, h, DiffOptions{Ref: "42", JSON: true}, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Number    int
		HeadSHA   string `json:"head_sha"`
		Truncated bool
		Files     []jsonDiffFile
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if doc.Number != 42 || doc.HeadSHA != "deadbeef" || len(doc.Files) != 5 {
		t.Errorf("doc = %+v", doc)
	}
	text, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return Diff(ctx, repo, cfg, h, DiffOptions{Ref: "42", File: "new.txt"}, p)
	})
	if err != nil || strings.TrimSpace(text) != "@@ -0,0 +1 @@\n+hello" {
		t.Errorf("one file: %q %v", text, err)
	}
	if _, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return Diff(ctx, repo, cfg, h, DiffOptions{Ref: "42", File: "nope.txt"}, p)
	}); err == nil {
		t.Error("a file the request does not touch is a usage error")
	}
}

func TestCommentsJSONKeepsWhereAndStrips(t *testing.T) {
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	h := &reviewHost{fakeHost: &fakeHost{}, comments: []provider.Comment{
		{ID: 1, Kind: provider.KindInline, Author: "bo", Body: "rename\x1b[31m this", CreatedAt: at, Path: "a.go", Line: 12, Side: provider.SideRight, Outdated: true, InReplyTo: 0},
		{ID: 2, Kind: provider.KindReview, Author: "cy", CreatedAt: at, ReviewState: provider.ReviewApproved},
	}}
	out, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return Comments(ctx, repo, cfg, h, CommentsOptions{Ref: "42", JSON: true}, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Number   int
		Comments []jsonComment
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(doc.Comments) != 2 || doc.Comments[0].Path != "a.go" || doc.Comments[0].Line != 12 || !doc.Comments[0].Outdated || doc.Comments[1].ReviewState != "approved" {
		t.Errorf("comments = %+v", doc.Comments)
	}
	if strings.ContainsRune(doc.Comments[0].Body, 0x1b) {
		t.Errorf("control characters in a body: %q", doc.Comments[0].Body)
	}
	empty := &reviewHost{fakeHost: &fakeHost{}}
	out, _, _ = runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return Comments(ctx, repo, cfg, empty, CommentsOptions{Ref: "42", JSON: true}, p)
	})
	if !strings.Contains(out, `"comments": []`) {
		t.Errorf("no comments is [] and not null:\n%s", out)
	}
}

func TestCommentRules(t *testing.T) {
	run := func(h *reviewHost, o CommentOptions) (Commented, error) {
		var made Commented
		_, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
			return Comment(ctx, repo, cfg, h, o, &made, p)
		})
		return made, err
	}
	h := &reviewHost{fakeHost: &fakeHost{token: true, single: sample()}}
	h.single.HeadSHA = "deadbeef"
	for name, o := range map[string]CommentOptions{
		"empty body":         {Ref: "42"},
		"line without file":  {Ref: "42", Body: "x", Line: 3},
		"file without line":  {Ref: "42", Body: "x", Path: "a.go"},
		"bad side":           {Ref: "42", Body: "x", Path: "a.go", Line: 1, Side: "middle"},
		"reply without file": {Ref: "42", Body: "x", InReplyTo: 5},
	} {
		if _, err := run(h, o); err == nil {
			t.Errorf("%s: expected a usage error", name)
		}
	}
	if len(h.added) != 0 {
		t.Fatalf("nothing is sent when the arguments are wrong: %+v", h.added)
	}
	if _, err := run(&reviewHost{fakeHost: &fakeHost{token: false}}, CommentOptions{Ref: "42", Body: "x"}); err == nil || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("without a token: %v", err)
	}

	made, err := run(h, CommentOptions{Ref: "42", Body: "thanks"})
	if err != nil || made.CommentID != 99 || len(h.added) != 1 || h.added[0].Path != "" {
		t.Fatalf("discussion: %+v %v %+v", made, err, h.added)
	}
	if _, err := run(h, CommentOptions{Ref: "42", Body: "here", Path: "a.go", Line: 7, Side: "left"}); err != nil {
		t.Fatal(err)
	}
	got := h.added[1]
	if got.Path != "a.go" || got.Line != 7 || got.Side != provider.SideLeft || got.CommitSHA != "deadbeef" {
		t.Errorf("a comment on a line is made on the head commit: %+v", got)
	}
	if _, err := run(h, CommentOptions{Ref: "42", Body: "re", Path: "a.go", InReplyTo: 5}); err != nil {
		t.Fatal(err)
	}
	if got := h.added[2]; got.InReplyTo != 5 || got.CommitSHA != "" {
		t.Errorf("an answer is tied to its thread, not a commit: %+v", got)
	}
}

func TestReviewRules(t *testing.T) {
	run := func(h *reviewHost, o ReviewOptions) error {
		_, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
			return Review(ctx, repo, cfg, h, o, nil, p)
		})
		return err
	}
	h := &reviewHost{fakeHost: &fakeHost{token: true}}
	if err := run(h, ReviewOptions{Ref: "42", Decision: provider.DecisionApprove}); err != nil {
		t.Fatal(err)
	}
	if err := run(h, ReviewOptions{Ref: "42", Decision: provider.DecisionRequestChanges}); err == nil {
		t.Error("asking for changes without a reason is refused")
	}
	if err := run(h, ReviewOptions{Ref: "42", Decision: provider.DecisionRequestChanges, Body: "no"}); err != nil {
		t.Fatal(err)
	}
	if len(h.reviews) != 2 || h.reviews[0].Decision != provider.DecisionApprove || h.reviews[1].Body != "no" {
		t.Errorf("reviews = %+v", h.reviews)
	}
	if err := run(&reviewHost{fakeHost: &fakeHost{}}, ReviewOptions{Ref: "42", Decision: provider.DecisionApprove}); err == nil {
		t.Error("a review needs the token")
	}
}

// plainHost shows only the Provider methods of what it holds, as an adapter that cannot be reviewed yet does.
type plainHost struct{ provider.Provider }

func TestAHostThatCannotBeReviewedSaysSo(t *testing.T) {
	_, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return Comments(ctx, repo, cfg, plainHost{&fakeHost{}}, CommentsOptions{Ref: "42"}, p)
	})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("err = %v", err)
	}
}

func TestListReviewerKeepsTheRequestsWaitingForThatPerson(t *testing.T) {
	mine, other := sample(), sample()
	mine.Number, mine.Reviewers = 1, []string{"Caio", "dee"}
	other.Number, other.Reviewers = 2, []string{"dee"}
	h := &fakeHost{prs: []provider.PullRequest{other, mine}}
	out, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return List(ctx, repo, cfg, h, ListOptions{Reviewer: "caio", JSON: true}, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []reqDoc
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 1 || got[0].Number != 1 {
		t.Errorf("got %+v %v", got, err)
	}
	if h.gotList.Limit != 0 {
		t.Errorf("the provider is asked for its whole page when the review requests are told apart here: %+v", h.gotList)
	}
}

// manageHost is a reviewHost that can also be edited and read commit by commit.
type manageHost struct {
	*reviewHost
	detail provider.CommitDetail
	edits  []provider.PullEdit
	labels []provider.Label
	people []string
}

func (h *manageHost) CommitDetail(context.Context, provider.Repo, string) (provider.CommitDetail, error) {
	return h.detail, nil
}
func (h *manageHost) Labels(context.Context, provider.Repo) ([]provider.Label, bool, error) {
	return h.labels, false, nil
}
func (h *manageHost) People(context.Context, provider.Repo) ([]string, bool, error) {
	return h.people, false, nil
}
func (h *manageHost) Edit(_ context.Context, _ provider.Repo, _ int, e provider.PullEdit) error {
	h.edits = append(h.edits, e)
	return nil
}

func newManageHost() *manageHost {
	return &manageHost{reviewHost: &reviewHost{fakeHost: &fakeHost{token: true, user: "me", single: sample()}}}
}

func TestShowCommitJSONHasTheAuthorTheDateAndMarksFilesWithoutHunks(t *testing.T) {
	h := newManageHost()
	h.detail = provider.CommitDetail{SHA: "abc1234full", Author: "ana", Name: "Ana S", Email: "a@x.io", Date: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC), Subject: "feat: login\x1b[31m", Body: "the body",
		Files: []provider.CommitFile{{Path: "a.go", Status: "modified", Additions: 2, Deletions: 1, Patch: "@@ -1 +1 @@\n+x"}, {Path: "big.bin", Status: "added", Additions: 40}, {Path: "empty.txt", Status: "added"}}}
	out, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return ShowCommit(ctx, repo, cfg, h, CommitOptions{Ref: "42", SHA: "abc1234", JSON: true}, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Author, Date, Subject string
		Files                 []jsonCommitFile
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if doc.Author != "ana" || doc.Date != "2026-11-03T10:00:00Z" || strings.ContainsRune(doc.Subject, 0x1b) || len(doc.Files) != 3 {
		t.Errorf("doc = %+v", doc)
	}
	if doc.Files[0].PatchMissing || !doc.Files[1].PatchMissing || doc.Files[2].PatchMissing {
		t.Errorf("a file that changed lines but has no hunks is marked, an empty file is not: %+v", doc.Files)
	}
	if _, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return ShowCommit(ctx, repo, cfg, h, CommitOptions{Ref: "42", SHA: "--help"}, p)
	}); err == nil {
		t.Error("a commit that is not hexadecimal is a usage error")
	}
}

func TestEditChecksNamesAndNeedsSomethingToDo(t *testing.T) {
	run := func(h *manageHost, o EditOptions) (provider.PullEdit, error) {
		var done provider.PullEdit
		_, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
			return Edit(ctx, repo, cfg, h, o, &done, p)
		})
		return done, err
	}
	h := newManageHost()
	for name, o := range map[string]EditOptions{
		"nothing":       {Ref: "42"},
		"bad login":     {Ref: "42", AddReviewers: []string{"a b"}},
		"flag as login": {Ref: "42", AddAssignees: []string{"--x"}},
		"long label":    {Ref: "42", AddLabels: []string{strings.Repeat("x", 51)}},
		"control label": {Ref: "42", AddLabels: []string{"a\x1bb"}},
	} {
		if _, err := run(h, o); err == nil {
			t.Errorf("%s: expected a usage error", name)
		}
	}
	if len(h.edits) != 0 {
		t.Fatalf("nothing is sent when the arguments are wrong: %+v", h.edits)
	}
	done, err := run(h, EditOptions{Ref: "42", AddReviewers: []string{"@Alice, bob", "alice"}, RemoveAssignees: []string{"cy"}, AddLabels: []string{"bug, needs work"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(done.AddReviewers) != 2 || done.AddReviewers[0] != "Alice" || done.AddReviewers[1] != "bob" || done.RemoveAssignees[0] != "cy" || len(done.AddLabels) != 2 || done.AddLabels[1] != "needs work" {
		t.Errorf("done = %+v: names are split on commas, a leading @ is dropped and a repeat is kept once", done)
	}
	noToken := newManageHost()
	noToken.fakeHost.token = false
	if _, err := run(noToken, EditOptions{Ref: "42", AddLabels: []string{"bug"}}); err == nil {
		t.Error("changing a request needs the token")
	}
}

func TestChoicesListsLabelsAndPeople(t *testing.T) {
	h := newManageHost()
	h.labels = []provider.Label{{Name: "bug", Color: "d73a4a", Description: "wrong"}, {Name: "evil", Color: "red;background:url(x)"}}
	h.people = []string{"ana", "bo"}
	out, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return Choices(ctx, repo, cfg, h, ChoicesOptions{JSON: true}, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Labels []jsonLabel
		People []string
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Labels) != 2 || len(doc.People) != 2 {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.Labels[0].Color != "d73a4a" || doc.Labels[1].Color != "" {
		t.Errorf("a colour that is not six hex digits is dropped, so it can never reach a style: %+v", doc.Labels)
	}
}

func TestViewJSONCarriesTheViewerLabelsAndCommitAuthors(t *testing.T) {
	h := &fakeHost{token: true, user: "me", single: sample(), commits: []provider.CommitSummary{{SHA: "abc", Subject: "feat: a", Author: "ana", Date: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)}}}
	h.single.Labels = []provider.Label{{Name: "bug", Color: "d73a4a"}}
	out, _, err := runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return View(ctx, repo, cfg, h, "42", true, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Viewer  string
		Labels  []jsonLabel
		Commits []jsonCommit
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Viewer != "me" || len(doc.Labels) != 1 || doc.Labels[0].Name != "bug" || doc.Commits[0].Author != "ana" || doc.Commits[0].Date != "2026-11-03T10:00:00Z" {
		t.Errorf("doc = %+v", doc)
	}
	h.token = false
	out, _, _ = runWith(t, func(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
		return View(ctx, repo, cfg, h, "42", true, p)
	})
	if !strings.Contains(out, `"viewer": ""`) {
		t.Errorf("without a token the viewer is empty:\n%s", out)
	}
}
