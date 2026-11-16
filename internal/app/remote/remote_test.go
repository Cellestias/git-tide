package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/testutil"
)

func setup(t *testing.T) (*repository.Repository, string) {
	t.Helper()
	dir := testutil.NewEmptyRepo(t)
	return repository.New(executor.Exec{}, dir), dir
}

func printer() (output.Printer, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return output.Printer{Out: &out, Err: &errOut}, &out, &errOut
}

func TestAddRegistersAndDetectsTheProvider(t *testing.T) {
	repo, dir := setup(t)
	p, out, errOut := printer()
	if err := Add(context.Background(), repo, "origin", "https://github.com/o/r.git", p); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Run(t, dir, "remote", "get-url", "origin"); got != "https://github.com/o/r.git" {
		t.Errorf("origin = %q", got)
	}
	if all := out.String() + errOut.String(); !strings.Contains(all, "provider: github") {
		t.Errorf("provider not reported:\n%s", all)
	}

	p, out, errOut = printer()
	if err := Add(context.Background(), repo, "upstream", "https://git.internal/o/r.git", p); err != nil {
		t.Fatal(err)
	}
	if all := out.String() + errOut.String(); !strings.Contains(all, "not a known provider") {
		t.Errorf("unknown host should warn:\n%s", all)
	}
}

func TestAddNeverReplacesARemote(t *testing.T) {
	repo, dir := setup(t)
	p, out, errOut := printer()
	ctx := context.Background()
	if err := Add(ctx, repo, "origin", "https://github.com/o/r.git", p); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if err := Add(ctx, repo, "origin", "https://github.com/o/r.git", p); err != nil || !strings.Contains(out.String()+errOut.String(), "already points") {
		t.Errorf("same URL again: err=%v out=%s%s", err, out, errOut)
	}
	if err := Add(ctx, repo, "origin", "https://github.com/o/other.git", p); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("different URL: err = %v, want usage error", err)
	}
	if got := testutil.Run(t, dir, "remote", "get-url", "origin"); got != "https://github.com/o/r.git" {
		t.Errorf("origin was changed to %q", got)
	}
}

func TestAddRejectsUnsafeInput(t *testing.T) {
	repo, dir := setup(t)
	p, _, _ := printer()
	for _, tc := range [][2]string{
		{"origin", "ext::sh -c id"}, {"origin", "--upload-pack=x"}, {"origin", "https://u:tok@github.com/o/r.git"},
		{"-bad", "https://github.com/o/r.git"}, {"a b", "https://github.com/o/r.git"},
	} {
		if err := Add(context.Background(), repo, tc[0], tc[1], p); apperr.CodeOf(err) != apperr.CodeUsage {
			t.Errorf("Add(%q, %q): err = %v, want usage error", tc[0], tc[1], err)
		}
	}
	if got := testutil.Run(t, dir, "remote"); got != "" {
		t.Errorf("a remote was created: %q", got)
	}
}

func TestSetURL(t *testing.T) {
	repo, dir := setup(t)
	p, _, _ := printer()
	ctx := context.Background()
	if err := SetURL(ctx, repo, "origin", "https://github.com/o/r.git", p); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Fatalf("set-url without a remote: err = %v, want usage error", err)
	}
	if err := Add(ctx, repo, "origin", "https://github.com/o/r.git", p); err != nil {
		t.Fatal(err)
	}
	if err := SetURL(ctx, repo, "origin", "git@gitlab.com:g/r.git", p); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Run(t, dir, "remote", "get-url", "origin"); got != "git@gitlab.com:g/r.git" {
		t.Errorf("origin = %q", got)
	}
}

func TestShow(t *testing.T) {
	repo, _ := setup(t)
	ctx := context.Background()
	p, out, errOut := printer()
	if err := Show(ctx, repo, "origin", false, p); err != nil || !strings.Contains(out.String()+errOut.String(), "gtide remote add") {
		t.Fatalf("no remote: err=%v\n%s%s", err, out, errOut)
	}
	_ = Add(ctx, repo, "origin", "git@github.com:o/r.git", p)
	p, out, _ = printer()
	if err := Show(ctx, repo, "origin", false, p); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "git@github.com:o/r.git") || !strings.Contains(s, "github") || !strings.Contains(s, "used by gtide") {
		t.Errorf("show:\n%s", s)
	}
	p, _, errOut = printer()
	_ = Show(ctx, repo, "fork", false, p)
	if !strings.Contains(errOut.String(), "not among these remotes") {
		t.Errorf("a configured remote that does not exist should warn:\n%s", errOut)
	}
}

func TestRedactHidesCredentials(t *testing.T) {
	for in, want := range map[string]string{
		"https://user:pass@host/o/r.git": "https://***@host/o/r.git",
		"https://host/o/r.git":           "https://host/o/r.git",
		"git@host:o/r.git":               "git@host:o/r.git",
	} {
		if got := redact(in); got != want {
			t.Errorf("redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShowJSON(t *testing.T) {
	repo, dir := setup(t)
	ctx := context.Background()
	p, out, _ := printer()
	if err := Show(ctx, repo, "origin", true, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"remotes": []`) || !strings.Contains(out.String(), `"configured_found": false`) {
		t.Errorf("no remote is an empty list, not a sentence:\n%s", out)
	}

	testutil.Run(t, dir, "remote", "add", "origin", "https://user:s3cret@github.com/acme/tool.git")
	testutil.Run(t, dir, "remote", "add", "lab", "git@gitlab.com:team/sub/app.git")
	testutil.Run(t, dir, "remote", "add", "elsewhere", "/srv/git/local.git")
	p, out, _ = printer()
	if err := Show(ctx, repo, "origin", true, p); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Configured      string
		ConfiguredFound bool `json:"configured_found"`
		Remotes         []struct {
			Name, URL, Provider, Repo string
			Used                      bool
		}
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.Configured != "origin" || !got.ConfiguredFound || len(got.Remotes) != 3 {
		t.Fatalf("got = %+v", got)
	}
	by := map[string]int{}
	for i, r := range got.Remotes {
		by[r.Name] = i
	}
	o, l, e := got.Remotes[by["origin"]], got.Remotes[by["lab"]], got.Remotes[by["elsewhere"]]
	if o.Provider != "github" || o.Repo != "acme/tool" || !o.Used || l.Provider != "gitlab" || l.Repo != "team/sub/app" || l.Used || e.Provider != "unknown" || e.Repo != "" {
		t.Errorf("remotes = %+v", got.Remotes)
	}
	if strings.Contains(out.String(), "s3cret") || !strings.Contains(o.URL, "***@github.com") {
		t.Errorf("a password in a URL must never reach the output: %q", o.URL)
	}

	// a configured remote that does not exist is said in the data
	p, out, _ = printer()
	_ = Show(ctx, repo, "fork", true, p)
	if !strings.Contains(out.String(), `"configured": "fork"`) || !strings.Contains(out.String(), `"configured_found": false`) {
		t.Errorf("configured remote missing:\n%s", out)
	}
}
