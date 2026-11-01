package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseRemoteURL(t *testing.T) {
	good := map[string]Repo{
		"https://github.com/acme/tool.git":            {"acme", "tool"},
		"https://github.com/acme/tool":                {"acme", "tool"},
		"https://user:tok@github.com/acme/tool.git":   {"acme", "tool"},
		"git@github.com:acme/tool.git":                {"acme", "tool"},
		"ssh://git@github.com/acme/tool.git":          {"acme", "tool"},
		"https://GitHub.com/acme/my.repo-name_1.git/": {"acme", "my.repo-name_1"},
	}
	for in, want := range good {
		got, err := ParseRemoteURL(in)
		if err != nil || got != want {
			t.Errorf("%q → %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "/local/path", "https://gitlab.com/a/b.git", "git@gitlab.com:a/b.git",
		"https://github.com/onlyowner", "https://github.com/a/b/c", "file:///x/y",
	} {
		if _, err := ParseRemoteURL(in); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
}

func TestParsePRRef(t *testing.T) {
	ok := map[string]PRRef{
		"12":                                  {Number: 12},
		"#12":                                 {Number: 12},
		" 7 ":                                 {Number: 7},
		"https://github.com/a/b/pull/5":       {Number: 5, Repo: &Repo{"a", "b"}},
		"https://github.com/a/b/pull/5/files": {Number: 5, Repo: &Repo{"a", "b"}},
	}
	for in, want := range ok {
		got, err := ParsePRRef(in)
		if err != nil || got.Number != want.Number || (want.Repo == nil) != (got.Repo == nil) || (want.Repo != nil && *got.Repo != *want.Repo) {
			t.Errorf("%q → %+v, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "0", "-1", "abc", "1; rm -rf /", "../1", "http://github.com/a/b/pull/1", "99999999999999999999"} {
		if _, err := ParsePRRef(in); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
}

func TestGetPullRequest(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		switch r.URL.Path {
		case "/repos/acme/tool/pulls/1":
			w.Write([]byte(`{"number":1,"title":"Add x","state":"open","user":{"login":"ana"},
				"head":{"ref":"feature/x","repo":{"full_name":"acme/tool"}},"base":{"ref":"main"}}`))
		case "/repos/acme/tool/pulls/2":
			w.Write([]byte(`{"number":2,"title":"t","state":"closed","user":{"login":"b"},"head":{"ref":"h","repo":null},"base":{"ref":"main"}}`))
		case "/repos/acme/tool/pulls/403":
			w.WriteHeader(http.StatusForbidden)
		case "/repos/acme/tool/pulls/429":
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "secret-token", HTTP: srv.Client()}
	repo := Repo{"acme", "tool"}
	ctx := context.Background()

	pr, err := c.GetPullRequest(ctx, repo, 1)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Title != "Add x" || pr.HeadRef != "feature/x" || pr.BaseRef != "main" || pr.Author != "ana" || pr.HeadRepo != "acme/tool" {
		t.Errorf("pr = %+v", pr)
	}
	if gotAuth != "Bearer secret-token" || gotPath != "/repos/acme/tool/pulls/1" {
		t.Errorf("auth=%q path=%q", gotAuth, gotPath)
	}
	if pr, err := c.GetPullRequest(ctx, repo, 2); err != nil || pr.HeadRepo != "" {
		t.Errorf("deleted head repo: %+v %v", pr, err)
	}
	if _, err := c.GetPullRequest(ctx, repo, 9); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if _, err := c.GetPullRequest(ctx, repo, 403); err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Errorf("err = %v", err)
	}
	if _, err := c.GetPullRequest(ctx, repo, 429); err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("err = %v", err)
	}
}

func TestCheckBaseURL(t *testing.T) {
	for _, ok := range []string{"https://api.github.com", "http://127.0.0.1:8080", "http://localhost:1"} {
		if err := checkBaseURL(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://api.github.com", "ftp://x", "api.github.com", ""} {
		if err := checkBaseURL(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestCreatePullRequest(t *testing.T) {
	var gotBody map[string]any
	var gotMethod, gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotType = r.Method, r.Header.Get("Content-Type")
		if r.URL.Path != "/repos/acme/tool/pulls" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewDecoder(r.Body).Decode(&gotBody)
		if gotBody["head"] == "dup" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Write([]byte(`{"message":"Validation Failed","errors":[{"message":"A pull request already exists for acme:dup."}]}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"number":9,"title":"T","state":"open","html_url":"https://github.com/acme/tool/pull/9",
			"user":{"login":"ana"},"head":{"ref":"feature/x"},"base":{"ref":"main"}}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()}
	repo := Repo{"acme", "tool"}

	pr, err := c.CreatePullRequest(context.Background(), repo, NewPullRequest{Title: "T", Body: "B", Head: "feature/x", Base: "main", Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 9 || pr.URL != "https://github.com/acme/tool/pull/9" {
		t.Errorf("pr = %+v", pr)
	}
	if gotMethod != "POST" || gotType != "application/json" {
		t.Errorf("method=%s type=%s", gotMethod, gotType)
	}
	if gotBody["title"] != "T" || gotBody["head"] != "feature/x" || gotBody["base"] != "main" || gotBody["draft"] != true {
		t.Errorf("body = %v", gotBody)
	}

	_, err = c.CreatePullRequest(context.Background(), repo, NewPullRequest{Title: "T", Head: "dup", Base: "main"})
	var verr *ErrValidation
	if !errors.As(err, &verr) || !strings.Contains(verr.Message, "already exists") {
		t.Errorf("err = %v", err)
	}
	if _, err := c.CreatePullRequest(context.Background(), Repo{"x", "y"}, NewPullRequest{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestListProtectedBranchesPaginates(t *testing.T) {
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		page := r.URL.Query().Get("page")
		if r.URL.Query().Get("per_page") != "100" {
			t.Errorf("per_page = %s", r.URL.Query().Get("per_page"))
		}
		var items []string
		n := 100
		if page == "2" {
			n = 1
		}
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("b%s-%d", page, i)
			items = append(items, fmt.Sprintf(`{"name":%q,"protected":%v}`, name, i == 0))
		}
		w.Write([]byte("[" + strings.Join(items, ",") + "]"))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}

	prot, truncated, err := c.ListProtectedBranches(context.Background(), Repo{"acme", "tool"})
	if err != nil || truncated {
		t.Fatalf("err=%v truncated=%v", err, truncated)
	}
	if pages != 2 || len(prot) != 101 || !prot["b1-0"] || prot["b1-1"] || !prot["b2-0"] {
		t.Errorf("pages=%d len=%d", pages, len(prot))
	}
}

func TestListProtectedBranchesTruncates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var items []string
		for i := 0; i < 100; i++ {
			items = append(items, fmt.Sprintf(`{"name":"p%s-%d","protected":false}`, r.URL.Query().Get("page"), i))
		}
		w.Write([]byte("[" + strings.Join(items, ",") + "]"))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	prot, truncated, err := c.ListProtectedBranches(context.Background(), Repo{"a", "b"})
	if err != nil || !truncated || len(prot) != maxPages*pageSize {
		t.Fatalf("err=%v truncated=%v len=%d", err, truncated, len(prot))
	}
}

func TestListOpenPullRequests(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("state")
		w.Write([]byte(`[
			{"number":1,"title":"a","state":"open","user":{"login":"x"},"head":{"ref":"feature/a","repo":{"full_name":"acme/tool"}},"base":{"ref":"main"}},
			{"number":2,"title":"b","state":"open","user":{"login":"y"},"head":{"ref":"feature/b","repo":{"full_name":"fork/tool"}},"base":{"ref":"main"}}]`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	prs, truncated, err := c.ListOpenPullRequests(context.Background(), Repo{"acme", "tool"})
	if err != nil || truncated || gotQuery != "open" {
		t.Fatalf("err=%v truncated=%v state=%q", err, truncated, gotQuery)
	}
	if len(prs) != 2 || prs[0].HeadRef != "feature/a" || prs[1].HeadRepo != "fork/tool" || prs[0].Number != 1 {
		t.Errorf("prs = %+v", prs)
	}
}

func TestListErrorsPropagate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, _, err := c.ListProtectedBranches(context.Background(), Repo{"a", "b"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if _, _, err := c.ListOpenPullRequests(context.Background(), Repo{"a", "b"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}
