package push

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"sort"
	"testing"

	appsecurity "github.com/cellestias/git-tide/internal/app/security"
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

func run(repo *repository.Repository, cfg domain.Config, opts Options) error {
	var out, errOut bytes.Buffer
	return Run(context.Background(), repo, cfg, opts, output.Printer{Out: &out, Err: &errOut})
}

func code(err error) int {
	var e *apperr.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return -1
}

func commitOnBranch(t *testing.T, dir, name string) {
	t.Helper()
	testutil.Run(t, dir, "checkout", "-q", "-b", name)
	testutil.WriteFile(t, dir, name+".txt", "x\n")
	testutil.Commit(t, dir, "feat: "+name)
}

func TestPushesAndSetsUpstream(t *testing.T) {
	repo, dir, bare := setup(t)
	commitOnBranch(t, dir, "feature/a")

	if err := run(repo, domain.Default(), Options{}); err != nil {
		t.Fatal(err)
	}
	if got, want := testutil.Run(t, bare, "rev-parse", "feature/a"), testutil.Run(t, dir, "rev-parse", "HEAD"); got != want {
		t.Errorf("remote tip = %s, want %s", got, want)
	}
	if up := testutil.Run(t, dir, "rev-parse", "--abbrev-ref", "feature/a@{upstream}"); up != "origin/feature/a" {
		t.Errorf("upstream = %q", up)
	}
}

func TestRefusesProtectedBranch(t *testing.T) {
	repo, _, bare := setup(t) // clone is on main
	before := testutil.Run(t, bare, "rev-parse", "main")

	err := run(repo, domain.Default(), Options{})
	if code(err) != apperr.CodePolicy {
		t.Fatalf("err = %v, want policy error", err)
	}
	if testutil.Run(t, bare, "rev-parse", "main") != before {
		t.Error("protected branch was modified")
	}
}

func TestRefusesDetachedHead(t *testing.T) {
	repo, dir, _ := setup(t)
	testutil.Run(t, dir, "checkout", "-q", "--detach")
	if err := run(repo, domain.Default(), Options{}); code(err) != apperr.CodeUnsafeRepo {
		t.Fatalf("err = %v, want unsafe repo", err)
	}
}

func TestMissingRemoteIsConfigError(t *testing.T) {
	repo, dir, _ := setup(t)
	commitOnBranch(t, dir, "feature/a")
	cfg := domain.Default()
	cfg.Remote = "nope"
	if err := run(repo, cfg, Options{}); code(err) != apperr.CodeConfig {
		t.Fatalf("err = %v, want config error", err)
	}
}

func rewrite(t *testing.T, dir string) {
	t.Helper()
	testutil.WriteFile(t, dir, "feature/a.txt", "rewritten\n")
	testutil.Run(t, dir, "add", "feature/a.txt")
	testutil.Run(t, dir, "commit", "-q", "--amend", "-m", "feat: rewritten")
}

func TestNonFastForwardNeedsForce(t *testing.T) {
	repo, dir, bare := setup(t)
	commitOnBranch(t, dir, "feature/a")
	if err := run(repo, domain.Default(), Options{}); err != nil {
		t.Fatal(err)
	}
	remoteTip := testutil.Run(t, bare, "rev-parse", "feature/a")
	rewrite(t, dir)

	if err := run(repo, domain.Default(), Options{}); code(err) != apperr.CodeExternal {
		t.Fatalf("plain push of rewritten history: err = %v, want external error", err)
	}
	if testutil.Run(t, bare, "rev-parse", "feature/a") != remoteTip {
		t.Fatal("remote moved without --force")
	}

	if err := run(repo, domain.Default(), Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	if testutil.Run(t, bare, "rev-parse", "feature/a") != testutil.Run(t, dir, "rev-parse", "HEAD") {
		t.Error("forced push did not update the remote")
	}
}

func TestForceDeclinedPushesNothing(t *testing.T) {
	repo, dir, bare := setup(t)
	commitOnBranch(t, dir, "feature/a")
	if err := run(repo, domain.Default(), Options{}); err != nil {
		t.Fatal(err)
	}
	remoteTip := testutil.Run(t, bare, "rev-parse", "feature/a")
	rewrite(t, dir)

	err := run(repo, domain.Default(), Options{Force: true, Confirm: func(string) (bool, error) { return false, nil }})
	if err == nil {
		t.Fatal("expected abort")
	}
	if testutil.Run(t, bare, "rev-parse", "feature/a") != remoteTip {
		t.Error("remote moved although the user declined")
	}
}

func TestForceKeepsLeaseOverUnseenWork(t *testing.T) {
	repo, dir, bare := setup(t)
	commitOnBranch(t, dir, "feature/a")
	if err := run(repo, domain.Default(), Options{}); err != nil {
		t.Fatal(err)
	}
	// a teammate pushes to the same branch; we never fetch it
	other := t.TempDir()
	testutil.Run(t, other, "clone", "-q", "-b", "feature/a", bare, ".")
	testutil.WriteFile(t, other, "theirs.txt", "t\n")
	testutil.Commit(t, other, "feat: theirs")
	testutil.Run(t, other, "push", "-q", "origin", "feature/a")
	theirs := testutil.Run(t, bare, "rev-parse", "feature/a")
	rewrite(t, dir)

	if err := run(repo, domain.Default(), Options{Force: true}); code(err) != apperr.CodeExternal {
		t.Fatalf("err = %v, want rejection by the lease", err)
	}
	if testutil.Run(t, bare, "rev-parse", "feature/a") != theirs {
		t.Error("force overwrote commits we had not fetched")
	}
}

func TestFirstPublishOfAProtectedBranchIsAllowed(t *testing.T) {
	bare := t.TempDir()
	testutil.Run(t, bare, "init", "-q", "--bare", "-b", "main")
	dir := testutil.NewRepo(t) // on main, with one commit
	testutil.Run(t, dir, "remote", "add", "origin", bare)
	repo := repository.New(executor.Exec{}, dir)

	if err := run(repo, domain.Default(), Options{}); err != nil {
		t.Fatalf("first publish of main: %v", err)
	}
	if got := testutil.Run(t, bare, "rev-parse", "main"); got != testutil.Run(t, dir, "rev-parse", "main") {
		t.Errorf("main was not published: %s", got)
	}

	testutil.WriteFile(t, dir, "more.txt", "m\n")
	testutil.Commit(t, dir, "feat: more")
	if err := run(repo, domain.Default(), Options{}); code(err) != apperr.CodePolicy {
		t.Fatalf("second push of main: err = %v, want policy error", err)
	}
	if err := run(repo, domain.Default(), Options{Force: true}); code(err) != apperr.CodePolicy {
		t.Fatalf("force push of main: err = %v, want policy error", err)
	}
}

func TestPushAsksTheSecurityCheckWithTheFilesBeingPushed(t *testing.T) {
	repo, dir, bare := setup(t)
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/sec")
	testutil.WriteFile(t, dir, "package.json", "{}\n")
	testutil.WriteFile(t, dir, "src/a.go", "package a\n")
	testutil.Commit(t, dir, "feat: sec")

	var got []string
	err := run(repo, domain.Default(), Options{Security: func(_ context.Context, touched []string) error {
		got = touched
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"package.json", "src/a.go"}) {
		t.Errorf("the check got %v", got)
	}
	if testutil.Run(t, bare, "rev-parse", "feature/sec") != testutil.Run(t, dir, "rev-parse", "HEAD") {
		t.Error("a check that passes must let the push through")
	}
	if os.Getenv(appsecurity.CheckedEnv) != "" {
		t.Error("the mark that tells the hook the check was done must not outlive the push")
	}
}

func TestPushStoppedBySecurityPushesNothing(t *testing.T) {
	repo, dir, bare := setup(t)
	commitOnBranch(t, dir, "feature/blocked")
	stop := apperr.New(apperr.CodePolicy, "push blocked by security level high: 1 open alert (1 critical)", "")
	err := run(repo, domain.Default(), Options{Security: func(context.Context, []string) error { return stop }})
	if code(err) != apperr.CodePolicy {
		t.Fatalf("err = %v", err)
	}
	if out := testutil.Run(t, bare, "branch", "--list", "feature/*"); out != "" {
		t.Errorf("the branch reached the remote: %q", out)
	}
	if os.Getenv(appsecurity.CheckedEnv) != "" {
		t.Error("a stopped push must not leave the mark behind")
	}
}

func TestPushSecurityRunsAfterTheProtectionAndAlsoForAForcePush(t *testing.T) {
	repo, dir, _ := setup(t) // on main, protected
	asked := false
	err := run(repo, domain.Default(), Options{Security: func(context.Context, []string) error { asked = true; return nil }})
	if code(err) != apperr.CodePolicy || asked {
		t.Errorf("protected: err=%v asked=%v: a refused push is not worth a network call", err, asked)
	}

	commitOnBranch(t, dir, "feature/f")
	if err := run(repo, domain.Default(), Options{}); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, dir, "more.txt", "m\n")
	testutil.Commit(t, dir, "feat: more")
	testutil.Run(t, dir, "commit", "-q", "--amend", "-m", "feat: more, reworded")
	asked = false
	err = run(repo, domain.Default(), Options{Force: true, Security: func(context.Context, []string) error { asked = true; return nil }})
	if err != nil || !asked {
		t.Errorf("force: err=%v asked=%v", err, asked)
	}
}

func TestPushWithoutASecurityCheckIsAsBefore(t *testing.T) {
	repo, dir, _ := setup(t)
	commitOnBranch(t, dir, "feature/plain")
	if err := run(repo, domain.Default(), Options{}); err != nil {
		t.Fatal(err)
	}
}
