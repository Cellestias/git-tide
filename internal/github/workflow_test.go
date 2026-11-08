package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cellestias/git-tide/internal/provider"
)

func newServerClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, HTTP: srv.Client(), Token: "tok"}
}

const listJSON = `[
 {"number":4,"title":"d","state":"open","draft":true,"user":{"login":"Ana"},"assignees":[{"login":"bia"}],"requested_reviewers":[{"login":"caio"}],
  "head":{"ref":"f/d","repo":{"full_name":"acme/tool"}},"base":{"ref":"main"},"created_at":"2026-11-01T10:00:00Z","updated_at":"2026-11-02T10:00:00Z"},
 {"number":3,"title":"c","state":"closed","merged_at":"2026-11-01T09:00:00Z","user":{"login":"bia"},"head":{"ref":"f/c","repo":{"full_name":"acme/tool"}},"base":{"ref":"main"}},
 {"number":2,"title":"b","state":"closed","merged_at":null,"user":{"login":"ana"},"head":{"ref":"f/b","repo":{"full_name":"acme/tool"}},"base":{"ref":"main"}},
 {"number":1,"title":"a","state":"open","user":{"login":"caio"},"head":{"ref":"f/a","repo":null},"base":{"ref":"main"}}]`

func TestListPullRequestsFilters(t *testing.T) {
	var query string
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Error("token not sent")
		}
		_, _ = w.Write([]byte(listJSON))
	})
	repo := Repo{Owner: "acme", Name: "tool"}
	ctx := context.Background()
	nums := func(prs []PullRequest) (out []int) {
		for _, p := range prs {
			out = append(out, p.Number)
		}
		return out
	}
	cases := []struct {
		name string
		opts ListOptions
		want []int
		api  string
	}{
		{"default is open", ListOptions{}, []int{4, 1}, "open"},
		{"merged", ListOptions{State: provider.StateMerged}, []int{3}, "closed"},
		{"closed excludes merged", ListOptions{State: provider.StateClosed}, []int{2}, "closed"},
		{"all", ListOptions{State: provider.StateAll}, []int{4, 3, 2, 1}, "all"},
		{"author is case-insensitive", ListOptions{State: provider.StateAll, Author: "ANA"}, []int{4, 2}, "all"},
		{"assignee", ListOptions{State: provider.StateAll, Assignee: "bia"}, []int{4}, "all"},
		{"limit", ListOptions{State: provider.StateAll, Limit: 2}, []int{4, 3}, "all"},
	}
	for _, tc := range cases {
		prs, truncated, err := c.ListPullRequests(ctx, repo, tc.opts)
		if err != nil || truncated {
			t.Fatalf("%s: err=%v truncated=%v", tc.name, err, truncated)
		}
		got := nums(prs)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
			}
		}
		if !strings.Contains(query, "state="+tc.api) {
			t.Errorf("%s: query %q lacks state=%s", tc.name, query, tc.api)
		}
	}

	prs, _, _ := c.ListPullRequests(ctx, repo, ListOptions{State: provider.StateAll, Head: "f/d"})
	if !strings.Contains(query, "head=acme%3Af%2Fd") || len(prs) == 0 {
		t.Errorf("head filter: query %q", query)
	}
	if _, _, err := c.ListPullRequests(ctx, repo, ListOptions{State: "bogus"}); err == nil {
		t.Error("unknown state accepted")
	}
}

func TestDecodeRichFields(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(listJSON)) })
	prs, _, err := c.ListPullRequests(context.Background(), Repo{Owner: "acme", Name: "tool"}, ListOptions{State: provider.StateAll})
	if err != nil {
		t.Fatal(err)
	}
	d := prs[0]
	if !d.Draft || d.State != provider.StateOpen || len(d.Assignees) != 1 || d.Assignees[0] != "bia" || len(d.Reviewers) != 1 || d.Reviewers[0] != "caio" {
		t.Errorf("pr = %+v", d)
	}
	if !d.CreatedAt.Equal(time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("created = %v", d.CreatedAt)
	}
	if prs[1].State != provider.StateMerged {
		t.Errorf("merged_at not mapped: %q", prs[1].State)
	}
	if prs[3].HeadRepo != "" {
		t.Errorf("deleted fork repo = %q", prs[3].HeadRepo)
	}
}

func TestCurrentUser(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"login":"ana"}`))
	})
	if u, err := c.CurrentUser(context.Background()); err != nil || u != "ana" {
		t.Errorf("user = %q, %v", u, err)
	}
	bad := newServerClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	if _, err := bad.CurrentUser(context.Background()); err == nil {
		t.Error("401 accepted")
	}
}

func TestChangedFilesAndCommits(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/7/files"):
			_, _ = w.Write([]byte(`[{"filename":"a.go","status":"added","additions":10,"deletions":0},
				{"filename":"b.go","status":"removed","additions":0,"deletions":4},
				{"filename":"c.go","previous_filename":"old.go","status":"renamed","additions":1,"deletions":1},
				{"filename":"d.go","status":"changed","additions":2,"deletions":2}]`))
		case strings.HasSuffix(r.URL.Path, "/pulls/7/commits"):
			_, _ = w.Write([]byte(`[{"sha":"aaa1111","commit":{"message":"feat: one\n\nbody"}},{"sha":"bbb2222","commit":{"message":"fix: two"}}]`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	repo := Repo{Owner: "acme", Name: "tool"}
	files, _, err := c.ChangedFiles(context.Background(), repo, 7)
	if err != nil || len(files) != 4 {
		t.Fatalf("files = %+v, %v", files, err)
	}
	if files[1].Status != "deleted" || files[3].Status != "modified" || files[2].OldPath != "old.go" || !files[0].HasStats || files[0].Additions != 10 {
		t.Errorf("files = %+v", files)
	}
	commits, _, err := c.Commits(context.Background(), repo, 7)
	if err != nil || len(commits) != 2 || commits[0].Subject != "feat: one" || commits[1].SHA != "bbb2222" {
		t.Errorf("commits = %+v, %v", commits, err)
	}
}

func TestReviewsReduceToLatestDecision(t *testing.T) {
	body := map[string]string{
		"approved": `[{"state":"COMMENTED","user":{"login":"a"}},{"state":"APPROVED","user":{"login":"a"}},{"state":"APPROVED","user":{"login":"b"}}]`,
		"changes":  `[{"state":"APPROVED","user":{"login":"a"}},{"state":"CHANGES_REQUESTED","user":{"login":"b"}}]`,
		"flipped":  `[{"state":"CHANGES_REQUESTED","user":{"login":"a"}},{"state":"APPROVED","user":{"login":"a"}}]`,
		"dismiss":  `[{"state":"APPROVED","user":{"login":"a"}},{"state":"DISMISSED","user":{"login":"a"}}]`,
		"none":     `[{"state":"COMMENTED","user":{"login":"a"}}]`,
	}
	for name, want := range map[string]string{
		"approved": provider.ReviewApproved, "changes": provider.ReviewChangesRequested,
		"flipped": provider.ReviewApproved, "dismiss": provider.ReviewPending, "none": provider.ReviewPending,
	} {
		c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body[name])) })
		rv, err := c.Reviews(context.Background(), Repo{Owner: "a", Name: "b"}, 1)
		if err != nil || rv.State != want {
			t.Errorf("%s: state = %q, %v (want %q)", name, rv.State, err, want)
		}
	}
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body["approved"])) })
	rv, _ := c.Reviews(context.Background(), Repo{Owner: "a", Name: "b"}, 1)
	if len(rv.Approvals) != 2 || rv.Approvals[0] != "a" || rv.Approvals[1] != "b" {
		t.Errorf("approvals = %v", rv.Approvals)
	}
}
