package remote

import (
	"bytes"
	"context"
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
	if err := Show(ctx, repo, "origin", p); err != nil || !strings.Contains(out.String()+errOut.String(), "gtide remote add") {
		t.Fatalf("no remote: err=%v\n%s%s", err, out, errOut)
	}
	_ = Add(ctx, repo, "origin", "git@github.com:o/r.git", p)
	p, out, _ = printer()
	if err := Show(ctx, repo, "origin", p); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "git@github.com:o/r.git") || !strings.Contains(s, "github") || !strings.Contains(s, "used by gtide") {
		t.Errorf("show:\n%s", s)
	}
	p, _, errOut = printer()
	_ = Show(ctx, repo, "fork", p)
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
