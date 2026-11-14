package tag

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/testutil"
)

func setup(t *testing.T) (*repository.Repository, string, string) {
	t.Helper()
	dir, bare := testutil.NewRepoWithRemote(t)
	return repository.New(executor.Exec{}, dir), dir, bare
}

func printer() (output.Printer, *bytes.Buffer) {
	var buf bytes.Buffer
	return output.Printer{Out: &buf, Err: &buf}, &buf
}

func TestValidName(t *testing.T) {
	for _, n := range []string{"v1.4.0", "1.4.0", "v2.0.0-rc.1", "v1.0.0+build5", "v10.20.30"} {
		if err := ValidName(n); err != nil {
			t.Errorf("ValidName(%q) = %v", n, err)
		}
	}
	for _, n := range []string{"", "latest", "v1.4", "v1.4.0.1", "-v1.4.0", "v1.4.0 ", "v1.4.0/x", "--delete"} {
		if err := ValidName(n); apperr.CodeOf(err) != apperr.CodeUsage {
			t.Errorf("ValidName(%q) = %v, want usage error", n, err)
		}
	}
}

func TestCreateListPushDelete(t *testing.T) {
	repo, dir, bare := setup(t)
	ctx := context.Background()
	cfg := domain.Default()
	p, _ := printer()

	if err := Create(ctx, repo, cfg, "v1.0.0", CreateOptions{Message: "First release"}, p); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Run(t, dir, "tag", "-l", "--format=%(objecttype) %(contents:subject)", "v1.0.0"); got != "tag First release" {
		t.Errorf("tag = %q", got)
	}
	if out := testutil.Run(t, bare, "tag", "-l"); out != "" {
		t.Errorf("creating must not publish: %q", out)
	}

	p, buf := printer()
	if err := List(ctx, repo, 0, p); err != nil || !strings.Contains(buf.String(), "v1.0.0") || !strings.Contains(buf.String(), "annotated") {
		t.Fatalf("list: err=%v\n%s", err, buf)
	}

	declined := func(string) (bool, error) { return false, nil }
	if err := Push(ctx, repo, cfg, "v1.0.0", declined, p); err == nil {
		t.Error("declined push must fail")
	}
	if out := testutil.Run(t, bare, "tag", "-l"); out != "" {
		t.Errorf("declined push published %q", out)
	}
	if err := Push(ctx, repo, cfg, "v1.0.0", nil, p); err != nil {
		t.Fatal(err)
	}
	if out := testutil.Run(t, bare, "tag", "-l"); out != "v1.0.0" {
		t.Errorf("remote tags = %q", out)
	}

	if err := Delete(ctx, repo, cfg, "v1.0.0", true, nil, p); err != nil {
		t.Fatal(err)
	}
	if out := testutil.Run(t, bare, "tag", "-l"); out != "" {
		t.Errorf("remote tag not deleted: %q", out)
	}
	if out := testutil.Run(t, dir, "tag", "-l"); out != "" {
		t.Errorf("local tag not deleted: %q", out)
	}
}

func TestCreateRefusals(t *testing.T) {
	repo, dir, _ := setup(t)
	ctx := context.Background()
	cfg := domain.Default()
	p, _ := printer()
	code := func(err error) int { return apperr.CodeOf(err) }

	if err := Create(ctx, repo, cfg, "latest", CreateOptions{}, p); code(err) != apperr.CodeUsage {
		t.Errorf("bad name: %v", err)
	}
	if err := Create(ctx, repo, cfg, "v1.0.0", CreateOptions{}, p); err != nil {
		t.Fatal(err)
	}
	if err := Create(ctx, repo, cfg, "v1.0.0", CreateOptions{}, p); code(err) != apperr.CodeUnsafeRepo {
		t.Errorf("existing tag: %v", err)
	}

	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/x")
	if err := Create(ctx, repo, cfg, "v1.1.0", CreateOptions{}, p); code(err) != apperr.CodePolicy {
		t.Errorf("unprotected branch: %v", err)
	}
	testutil.WriteFile(t, dir, "x.txt", "x\n")
	testutil.Commit(t, dir, "feat: x")
	if err := Create(ctx, repo, cfg, "v1.1.0", CreateOptions{AnyBranch: true}, p); code(err) != apperr.CodeUnsafeRepo {
		t.Errorf("commit not on the remote: %v", err)
	}

	testutil.Run(t, dir, "checkout", "-q", "main")
	testutil.WriteFile(t, dir, "m.txt", "m\n")
	testutil.Commit(t, dir, "feat: unpushed on main")
	if err := Create(ctx, repo, cfg, "v1.2.0", CreateOptions{}, p); code(err) != apperr.CodeUnsafeRepo {
		t.Errorf("unpushed main: %v", err)
	}
}

func TestCreateRefusesWhenBehindTheUpstream(t *testing.T) {
	repo, dir, bare := setup(t)
	other := t.TempDir()
	testutil.Run(t, other, "clone", "-q", bare, ".")
	testutil.Run(t, other, "config", "user.email", "o@example.invalid")
	testutil.Run(t, other, "config", "user.name", "Other")
	testutil.WriteFile(t, other, "r.txt", "r\n")
	testutil.Commit(t, other, "feat: remote")
	testutil.Run(t, other, "push", "-q")
	testutil.Run(t, dir, "fetch", "-q")
	p, _ := printer()
	if err := Create(context.Background(), repo, domain.Default(), "v1.0.0", CreateOptions{}, p); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Fatalf("behind: err = %v, want unsafe repo", err)
	}
}

func TestDeleteNeedsConfirmationAndAKnownTag(t *testing.T) {
	repo, dir, _ := setup(t)
	ctx := context.Background()
	p, _ := printer()
	if err := Delete(ctx, repo, domain.Default(), "v9.9.9", false, nil, p); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("unknown tag: %v", err)
	}
	testutil.Run(t, dir, "tag", "-a", "-m", "x", "v1.0.0")
	no := func(string) (bool, error) { return false, nil }
	if err := Delete(ctx, repo, domain.Default(), "v1.0.0", false, no, p); err == nil {
		t.Error("declined delete must fail")
	}
	if got := testutil.Run(t, dir, "tag", "-l"); got != "v1.0.0" {
		t.Errorf("a declined delete removed the tag: %q", got)
	}
}
