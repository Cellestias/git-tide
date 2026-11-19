package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/provider"
)

func reviewServer(t *testing.T, calls *[]string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*calls = append(*calls, r.Method+" "+r.URL.Path+" "+r.Header.Get("Accept")+" "+string(b))
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/acme/tool/issues/7/comments":
			w.Write([]byte(`[{"id":1,"user":{"login":"ana"},"body":"looks good","created_at":"2026-11-03T10:00:00Z","updated_at":"2026-11-03T10:00:00Z","html_url":"u1"}]`))
		case "GET /repos/acme/tool/pulls/7/comments":
			w.Write([]byte(`[{"id":2,"user":{"login":"bo"},"body":"rename this","created_at":"2026-11-03T09:00:00Z","updated_at":"2026-11-03T09:00:00Z","html_url":"u2",
				"path":"a.go","line":12,"side":"RIGHT","diff_hunk":"@@ -1 +1 @@","commit_id":"abc","in_reply_to_id":0,"position":3},
				{"id":3,"user":{"login":"ana"},"body":"done","created_at":"2026-11-03T09:30:00Z","path":"a.go","line":null,"original_line":9,"side":"LEFT","in_reply_to_id":2,"position":null}]`))
		case "GET /repos/acme/tool/pulls/7/reviews":
			w.Write([]byte(`[{"id":4,"user":{"login":"bo"},"body":"","state":"COMMENTED","submitted_at":"2026-11-03T09:00:01Z"},
				{"id":5,"user":{"login":"bo"},"body":"please fix","state":"CHANGES_REQUESTED","submitted_at":"2026-11-03T11:00:00Z"},
				{"id":6,"user":{"login":"cy"},"body":"","state":"APPROVED","submitted_at":"2026-11-03T12:00:00Z"},
				{"id":7,"user":{"login":"dee"},"body":"draft","state":"PENDING","submitted_at":"2026-11-03T13:00:00Z"}]`))
		case "GET /repos/acme/tool/pulls/7":
			if strings.Contains(r.Header.Get("Accept"), "diff") {
				w.Write([]byte("diff --git a/a.go b/a.go\n+x\n"))
				return
			}
			w.Write([]byte(`{"number":7,"title":"t","state":"open","user":{"login":"a"},"head":{"ref":"h","sha":"deadbeef","repo":null},"base":{"ref":"main"}}`))
		case "POST /repos/acme/tool/issues/7/comments":
			w.WriteHeader(201)
			w.Write([]byte(`{"id":10,"user":{"login":"me"},"body":"hello","created_at":"2026-11-04T10:00:00Z","html_url":"u10"}`))
		case "POST /repos/acme/tool/pulls/7/comments":
			w.WriteHeader(201)
			w.Write([]byte(`{"id":11,"user":{"login":"me"},"body":"on line","path":"a.go","line":5,"side":"RIGHT","commit_id":"deadbeef","position":1}`))
		case "POST /repos/acme/tool/pulls/7/reviews":
			w.Write([]byte(`{"id":12}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()}
}

var testRepo = Repo{Owner: "acme", Name: "tool"}

func TestCommentsMergesTheThreeSourcesOldestFirst(t *testing.T) {
	var calls []string
	c := reviewServer(t, &calls)
	got, truncated, err := c.Comments(context.Background(), testRepo, 7)
	if err != nil || truncated {
		t.Fatal(err, truncated)
	}
	var ids []int64
	for _, x := range got {
		ids = append(ids, x.ID)
	}
	// 2 (09:00 inline), 3 (09:30 reply), 1 (10:00 discussion), 5 (11:00 changes requested), 6 (12:00 approved)
	want := []int64{2, 3, 1, 5, 6}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v (a pending review and an empty comment-only review are left out)", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	}
	first, reply := got[0], got[1]
	if first.Kind != provider.KindInline || first.Path != "a.go" || first.Line != 12 || first.Side != provider.SideRight || first.Outdated || first.CommitSHA != "abc" {
		t.Errorf("inline = %+v", first)
	}
	if reply.InReplyTo != 2 || reply.Line != 9 || reply.Side != provider.SideLeft || !reply.Outdated {
		t.Errorf("reply = %+v: the original line and an outdated mark are kept when the line left the diff", reply)
	}
	if got[3].Kind != provider.KindReview || got[3].ReviewState != provider.ReviewChangesRequested || got[3].Body != "please fix" {
		t.Errorf("review = %+v", got[3])
	}
	if got[4].ReviewState != provider.ReviewApproved {
		t.Errorf("approval = %+v", got[4])
	}
}

func TestPullDiffAsksForTheDiffAndKnowsTheHead(t *testing.T) {
	var calls []string
	c := reviewServer(t, &calls)
	d, err := c.PullDiff(context.Background(), testRepo, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(d.Patch, "diff --git") || d.HeadSHA != "deadbeef" || d.Truncated {
		t.Errorf("diff = %+v", d)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "application/vnd.github.v3.diff") {
		t.Errorf("calls = %v", calls)
	}
}

func TestCutAtLine(t *testing.T) {
	if got := cutAtLine("aaa\nbbb\nccc\n", 9); got != "aaa\nbbb\n" {
		t.Errorf("got %q", got)
	}
	if got := cutAtLine("short\n", 100); got != "short\n" {
		t.Errorf("got %q", got)
	}
}

func TestAddCommentToTheDiscussionAndToALine(t *testing.T) {
	var calls []string
	c := reviewServer(t, &calls)
	got, err := c.AddComment(context.Background(), testRepo, 7, provider.NewComment{Body: "hello"})
	if err != nil || got.ID != 10 || got.Kind != provider.KindConversation {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = c.AddComment(context.Background(), testRepo, 7, provider.NewComment{Body: "on line", Path: "a.go", Line: 5, Side: provider.SideLeft, CommitSHA: "deadbeef"})
	if err != nil || got.ID != 11 || got.Kind != provider.KindInline {
		t.Fatalf("%+v %v", got, err)
	}
	var sent map[string]any
	last := calls[len(calls)-1]
	if err := json.Unmarshal([]byte(last[strings.Index(last, "{"):]), &sent); err != nil {
		t.Fatal(err, last)
	}
	if sent["path"] != "a.go" || sent["line"] != float64(5) || sent["side"] != "LEFT" || sent["commit_id"] != "deadbeef" {
		t.Errorf("sent = %v", sent)
	}
	if _, err := c.AddComment(context.Background(), testRepo, 7, provider.NewComment{Body: "re", Path: "a.go", InReplyTo: 2}); err != nil {
		t.Fatal(err)
	}
	last = calls[len(calls)-1]
	if !strings.Contains(last, `"in_reply_to":2`) || strings.Contains(last, "commit_id") {
		t.Errorf("a reply carries the thread, not a position: %s", last)
	}
}

func TestSubmitReviewMapsTheDecision(t *testing.T) {
	var calls []string
	c := reviewServer(t, &calls)
	for decision, event := range map[string]string{provider.DecisionApprove: "APPROVE", provider.DecisionRequestChanges: "REQUEST_CHANGES", provider.DecisionComment: "COMMENT"} {
		if err := c.SubmitReview(context.Background(), testRepo, 7, provider.ReviewSubmission{Decision: decision, Body: "text"}); err != nil {
			t.Fatal(err)
		}
		if last := calls[len(calls)-1]; !strings.Contains(last, `"event":"`+event+`"`) {
			t.Errorf("%s -> %s", decision, last)
		}
	}
	if err := c.SubmitReview(context.Background(), testRepo, 7, provider.ReviewSubmission{Decision: "merge"}); err == nil {
		t.Error("an unknown decision must fail before any request")
	}
}
