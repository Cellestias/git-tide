package github

import (
	"context"
	"errors"
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
