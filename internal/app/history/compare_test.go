package history

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/testutil"
)

func requireModernGit(t *testing.T) {
	t.Helper()
	repo := repository.New(executor.Exec{}, t.TempDir())
	major, minor, err := repo.GitVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if major < repository.MinCompareMajor || (major == repository.MinCompareMajor && minor < repository.MinCompareMinor) {
		t.Skipf("needs Git %d.%d, have %d.%d", repository.MinCompareMajor, repository.MinCompareMinor, major, minor)
	}
}

func commitFile(t *testing.T, dir, file, content, msg string) {
	t.Helper()
	testutil.WriteFile(t, dir, file, content)
	testutil.Commit(t, dir, msg)
}

// compareSetup builds feat/sign-in with four commits and feat/staging with two,
// both from main. The sign-in commits:
//
//	1 adds form.txt                      clean
//	2 appends to form.txt                clean only on top of 1
//	3 changes line 2 of f.txt            conflicts with staging
//	4 appends to form.txt again          clean, once 3 is left out
func compareSetup(t *testing.T) (*repository.Repository, string) {
	t.Helper()
	requireModernGit(t)
	dir := testutil.NewRepo(t)
	commitFile(t, dir, "f.txt", "l1\nl2\nl3\n", "chore: add f")

	testutil.Run(t, dir, "checkout", "-q", "-b", "feat/staging")
	commitFile(t, dir, "f.txt", "l1\nSTAGING\nl3\n", "fix: staging edit")
	commitFile(t, dir, "ci.txt", "ci\n", "chore: ci")

	testutil.Run(t, dir, "checkout", "-q", "-b", "feat/sign-in", "main")
	commitFile(t, dir, "form.txt", "form\n", "feat: add sign-in form")
	commitFile(t, dir, "form.txt", "form\nemail\n", "feat: validate email")
	commitFile(t, dir, "f.txt", "l1\nSIGNIN\nl3\n", "fix: trim password")
	commitFile(t, dir, "form.txt", "form\nemail\ntests\n", "test: sign-in tests")
	return repository.New(executor.Exec{}, dir), dir
}

func runCompare(repo *repository.Repository, dir string, opts Options) (string, error) {
	var out, errOut bytes.Buffer
	opts.Cwd = dir
	err := Compare(context.Background(), repo, domain.Default(), "feat/sign-in", "feat/staging", opts, output.Printer{Out: &out, Err: &errOut})
	return out.String(), err
}

func TestCompareJudgesEachCommitOnTopOfTheOnesThatApplied(t *testing.T) {
	repo, dir := compareSetup(t)
	out, err := runCompare(repo, dir, Options{JSON: true})
	if apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Fatalf("a conflict must exit with the unsafe-repo code: %v", err)
	}
	var got struct {
		Ahead []struct {
			Subject       string
			Status        string
			Conflicts     []string
			SkippedBefore []string `json:"skipped_before"`
		}
		Behind      []struct{ Subject string }
		BothChanged []string `json:"changed_on_both"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}

	want := []struct{ subject, status string }{
		{"feat: add sign-in form", StatusClean},
		{"feat: validate email", StatusClean}, // would conflict if tried alone: it needs the form from commit 1
		{"fix: trim password", StatusConflict},
		{"test: sign-in tests", StatusClean},
	}
	if len(got.Ahead) != len(want) {
		t.Fatalf("ahead = %d, want %d\n%s", len(got.Ahead), len(want), out)
	}
	for i, w := range want {
		if got.Ahead[i].Subject != w.subject || got.Ahead[i].Status != w.status {
			t.Errorf("ahead[%d] = %q %s, want %q %s", i, got.Ahead[i].Subject, got.Ahead[i].Status, w.subject, w.status)
		}
	}
	if c := got.Ahead[2].Conflicts; len(c) != 1 || c[0] != "f.txt" {
		t.Errorf("conflicts = %v, want [f.txt]", c)
	}
	if s := got.Ahead[3].SkippedBefore; len(s) != 1 {
		t.Errorf("the last commit was tried without the conflicting one: skipped_before = %v", s)
	}
	if len(got.Behind) != 2 || got.Behind[0].Subject != "fix: staging edit" || got.Behind[1].Subject != "chore: ci" {
		t.Errorf("behind (oldest first) = %v", got.Behind)
	}
	if len(got.BothChanged) != 1 || got.BothChanged[0] != "f.txt" {
		t.Errorf("changed_on_both = %v", got.BothChanged)
	}
}

func TestCompareTextReport(t *testing.T) {
	repo, dir := compareSetup(t)
	out, err := runCompare(repo, dir, Options{})
	if apperr.CodeOf(err) != apperr.CodeUnsafeRepo || !strings.Contains(err.Error(), "3 of 4 commits cherry-pick cleanly onto feat/staging, 1 conflict") {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{
		"feat/sign-in vs feat/staging",
		"4 commits ahead and 2 commits behind",
		"✓ clean", "✗ conflict: f.txt", "(without ",
		"changed on both branches: f.txt",
		"git cherry-pick ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestCompareRecognizesACommitAlreadyCherryPicked(t *testing.T) {
	repo, dir := compareSetup(t)
	first := testutil.Run(t, dir, "rev-parse", "feat/sign-in~3")
	testutil.Run(t, dir, "checkout", "-q", "feat/staging")
	testutil.Run(t, dir, "cherry-pick", first)

	out, _ := runCompare(repo, dir, Options{JSON: true})
	var got struct {
		Ahead []struct{ Subject, Status string }
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Ahead[0].Subject != "feat: add sign-in form" || got.Ahead[0].Status != StatusApplied {
		t.Errorf("ahead[0] = %+v, want it already applied", got.Ahead[0])
	}
}

func TestCompareWithNothingToApply(t *testing.T) {
	requireModernGit(t)
	dir := testutil.NewRepo(t)
	testutil.Run(t, dir, "branch", "a")
	testutil.Run(t, dir, "branch", "b")
	repo := repository.New(executor.Exec{}, dir)
	var out, errOut bytes.Buffer
	if err := Compare(context.Background(), repo, domain.Default(), "a", "b", Options{}, output.Printer{Out: &out, Err: &errOut}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing to apply") || !strings.Contains(out.String(), "0 commits ahead and 0 commits behind") {
		t.Fatalf("report:\n%s", out.String())
	}
}

func TestCompareDoesNotTouchTheRepository(t *testing.T) {
	repo, dir := compareSetup(t)
	testutil.WriteFile(t, dir, "wip.txt", "uncommitted\n")
	before := testutil.Run(t, dir, "status", "--porcelain") + testutil.Run(t, dir, "for-each-ref") + testutil.Run(t, dir, "rev-parse", "HEAD")
	_, _ = runCompare(repo, dir, Options{})
	after := testutil.Run(t, dir, "status", "--porcelain") + testutil.Run(t, dir, "for-each-ref") + testutil.Run(t, dir, "rev-parse", "HEAD")
	if before != after {
		t.Fatalf("repository changed:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestCompareRejectsListFlagsAndUnknownBranches(t *testing.T) {
	repo, dir := compareSetup(t)
	for name, opts := range map[string]Options{
		"--me":    {Me: true},
		"--all":   {All: true},
		"--since": {Since: "7d"},
	} {
		if _, err := runCompare(repo, dir, opts); apperr.CodeOf(err) != apperr.CodeUsage || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	var out, errOut bytes.Buffer
	err := Compare(context.Background(), repo, domain.Default(), "feat/sign-in", "nope", Options{}, output.Printer{Out: &out, Err: &errOut})
	if apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("unknown branch: err = %v", err)
	}
}

type fakeGit struct{ version string }

func (f fakeGit) Run(_ context.Context, c executor.Command) (executor.Result, error) {
	return executor.Result{Stdout: f.version + "\n"}, nil
}

func TestCompareDemandsAModernGit(t *testing.T) {
	for _, old := range []string{"git version 2.39.2 (Apple Git-143)", "git version 1.9.1"} {
		repo := repository.New(fakeGit{old}, t.TempDir())
		var out, errOut bytes.Buffer
		err := Compare(context.Background(), repo, domain.Default(), "a", "b", Options{}, output.Printer{Out: &out, Err: &errOut})
		if apperr.CodeOf(err) != apperr.CodeExternal || !strings.Contains(err.Error(), "needs Git 2.40") {
			t.Errorf("%s: err = %v", old, err)
		}
	}
}
