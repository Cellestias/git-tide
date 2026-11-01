package change

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

type fixture struct {
	repo *repository.Repository
	dir  string
	bare string
}

// setup: main (pushed) and local branches feature/two-fa, feature/login, fix/typo,
// plus remote-only branches colleague/payments (fetched) and release/1 (fetched).
func setup(t *testing.T) fixture {
	t.Helper()
	dir, bare := testutil.NewRepoWithRemote(t)
	for _, b := range []string{"feature/two-fa", "feature/login", "fix/typo"} {
		testutil.Run(t, dir, "branch", b, "main")
	}
	testutil.Run(t, bare, "branch", "colleague/payments", "main")
	testutil.Run(t, bare, "branch", "release/1", "main")
	testutil.Run(t, dir, "fetch", "-q", "origin")
	return fixture{repository.New(executor.Exec{}, dir), dir, bare}
}

func run(t *testing.T, f fixture, query string, opts Options) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := Run(context.Background(), f.repo, domain.Default(), query, opts, output.Printer{Out: &out, Err: &errOut})
	return out.String(), err
}

func current(t *testing.T, f fixture) string {
	return testutil.Run(t, f.dir, "rev-parse", "--abbrev-ref", "HEAD")
}

func TestExactAndPartialMatch(t *testing.T) {
	f := setup(t)
	if _, err := run(t, f, "feature/login", Options{}); err != nil || current(t, f) != "feature/login" {
		t.Fatalf("exact: err=%v on %s", err, current(t, f))
	}
	if out, err := run(t, f, "two-fa", Options{}); err != nil || current(t, f) != "feature/two-fa" {
		t.Fatalf("partial: err=%v on %s\n%s", err, current(t, f), out)
	}
	// case-insensitive substring
	if _, err := run(t, f, "TYPO", Options{}); err != nil || current(t, f) != "fix/typo" {
		t.Fatalf("case-insensitive: err=%v on %s", err, current(t, f))
	}
}

func TestExactBeatsSubstring(t *testing.T) {
	f := setup(t)
	testutil.Run(t, f.dir, "branch", "main-old", "main")
	if _, err := run(t, f, "main", Options{}); err != nil || current(t, f) != "main" {
		t.Fatalf("err=%v on %s", err, current(t, f))
	}
}

func TestAmbiguousAndMissing(t *testing.T) {
	f := setup(t)
	_, err := run(t, f, "feature", Options{})
	if apperr.CodeOf(err) != apperr.CodeUnsafeRepo || !strings.Contains(err.Error(), "feature/login") || !strings.Contains(err.Error(), "feature/two-fa") {
		t.Fatalf("ambiguous: %v", err)
	}
	if current(t, f) != "main" {
		t.Error("switched despite ambiguity")
	}
	if _, err := run(t, f, "nothing-like-this", Options{}); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("missing: %v", err)
	}
	if _, err := run(t, f, "--force", Options{}); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("option-like query: %v", err)
	}
}

func TestPreviousBranch(t *testing.T) {
	f := setup(t)
	if _, err := run(t, f, "-", Options{}); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("no previous yet: %v", err)
	}
	if _, err := run(t, f, "feature/login", Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, f, "-", Options{}); err != nil || current(t, f) != "main" {
		t.Fatalf("err=%v on %s", err, current(t, f))
	}
	if _, err := run(t, f, "-", Options{}); err != nil || current(t, f) != "feature/login" {
		t.Fatalf("toggle: err=%v on %s", err, current(t, f))
	}
}

func TestRemoteOnlyBranchGetsUpstream(t *testing.T) {
	f := setup(t)
	out, err := run(t, f, "payments", Options{}) // partial match on a remote-only branch
	if err != nil || current(t, f) != "colleague/payments" {
		t.Fatalf("err=%v on %s", err, current(t, f))
	}
	if !strings.Contains(out, "tracking origin/colleague/payments") {
		t.Errorf("output = %q", out)
	}
	if got := testutil.Run(t, f.dir, "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/colleague/payments" {
		t.Errorf("upstream = %s", got)
	}
}

func TestAlreadyOnBranch(t *testing.T) {
	f := setup(t)
	out, err := run(t, f, "main", Options{})
	if err != nil || !strings.Contains(out, "already on main") {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

func TestDirtyTreeRefusedUnlessCarry(t *testing.T) {
	f := setup(t)
	testutil.WriteFile(t, f.dir, "README.md", "dirty\n")

	_, err := run(t, f, "feature/login", Options{})
	if apperr.CodeOf(err) != apperr.CodeUnsafeRepo || !strings.Contains(apperr.HintOf(err), "--carry") {
		t.Fatalf("err=%v hint=%q", err, apperr.HintOf(err))
	}
	if current(t, f) != "main" {
		t.Error("switched despite uncommitted changes")
	}

	// non-conflicting changes travel with --carry
	if _, err := run(t, f, "feature/login", Options{Carry: true}); err != nil || current(t, f) != "feature/login" {
		t.Fatalf("carry: err=%v on %s", err, current(t, f))
	}
	if st := testutil.Run(t, f.dir, "status", "--porcelain"); !strings.Contains(st, "README.md") {
		t.Errorf("changes lost: %q", st)
	}
}

func TestCarryRefusesOnConflictAndKeepsChanges(t *testing.T) {
	f := setup(t)
	testutil.Run(t, f.dir, "checkout", "-q", "feature/login")
	testutil.WriteFile(t, f.dir, "README.md", "login version\n")
	testutil.Commit(t, f.dir, "feat: login readme")
	testutil.Run(t, f.dir, "checkout", "-q", "main")
	testutil.WriteFile(t, f.dir, "README.md", "my edits\n")

	_, err := run(t, f, "feature/login", Options{Carry: true})
	if apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Fatalf("err = %v", err)
	}
	if current(t, f) != "main" || testutil.Run(t, f.dir, "status", "--porcelain") == "" {
		t.Error("state changed despite refusal")
	}
}

func TestRefusesDuringMerge(t *testing.T) {
	f := setup(t)
	testutil.Run(t, f.dir, "checkout", "-q", "-b", "a")
	testutil.WriteFile(t, f.dir, "README.md", "a\n")
	testutil.Commit(t, f.dir, "feat: a")
	testutil.Run(t, f.dir, "checkout", "-q", "main")
	testutil.WriteFile(t, f.dir, "README.md", "main\n")
	testutil.Commit(t, f.dir, "feat: m")
	if _, err := testutil.TryRun(f.dir, "merge", "a"); err == nil {
		t.Fatal("expected conflict")
	}
	if _, err := run(t, f, "feature/login", Options{Carry: true}); apperr.CodeOf(err) != apperr.CodeUnsafeRepo || !strings.Contains(err.Error(), "merge") {
		t.Errorf("err = %v", err)
	}
}

func TestHints(t *testing.T) {
	f := setup(t)
	// main moves on: feature/login is now behind it
	testutil.WriteFile(t, f.dir, "m.txt", "m\n")
	testutil.Commit(t, f.dir, "feat: main moves")
	testutil.Run(t, f.dir, "push", "-q", "origin", "main")

	out, err := run(t, f, "feature/login", Options{})
	if err != nil || !strings.Contains(out, "↓1 behind main: run `tide update`") {
		t.Fatalf("err=%v out=%q", err, out)
	}

	out, err = run(t, f, "main", Options{})
	if err != nil || !strings.Contains(out, "protected branch") {
		t.Fatalf("err=%v out=%q", err, out)
	}

	// upstream ahead on the remote
	testutil.Run(t, f.dir, "checkout", "-q", "-b", "feature/shared")
	testutil.Run(t, f.dir, "push", "-q", "-u", "origin", "feature/shared")
	other := t.TempDir()
	testutil.Run(t, other, "clone", "-q", f.bare, ".")
	testutil.Run(t, other, "checkout", "-q", "feature/shared")
	testutil.WriteFile(t, other, "s.txt", "s\n")
	testutil.Commit(t, other, "feat: shared")
	testutil.Run(t, other, "push", "-q", "origin", "feature/shared")
	testutil.Run(t, f.dir, "fetch", "-q", "origin")
	testutil.Run(t, f.dir, "checkout", "-q", "main")
	out, err = run(t, f, "feature/shared", Options{})
	if err != nil || !strings.Contains(out, "behind origin/feature/shared: run `git pull`") {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

func TestMakesNoNetworkCall(t *testing.T) {
	f := setup(t)
	testutil.Run(t, f.dir, "remote", "set-url", "origin", "/nonexistent/remote")
	if _, err := run(t, f, "payments", Options{}); err != nil {
		t.Fatalf("change must work offline: %v", err)
	}
}

func TestOutputStripsControlCharacters(t *testing.T) {
	f := setup(t)
	_, err := run(t, f, "zzz\x1b[31m", Options{})
	if err == nil || strings.Contains(err.Error(), "\x1b") {
		t.Errorf("err = %q", err)
	}
}
