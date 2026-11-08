package gitlab

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/provider"
)

func TestListPullRequestsMapsFilters(t *testing.T) {
	var query string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = w.Write([]byte(`[
		 {"iid":9,"title":"t","state":"opened","draft":true,"author":{"username":"ana"},"assignees":[{"username":"bia"}],"reviewers":[{"username":"caio"}],
		  "source_branch":"f/x","target_branch":"main","source_project_id":1,"target_project_id":1,"created_at":"2026-11-01T10:00:00Z","description":"desc"},
		 {"iid":8,"state":"merged","source_branch":"f/y","target_branch":"main","source_project_id":2,"target_project_id":1},
		 {"iid":7,"state":"locked","source_branch":"f/z","target_branch":"main","source_project_id":1,"target_project_id":1}]`))
	})
	repo := Repo{Owner: "g/sub", Name: "p"}
	prs, truncated, err := c.ListPullRequests(context.Background(), repo, ListOptions{State: provider.StateMerged, Author: "ana", Assignee: "bia", Head: "f/x"})
	if err != nil || truncated || len(prs) != 3 {
		t.Fatalf("prs=%d truncated=%v err=%v", len(prs), truncated, err)
	}
	for _, want := range []string{"state=merged", "author_username=ana", "assignee_username=bia", "source_branch=f%2Fx", "order_by=created_at"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q lacks %q", query, want)
		}
	}
	x := prs[0]
	if x.State != provider.StateOpen || !x.Draft || x.Body != "desc" || x.Assignees[0] != "bia" || x.Reviewers[0] != "caio" || x.CreatedAt.IsZero() {
		t.Errorf("pr = %+v", x)
	}
	if prs[1].State != provider.StateMerged || prs[1].HeadRepo != "" {
		t.Errorf("fork/merged = %+v", prs[1])
	}
	if prs[2].State != provider.StateOpen {
		t.Errorf("locked = %q", prs[2].State)
	}

	limited, _, _ := c.ListPullRequests(context.Background(), repo, ListOptions{State: provider.StateAll, Limit: 2})
	if len(limited) != 2 || !strings.Contains(query, "state=all") {
		t.Errorf("limit: %d prs, query %q", len(limited), query)
	}
	if _, _, err := c.ListPullRequests(context.Background(), repo, ListOptions{State: "bogus"}); err == nil {
		t.Error("unknown state accepted")
	}
}

func TestCurrentUserFilesCommitsReviews(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/user":
			_, _ = w.Write([]byte(`{"username":"ana"}`))
		case strings.HasSuffix(r.URL.Path, "/merge_requests/5/diffs"):
			_, _ = w.Write([]byte(`[{"old_path":"a","new_path":"a","new_file":true},{"old_path":"b","new_path":"b","deleted_file":true},
				{"old_path":"c","new_path":"d","renamed_file":true},{"old_path":"e","new_path":"e"}]`))
		case strings.HasSuffix(r.URL.Path, "/merge_requests/5/commits"):
			_, _ = w.Write([]byte(`[{"id":"bbb","title":"second"},{"id":"aaa","title":"first"}]`))
		case strings.HasSuffix(r.URL.Path, "/merge_requests/5/approvals"):
			_, _ = w.Write([]byte(`{"approved_by":[{"user":{"username":"bia"}}],"approvals_left":0}`))
		case strings.HasSuffix(r.URL.Path, "/merge_requests/6/approvals"):
			_, _ = w.Write([]byte(`{"approved_by":[{"user":{"username":"bia"}}],"approvals_left":1}`))
		case strings.HasSuffix(r.URL.Path, "/merge_requests/7/approvals"):
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	repo := Repo{Owner: "g", Name: "p"}
	ctx := context.Background()
	if u, err := c.CurrentUser(ctx); err != nil || u != "ana" {
		t.Errorf("user = %q, %v", u, err)
	}
	files, _, err := c.ChangedFiles(ctx, repo, 5)
	if err != nil || len(files) != 4 {
		t.Fatalf("files = %+v, %v", files, err)
	}
	if files[0].Status != "added" || files[1].Status != "deleted" || files[1].Path != "b" || files[2].Status != "renamed" || files[2].OldPath != "c" || files[3].Status != "modified" || files[0].HasStats {
		t.Errorf("files = %+v", files)
	}
	commits, _, err := c.Commits(ctx, repo, 5)
	if err != nil || len(commits) != 2 || commits[0].Subject != "first" {
		t.Errorf("commits not oldest first: %+v, %v", commits, err)
	}
	if rv, err := c.Reviews(ctx, repo, 5); err != nil || rv.State != provider.ReviewApproved || rv.Approvals[0] != "bia" {
		t.Errorf("approved = %+v, %v", rv, err)
	}
	if rv, err := c.Reviews(ctx, repo, 6); err != nil || rv.State != provider.ReviewPending || len(rv.Approvals) != 1 {
		t.Errorf("partial = %+v, %v", rv, err)
	}
	if rv, err := c.Reviews(ctx, repo, 7); err != nil || rv.State != provider.ReviewPending {
		t.Errorf("no approvals feature = %+v, %v", rv, err)
	}
}
