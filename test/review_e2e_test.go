package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cellestias/git-tide/internal/testutil"
)

// fakeGitHubReview serves one pull request with a diff, comments and the endpoints that take part in a review.
func fakeGitHubReview(t *testing.T, posted *[]string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		const pr = `{"number":7,"title":"Add auth","state":"open","html_url":"https://github.com/acme/tool/pull/7","user":{"login":"ana"},
		  "requested_reviewers":[{"login":"me-reviewer"}],"head":{"ref":"feature/login","sha":"deadbeefcafe","repo":{"full_name":"acme/tool"}},"base":{"ref":"main"}}`
		key := r.Method + " " + r.URL.Path
		if r.Method == http.MethodPost {
			mu.Lock()
			*posted = append(*posted, key+" "+string(body))
			mu.Unlock()
		}
		switch key {
		case "GET /user":
			_, _ = fmt.Fprint(w, `{"login":"me-reviewer"}`)
		case "GET /repos/acme/tool/pulls":
			_, _ = fmt.Fprint(w, "["+pr+`,{"number":8,"title":"Other","state":"open","user":{"login":"bo"},"requested_reviewers":[{"login":"dee"}],"head":{"ref":"x","repo":null},"base":{"ref":"main"}}]`)
		case "GET /repos/acme/tool/pulls/7":
			if strings.Contains(r.Header.Get("Accept"), "diff") {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = fmt.Fprint(w, "diff --git a/a.go b/a.go\nindex 1..2 100644\n--- a/a.go\n+++ b/a.go\n@@ -1 +1,2 @@\n keep\n+added\n")
				return
			}
			_, _ = fmt.Fprint(w, pr)
		case "GET /repos/acme/tool/issues/7/comments":
			_, _ = fmt.Fprint(w, `[{"id":1,"user":{"login":"ana"},"body":"ready","created_at":"2026-11-03T10:00:00Z"}]`)
		case "GET /repos/acme/tool/pulls/7/comments":
			_, _ = fmt.Fprint(w, `[{"id":2,"user":{"login":"bo"},"body":"why?","created_at":"2026-11-03T09:00:00Z","path":"a.go","line":2,"side":"RIGHT","commit_id":"deadbeefcafe","position":2}]`)
		case "GET /repos/acme/tool/pulls/7/reviews":
			_, _ = fmt.Fprint(w, `[{"id":3,"user":{"login":"cy"},"body":"","state":"APPROVED","submitted_at":"2026-11-03T11:00:00Z"}]`)
		case "POST /repos/acme/tool/issues/7/comments":
			w.WriteHeader(201)
			_, _ = fmt.Fprint(w, `{"id":50,"user":{"login":"me-reviewer"},"body":"x","html_url":"https://github.com/acme/tool/pull/7#issuecomment-50"}`)
		case "POST /repos/acme/tool/pulls/7/comments":
			w.WriteHeader(201)
			_, _ = fmt.Fprint(w, `{"id":51,"user":{"login":"me-reviewer"},"body":"x","path":"a.go","line":2,"html_url":"https://github.com/acme/tool/pull/7#discussion_r51","position":2}`)
		case "POST /repos/acme/tool/pulls/7/reviews":
			_, _ = fmt.Fprint(w, `{"id":52}`)
		default:
			w.WriteHeader(404)
			_, _ = fmt.Fprint(w, `{"message":"Not Found"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReviewCommandsEndToEnd(t *testing.T) {
	var posted []string
	srv := fakeGitHubReview(t, &posted)
	env := []string{"TIDE_GITHUB_API_URL=" + srv.URL, "GITHUB_TOKEN=test-token", "GH_TOKEN="}
	dir, bare := testutil.NewRepoWithRemote(t)
	testutil.Run(t, dir, "remote", "set-url", "origin", "https://github.com/acme/tool.git")
	testutil.Run(t, dir, "config", "url."+bare+".insteadOf", "https://github.com/acme/tool.git")
	if out, code := gtide(t, dir, "init", "--no-hooks"); code != 0 {
		t.Fatalf("init: %d\n%s", code, out)
	}

	// diff --json: one entry per file, the head commit for comments on lines
	out, code := stdoutWith(t, dir, env, "pr", "diff", "7", "--json")
	var diff struct {
		Number  int
		HeadSHA string `json:"head_sha"`
		Files   []struct{ Path, Status, Patch string }
	}
	if err := json.Unmarshal([]byte(out), &diff); err != nil || code != 0 || diff.Number != 7 || diff.HeadSHA != "deadbeefcafe" ||
		len(diff.Files) != 1 || diff.Files[0].Path != "a.go" || !strings.HasPrefix(diff.Files[0].Patch, "@@ -1 +1,2 @@") {
		t.Errorf("diff: exit %d (%v) %+v\n%s", code, err, diff, out)
	}

	// comments --json: the discussion, the comment on a line and the approval, oldest first
	out, code = stdoutWith(t, dir, env, "pr", "comments", "7", "--json")
	var cm struct {
		Comments []struct {
			ID                 int64
			Kind, Path, Author string
			Line               int
			ReviewState        string `json:"review_state"`
		}
	}
	if err := json.Unmarshal([]byte(out), &cm); err != nil || code != 0 || len(cm.Comments) != 3 {
		t.Fatalf("comments: exit %d (%v)\n%s", code, err, out)
	}
	if c := cm.Comments[0]; c.Kind != "inline" || c.Path != "a.go" || c.Line != 2 || c.Author != "bo" {
		t.Errorf("first = %+v", c)
	}
	if c := cm.Comments[1]; c.Kind != "conversation" || c.ID != 1 {
		t.Errorf("second = %+v", c)
	}
	if c := cm.Comments[2]; c.Kind != "review" || c.ReviewState != "approved" {
		t.Errorf("third = %+v", c)
	}

	// the actions: a discussion comment, a comment on a line, an approval and a request for changes
	r := act(t, dir, env, "pr-comment", "pr", "comment", "7", "--body", "thanks")
	if r.Details["comment_id"] != float64(50) || r.Details["number"] != float64(7) {
		t.Errorf("comment = %+v", r)
	}
	r = act(t, dir, env, "pr-comment", "pr", "comment", "7", "--file", "a.go", "--line", "2", "--side", "left", "--body", "here")
	if r.Details["comment_id"] != float64(51) {
		t.Errorf("inline comment = %+v", r)
	}
	r = act(t, dir, env, "pr-review", "pr", "approve", "7")
	if r.Details["decision"] != "approve" {
		t.Errorf("approve = %+v", r)
	}
	r = act(t, dir, env, "pr-review", "pr", "request-changes", "7", "--body", "please fix")
	if r.Details["decision"] != "request_changes" {
		t.Errorf("request-changes = %+v", r)
	}
	all := strings.Join(posted, "\n")
	for _, want := range []string{
		`POST /repos/acme/tool/issues/7/comments {"body":"thanks"}`,
		`"commit_id":"deadbeefcafe"`, `"line":2`, `"path":"a.go"`, `"side":"LEFT"`,
		`"event":"APPROVE"`, `"event":"REQUEST_CHANGES"`, `"body":"please fix"`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the API never got %s:\n%s", want, all)
		}
	}

	// refused before reaching the API
	before := len(posted)
	if _, code := stdoutWith(t, dir, env, "pr", "request-changes", "7"); code == 0 {
		t.Error("request-changes without a reason must fail")
	}
	if _, code := stdoutWith(t, dir, env, "pr", "comment", "7", "--file", "a.go", "--body", "no line"); code == 0 {
		t.Error("a comment on a file without a line must fail")
	}
	if _, code := stdoutWith(t, dir, []string{"TIDE_GITHUB_API_URL=" + srv.URL, "GITHUB_TOKEN=", "GH_TOKEN="}, "pr", "approve", "7"); code == 0 {
		t.Error("approving without a token must fail")
	}
	if len(posted) != before {
		t.Errorf("nothing may be sent when the command is refused: %v", posted[before:])
	}

	// list --reviewer me: only what waits for the owner of the token
	out, code = stdoutWith(t, dir, env, "pr", "list", "--reviewer", "me", "--json")
	var list []struct{ Number int }
	if err := json.Unmarshal([]byte(out), &list); err != nil || code != 0 || len(list) != 1 || list[0].Number != 7 {
		t.Errorf("list --reviewer me: exit %d (%v) %+v\n%s", code, err, list, out)
	}
}
