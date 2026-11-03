package commit

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/testutil"
)

// setup returns a repo on feature/x with one initial commit.
func setup(t *testing.T) (*repository.Repository, string) {
	t.Helper()
	dir := testutil.NewRepo(t)
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/x")
	return repository.New(executor.Exec{}, dir), dir
}

func run(repo *repository.Repository, dir string, opts Options) (string, error) {
	if opts.Cwd == "" {
		opts.Cwd = dir
	}
	var out, errOut bytes.Buffer
	err := Run(context.Background(), repo, domain.Default(), opts, output.Printer{Out: &out, Err: &errOut})
	return out.String() + errOut.String(), err
}

func staged(t *testing.T, dir string) string {
	return testutil.Run(t, dir, "diff", "--cached", "--name-only")
}

func subject(t *testing.T, dir string) string {
	return testutil.Run(t, dir, "log", "-1", "--format=%s")
}

func TestSaveStagesOnlyNamedFilesAndCommits(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "auth/login.tsx", "x\n")
	testutil.WriteFile(t, dir, "other.txt", "y\n")

	if _, err := run(repo, dir, Options{Message: "feat(auth): add login form", Files: []string{"./auth/login.tsx"}}); err != nil {
		t.Fatal(err)
	}
	if subject(t, dir) != "feat(auth): add login form" {
		t.Errorf("subject = %q", subject(t, dir))
	}
	if files := testutil.Run(t, dir, "show", "--name-only", "--format=", "HEAD"); files != "auth/login.tsx" {
		t.Errorf("commit files = %q", files)
	}
	if st := testutil.Run(t, dir, "status", "--porcelain"); !strings.Contains(st, "?? other.txt") {
		t.Errorf("other.txt must stay untracked, status:\n%s", st)
	}
}

func TestSaveResolvesPathsFromSubdirectory(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "auth/login.tsx", "x\n")

	if _, err := run(repo, dir, Options{Message: "feat: add login", Files: []string{"login.tsx"}, Cwd: filepath.Join(dir, "auth")}); err != nil {
		t.Fatal(err)
	}
	if files := testutil.Run(t, dir, "show", "--name-only", "--format=", "HEAD"); files != "auth/login.tsx" {
		t.Errorf("commit files = %q", files)
	}
}

func TestSaveStagesDirectory(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "docs/a.md", "a\n")
	testutil.WriteFile(t, dir, "docs/b.md", "b\n")
	if _, err := run(repo, dir, Options{Message: "docs: add pages", Files: []string{"docs"}}); err != nil {
		t.Fatal(err)
	}
	if files := testutil.Run(t, dir, "show", "--name-only", "--format=", "HEAD"); !strings.Contains(files, "docs/a.md") || !strings.Contains(files, "docs/b.md") {
		t.Errorf("commit files = %q", files)
	}
}

func TestSaveRefusesWhenOtherFilesAreStaged(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "pre.txt", "p\n")
	testutil.Run(t, dir, "add", "pre.txt")
	testutil.WriteFile(t, dir, "new.txt", "n\n")
	head := testutil.Run(t, dir, "rev-parse", "HEAD")

	_, err := run(repo, dir, Options{Message: "feat: add new", Files: []string{"new.txt"}})
	if apperr.CodeOf(err) != apperr.CodeUnsafeRepo || !strings.Contains(err.Error(), "pre.txt") {
		t.Fatalf("err = %v", err)
	}
	if testutil.Run(t, dir, "rev-parse", "HEAD") != head || staged(t, dir) != "pre.txt" {
		t.Error("repository state changed")
	}
}

func TestSecretUndoesStaging(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "ok.txt", "fine\n")
	testutil.WriteFile(t, dir, "cfg.env", "token = "+"ghp_"+strings.Repeat("a", 36)+"\n")
	head := testutil.Run(t, dir, "rev-parse", "HEAD")

	out, err := run(repo, dir, Options{Message: "feat: add config", Files: []string{"ok.txt", "cfg.env"}})
	if apperr.CodeOf(err) != apperr.CodePolicy {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(out+err.Error(), strings.Repeat("a", 36)) {
		t.Error("secret value leaked into output")
	}
	if testutil.Run(t, dir, "rev-parse", "HEAD") != head || staged(t, dir) != "" {
		t.Errorf("must leave nothing staged; staged=%q", staged(t, dir))
	}
	if _, err := os.Stat(filepath.Join(dir, "cfg.env")); err != nil {
		t.Error("work tree file must be untouched")
	}
}

func TestRefusesProtectedBranch(t *testing.T) {
	repo, dir := setup(t)
	testutil.Run(t, dir, "checkout", "-q", "main")
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	_, err := run(repo, dir, Options{Message: "feat: a", Files: []string{"a.txt"}})
	if apperr.CodeOf(err) != apperr.CodePolicy {
		t.Fatalf("err = %v", err)
	}
	if staged(t, dir) != "" {
		t.Error("nothing may be staged after a refusal")
	}
}

func TestInvalidMessageIsRejectedBeforeStaging(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	_, err := run(repo, dir, Options{Message: "added stuff", Files: []string{"a.txt"}})
	if apperr.CodeOf(err) != apperr.CodePolicy || !strings.Contains(apperr.HintOf(err), "feat") {
		t.Fatalf("err = %v hint=%q", err, apperr.HintOf(err))
	}
	if staged(t, dir) != "" {
		t.Error("nothing may be staged after a refusal")
	}
}

func TestTypeAndScopeBuildTheSubject(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	if _, err := run(repo, dir, Options{Type: "fix", Scope: "api", Message: "handle nil", Body: "Why it broke.", Files: []string{"a.txt"}}); err != nil {
		t.Fatal(err)
	}
	if subject(t, dir) != "fix(api): handle nil" {
		t.Errorf("subject = %q", subject(t, dir))
	}
	if body := testutil.Run(t, dir, "log", "-1", "--format=%b"); body != "Why it broke." {
		t.Errorf("body = %q", body)
	}
	if _, err := BuildMessage("", "api", "x"); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("--scope without --type must be a usage error: %v", err)
	}
}

func TestCommitUsesStagedOnly(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	testutil.WriteFile(t, dir, "b.txt", "b\n")
	testutil.Run(t, dir, "add", "a.txt")
	if _, err := run(repo, dir, Options{Message: "feat: add a"}); err != nil {
		t.Fatal(err)
	}
	if files := testutil.Run(t, dir, "show", "--name-only", "--format=", "HEAD"); files != "a.txt" {
		t.Errorf("commit files = %q", files)
	}
}

func TestCommitWithNothingStaged(t *testing.T) {
	repo, dir := setup(t)
	_, err := run(repo, dir, Options{Message: "feat: nothing"})
	if apperr.CodeOf(err) != apperr.CodeUnsafeRepo || !strings.Contains(err.Error(), "nothing to commit") {
		t.Fatalf("err = %v", err)
	}
}

func TestCommitAllStagesTrackedOnly(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "t.txt", "1\n")
	testutil.Commit(t, dir, "feat: add t")
	testutil.WriteFile(t, dir, "t.txt", "2\n")
	testutil.WriteFile(t, dir, "untracked.txt", "u\n")
	if _, err := run(repo, dir, Options{Message: "fix: change t", All: true}); err != nil {
		t.Fatal(err)
	}
	if files := testutil.Run(t, dir, "show", "--name-only", "--format=", "HEAD"); files != "t.txt" {
		t.Errorf("commit files = %q", files)
	}
}

func TestPathsOutsideTheRepositoryAreRejected(t *testing.T) {
	repo, dir := setup(t)
	for _, f := range []string{"../escape.txt", "/etc/hosts", ""} {
		if _, err := run(repo, dir, Options{Message: "feat: x", Files: []string{f}}); apperr.CodeOf(err) != apperr.CodeUsage {
			t.Errorf("%q: err = %v, want usage error", f, err)
		}
	}
}

func TestSaveRefusesDuringMerge(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	if err := os.WriteFile(filepath.Join(dir, ".git", "MERGE_HEAD"), []byte(testutil.Run(t, dir, "rev-parse", "HEAD")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(repo, dir, Options{Message: "feat: a", Files: []string{"a.txt"}}); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Fatalf("err = %v", err)
	}
}
