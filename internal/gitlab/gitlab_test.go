package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseRemoteURL(t *testing.T) {
	good := map[string]Repo{
		"https://gitlab.com/acme/tool.git":                {Owner: "acme", Name: "tool"},
		"https://gitlab.com/acme/tool":                    {Owner: "acme", Name: "tool"},
		"https://user:tok@gitlab.com/acme/tool.git":       {Owner: "acme", Name: "tool"},
		"git@gitlab.com:acme/tool.git":                    {Owner: "acme", Name: "tool"},
		"ssh://git@gitlab.com/acme/tool.git":              {Owner: "acme", Name: "tool"},
		"ssh://git@gitlab.example.org:2222/acme/tool.git": {Owner: "acme", Name: "tool"},
		"git@gitlab.com:group/sub/deep/tool.git":          {Owner: "group/sub/deep", Name: "tool"},
		"https://git.corp.example/team/app/":              {Owner: "team", Name: "app"},
	}
	for in, want := range good {
		got, err := ParseRemoteURL(in)
		if err != nil || got != want {
			t.Errorf("ParseRemoteURL(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "https://gitlab.com/onlyone", "file:///x/y", "https://gitlab.com/a/../b", "https://gitlab.com/a/b c", "/srv/repo.git"} {
		if _, err := ParseRemoteURL(in); err == nil {
			t.Errorf("ParseRemoteURL(%q) should fail", in)
		}
	}
	if h := HostOf("git@gitlab.example.org:a/b.git"); h != "gitlab.example.org" {
		t.Errorf("HostOf = %q", h)
	}
}

func TestParseRef(t *testing.T) {
	good := map[string]Ref{
		"12":   {Number: 12},
		"#12":  {Number: 12},
		"!12":  {Number: 12},
		" 12 ": {Number: 12},
		"https://gitlab.com/a/b/-/merge_requests/5":             {Number: 5, Repo: &Repo{Owner: "a", Name: "b"}},
		"https://gitlab.com/g/s/p/-/merge_requests/5/diffs":     {Number: 5, Repo: &Repo{Owner: "g/s", Name: "p"}},
		"https://gitlab.example.org/a/b/merge_requests/7?x=1#y": {Number: 7, Repo: &Repo{Owner: "a", Name: "b"}},
	}
	for in, want := range good {
		got, err := ParseRef(in)
		if err != nil || got.Number != want.Number || (want.Repo == nil) != (got.Repo == nil) || (want.Repo != nil && *got.Repo != *want.Repo) {
			t.Errorf("ParseRef(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "-1", "abc", "!!1", "#!1", "1; rm -rf /", "https://github.com/a/b/pull/1"} {
		if _, err := ParseRef(in); err == nil {
			t.Errorf("ParseRef(%q) should fail", in)
		}
	}
}

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Setenv("TIDE_GITLAB_API_URL", srv.URL+"/api/v4")
	t.Setenv("GITLAB_TOKEN", "")
	c, err := NewClient("")
	if err != nil {
		t.Fatal(err)
	}
	c.Token = "glpat-test"
	return c
}

func TestGetAndCreate(t *testing.T) {
	var gotBody map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "glpat-test" {
			t.Errorf("missing token header on %s", r.URL)
		}
		if !strings.HasPrefix(r.URL.EscapedPath(), "/api/v4/projects/group%2Fsub%2Ftool/merge_requests") {
			t.Errorf("path = %s", r.URL.EscapedPath())
		}
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.WriteHeader(http.StatusCreated)
		}
		_, _ = w.Write([]byte(`{"iid":3,"title":"T","state":"opened","web_url":"https://gitlab.com/group/sub/tool/-/merge_requests/3",
			"source_branch":"feature/x","target_branch":"main","source_project_id":1,"target_project_id":1,"author":{"username":"ana"}}`))
	})
	repo := Repo{Owner: "group/sub", Name: "tool"}

	pr, err := c.GetPullRequest(context.Background(), repo, 3)
	if err != nil || pr.Number != 3 || pr.State != "opened" || pr.Author != "ana" || pr.HeadRef != "feature/x" || pr.BaseRef != "main" || pr.HeadRepo != "group/sub/tool" {
		t.Fatalf("get = %+v, %v", pr, err)
	}

	pr, err = c.CreatePullRequest(context.Background(), repo, NewPullRequest{Title: "Add x", Body: "b", Head: "feature/x", Base: "main", Draft: true})
	if err != nil || pr.URL == "" {
		t.Fatalf("create = %+v, %v", pr, err)
	}
	if gotBody["title"] != "Draft: Add x" || gotBody["source_branch"] != "feature/x" || gotBody["target_branch"] != "main" || gotBody["description"] != "b" {
		t.Errorf("body = %v", gotBody)
	}
}

func TestForkIsNotSameRepo(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"iid":1,"source_project_id":9,"target_project_id":1}`))
	})
	pr, err := c.GetPullRequest(context.Background(), Repo{Owner: "a", Name: "b"}, 1)
	if err != nil || pr.HeadRepo != "" {
		t.Errorf("fork head repo = %q, %v", pr.HeadRepo, err)
	}
}

func TestErrorMapping(t *testing.T) {
	status := 0
	body := ""
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	repo := Repo{Owner: "a", Name: "b"}
	cases := []struct {
		status int
		body   string
		check  func(error) bool
	}{
		{404, `{}`, func(e error) bool { return errors.Is(e, ErrNotFound) }},
		{401, `{}`, func(e error) bool { return strings.Contains(e.Error(), "GITLAB_TOKEN") }},
		{403, `{}`, func(e error) bool { return strings.Contains(e.Error(), "403") }},
		{429, `{}`, func(e error) bool { return strings.Contains(e.Error(), "rate limit") }},
		{409, `{"message":["Another open merge request already exists for this source branch: !4"]}`, func(e error) bool {
			var v *ErrValidation
			return errors.As(e, &v) && strings.Contains(v.Message, "already exists")
		}},
		{422, `{"message":{"base":["bad"]}}`, func(e error) bool {
			var v *ErrValidation
			return errors.As(e, &v) && strings.Contains(v.Message, "base: bad")
		}},
		{500, `{}`, func(e error) bool { return strings.Contains(e.Error(), "500") }},
	}
	for _, tc := range cases {
		status, body = tc.status, tc.body
		_, err := c.CreatePullRequest(context.Background(), repo, NewPullRequest{})
		if err == nil || !tc.check(err) {
			t.Errorf("status %d: %v", tc.status, err)
		}
		if err != nil && strings.Contains(err.Error(), "glpat-test") {
			t.Errorf("token leaked: %v", err)
		}
	}
}

func TestListsPaginateAndProtected(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case strings.HasSuffix(r.URL.Path, "/protected_branches"):
			_, _ = w.Write([]byte(`[{"name":"main"},{"name":"release/*"}]`))
		case r.URL.Query().Get("page") == "1":
			var items []string
			for i := 1; i <= 100; i++ {
				items = append(items, `{"iid":`+itoa(i)+`,"source_branch":"b","source_project_id":1,"target_project_id":1}`)
			}
			_, _ = w.Write([]byte("[" + strings.Join(items, ",") + "]"))
		default:
			_, _ = w.Write([]byte(`[{"iid":101,"source_branch":"c","source_project_id":1,"target_project_id":1}]`))
		}
		if r.URL.Query().Get("state") != "opened" && strings.HasSuffix(r.URL.Path, "/merge_requests") {
			t.Errorf("state = %q", r.URL.Query().Get("state"))
		}
	})
	repo := Repo{Owner: "a", Name: "b"}
	prs, truncated, err := c.ListOpenPullRequests(context.Background(), repo)
	if err != nil || truncated || len(prs) != 101 || calls != 2 {
		t.Fatalf("list = %d prs, truncated=%v, calls=%d, %v", len(prs), truncated, calls, err)
	}
	prot, _, err := c.ListProtectedBranches(context.Background(), repo)
	if err != nil || !prot["main"] || !prot["release/*"] || prot["dev"] {
		t.Errorf("protected = %v, %v", prot, err)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestCheckBaseURLAndHost(t *testing.T) {
	for _, ok := range []string{"https://gitlab.com/api/v4", "http://127.0.0.1:8080/api/v4", "http://localhost:1"} {
		if err := checkBaseURL(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://gitlab.com/api/v4", "ftp://x", "gitlab.com", ""} {
		if err := checkBaseURL(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	t.Setenv("TIDE_GITLAB_API_URL", "")
	c, err := NewClient("git.example.org")
	if err != nil || c.BaseURL != "https://git.example.org/api/v4" {
		t.Errorf("self-hosted base = %v, %v", c, err)
	}
	if got := c.ReviewRefspec(4, "refs/tide/pr/4"); got != "+refs/merge-requests/4/head:refs/tide/pr/4" {
		t.Errorf("refspec = %q", got)
	}
}
