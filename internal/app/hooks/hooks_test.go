package hooks

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	appsecurity "github.com/cellestias/git-tide/internal/app/security"
	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	sec "github.com/cellestias/git-tide/internal/domain/security"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/testutil"
)

func TestCommitMsg(t *testing.T) {
	cfg := domain.Default()
	write := func(msg string) string {
		p := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
		if err := os.WriteFile(p, []byte(msg), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if err := CommitMsg(cfg, write("feat: ok\n")); err != nil {
		t.Fatal(err)
	}
	err := CommitMsg(cfg, write("nope\n"))
	if apperr.CodeOf(err) != apperr.CodePolicy || !strings.Contains(apperr.HintOf(err), "feat:") {
		t.Fatalf("err=%v hint=%q", err, apperr.HintOf(err))
	}
	if apperr.CodeOf(CommitMsg(cfg, "/nonexistent/file")) != apperr.CodeGeneral {
		t.Error("missing file should be a general error")
	}
}

func TestPrePush(t *testing.T) {
	cfg := domain.Default()
	cfg.Branches.Protected = []string{"main", "stage"}
	zero := strings.Repeat("0", 40)
	line := func(remoteRef string) *strings.Reader {
		return strings.NewReader("refs/heads/x " + zero + " " + remoteRef + " " + zero + "\n")
	}
	if err := PrePush(context.Background(), nil, cfg, "", line("refs/heads/feature/x"), nil); err != nil {
		t.Fatal(err)
	}
	if err := PrePush(context.Background(), nil, cfg, "", line("refs/tags/v1"), nil); err != nil {
		t.Fatalf("tags are not branches: %v", err)
	}
	if err := PrePush(context.Background(), nil, cfg, "", strings.NewReader("\n"), nil); err != nil {
		t.Fatalf("blank input: %v", err)
	}
	if apperr.CodeOf(PrePush(context.Background(), nil, cfg, "", line("refs/heads/stage"), nil)) != apperr.CodePolicy {
		t.Error("push to stage should be rejected")
	}
}

func TestPreCommit(t *testing.T) {
	ctx := context.Background()
	dir := testutil.NewRepo(t)
	repo, _ := repository.Open(ctx, executor.Exec{}, dir)
	cfg := domain.Default()

	// protected branch
	if apperr.CodeOf(PreCommit(ctx, repo, cfg)) != apperr.CodePolicy {
		t.Error("commit on main should be rejected")
	}

	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/x")
	secretLine := "token = " + "ghp_" + strings.Repeat("a", 36)
	testutil.WriteFile(t, dir, "ok.txt", "fine\n")
	testutil.WriteFile(t, dir, "cfg.env", "a=1\n"+secretLine+"\n")
	testutil.Run(t, dir, "add", "-A")

	err := PreCommit(ctx, repo, cfg)
	if apperr.CodeOf(err) != apperr.CodePolicy {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "cfg.env:2 (github_token)") {
		t.Errorf("finding location missing: %v", err)
	}
	if strings.Contains(err.Error(), "ghp_") || strings.Contains(apperr.HintOf(err), "ghp_") {
		t.Error("secret value leaked into output")
	}

	cfg.Secrets.Enabled = false
	if err := PreCommit(ctx, repo, cfg); err != nil {
		t.Errorf("scanning disabled: %v", err)
	}
}

func TestPreCommitRejectsConflictMarkers(t *testing.T) {
	ctx := context.Background()
	dir := testutil.NewRepo(t)
	repo, _ := repository.Open(ctx, executor.Exec{}, dir)
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/x")

	testutil.WriteFile(t, dir, "a.go", "package a\n<<<<<<< HEAD\nx := 1\n=======\nx := 2\n>>>>>>> feature/y\n")
	testutil.WriteFile(t, dir, "doc.md", "Title\n=======\n") // heading underline is fine
	testutil.Run(t, dir, "add", "-A")

	cfg := domain.Default()
	cfg.Secrets.Enabled = false // markers are checked even when secret scanning is off
	err := PreCommit(ctx, repo, cfg)
	if apperr.CodeOf(err) != apperr.CodePolicy || !strings.Contains(err.Error(), "a.go:2") || !strings.Contains(err.Error(), "a.go:6") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "doc.md") {
		t.Errorf("heading underline flagged: %v", err)
	}

	testutil.WriteFile(t, dir, "a.go", "package a\n<<<<<<< HEAD tide:allow-conflict-marker\n")
	testutil.Run(t, dir, "add", "-A")
	if err := PreCommit(ctx, repo, cfg); err != nil {
		t.Errorf("allow marker ignored: %v", err)
	}
}

func TestPreCommitAllowsTheFirstCommitOfANewRepository(t *testing.T) {
	ctx := context.Background()
	dir := testutil.NewEmptyRepo(t) // main is protected, but there is no history to protect yet
	repo, _ := repository.Open(ctx, executor.Exec{}, dir)
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	testutil.Run(t, dir, "add", "-A")
	if err := PreCommit(ctx, repo, domain.Default()); err != nil {
		t.Fatalf("first commit: %v", err)
	}
}

func TestPrePushAllowsCreatingAProtectedBranchOnTheRemote(t *testing.T) {
	cfg := domain.Default()
	zero := strings.Repeat("0", 40)
	sha := strings.Repeat("a", 40)
	create := strings.NewReader("refs/heads/main " + sha + " refs/heads/main " + zero + "\n")
	if err := PrePush(context.Background(), nil, cfg, "", create, nil); err != nil {
		t.Fatalf("creating main: %v", err)
	}
	update := strings.NewReader("refs/heads/main " + sha + " refs/heads/main " + strings.Repeat("b", 40) + "\n")
	if apperr.CodeOf(PrePush(context.Background(), nil, cfg, "", update, nil)) != apperr.CodePolicy {
		t.Error("updating main should be rejected")
	}
	del := strings.NewReader("(delete) " + zero + " refs/heads/main " + sha + "\n")
	if apperr.CodeOf(PrePush(context.Background(), nil, cfg, "", del, nil)) != apperr.CodePolicy {
		t.Error("deleting main should be rejected")
	}
}

// gateProbe records how the pre-push hook called the security gate.
type gateProbe struct {
	calls   int
	stage   sec.Stage
	touched []string
	err     error
}

func (g *gateProbe) gate() appsecurity.Gate {
	return func(_ context.Context, stage sec.Stage, touched []string) error {
		g.calls++
		g.stage, g.touched = stage, touched
		return g.err
	}
}

func pushedBranch(t *testing.T) (*repository.Repository, string) {
	t.Helper()
	dir, _ := testutil.NewRepoWithRemote(t)
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/x")
	testutil.WriteFile(t, dir, "package.json", "{}\n")
	testutil.WriteFile(t, dir, "src/a.go", "package a\n")
	testutil.Commit(t, dir, "feat: x")
	return repository.New(executor.Exec{}, dir), testutil.Run(t, dir, "rev-parse", "HEAD")
}

func TestPrePushChecksTheAlertsOnceWithTheFilesBeingPushed(t *testing.T) {
	repo, sha := pushedBranch(t)
	zero := strings.Repeat("0", 40)
	in := strings.NewReader("refs/heads/feature/x " + sha + " refs/heads/feature/x " + zero + "\n" +
		"refs/heads/feature/x " + sha + " refs/heads/other " + zero + "\n" + // a second ref: still one check
		"refs/tags/v1 " + sha + " refs/tags/v1 " + zero + "\n")
	var probe gateProbe
	if err := PrePush(t.Context(), repo, domain.Default(), "origin", in, probe.gate()); err != nil {
		t.Fatal(err)
	}
	if probe.calls != 1 || probe.stage != sec.StagePush {
		t.Fatalf("calls=%d stage=%d, want one check at the push stage", probe.calls, probe.stage)
	}
	sort.Strings(probe.touched)
	if !reflect.DeepEqual(probe.touched, []string{"package.json", "src/a.go"}) {
		t.Errorf("touched = %v (each file once)", probe.touched)
	}
}

func TestPrePushStopsWhenTheGateDoes(t *testing.T) {
	repo, sha := pushedBranch(t)
	probe := gateProbe{err: apperr.New(apperr.CodePolicy, "push blocked by security level high", "")}
	in := strings.NewReader("refs/heads/feature/x " + sha + " refs/heads/feature/x " + strings.Repeat("0", 40) + "\n")
	if err := PrePush(t.Context(), repo, domain.Default(), "origin", in, probe.gate()); apperr.CodeOf(err) != apperr.CodePolicy {
		t.Fatalf("err = %v", err)
	}
}

func TestPrePushDoesNotAskWhenThereIsNothingToCheck(t *testing.T) {
	repo, sha := pushedBranch(t)
	zero := strings.Repeat("0", 40)
	for name, in := range map[string]string{
		"a tag":            "refs/tags/v1 " + sha + " refs/tags/v1 " + zero + "\n",
		"a deleted ref":    "(delete) " + zero + " refs/heads/feature/x " + sha + "\n",
		"nothing":          "\n",
		"a malformed line": "refs/heads/feature/x\n",
	} {
		var probe gateProbe
		if err := PrePush(t.Context(), repo, domain.Default(), "origin", strings.NewReader(in), probe.gate()); err != nil || probe.calls != 0 {
			t.Errorf("%s: err=%v calls=%d", name, err, probe.calls)
		}
	}
	// and the protection comes first: a refused push is not worth a network call
	var probe gateProbe
	in := strings.NewReader("refs/heads/main " + sha + " refs/heads/main " + strings.Repeat("b", 40) + "\n")
	if err := PrePush(t.Context(), repo, domain.Default(), "origin", in, probe.gate()); apperr.CodeOf(err) != apperr.CodePolicy || probe.calls != 0 {
		t.Errorf("protected: err=%v calls=%d", err, probe.calls)
	}
}

func TestPrePushSkipsTheCheckThatGtidePushJustMade(t *testing.T) {
	repo, sha := pushedBranch(t)
	line := "refs/heads/feature/x " + sha + " refs/heads/feature/x " + strings.Repeat("0", 40) + "\n"
	var probe gateProbe
	undo := appsecurity.MarkChecked()
	if err := PrePush(t.Context(), repo, domain.Default(), "origin", strings.NewReader(line), probe.gate()); err != nil || probe.calls != 0 {
		t.Fatalf("marked: err=%v calls=%d", err, probe.calls)
	}
	undo()
	if appsecurity.AlreadyChecked() {
		t.Fatal("the mark must be removed")
	}
	if err := PrePush(t.Context(), repo, domain.Default(), "origin", strings.NewReader(line), probe.gate()); err != nil || probe.calls != 1 {
		t.Fatalf("unmarked: err=%v calls=%d", err, probe.calls)
	}
}

func TestPrePushWithoutAGateIsAsBefore(t *testing.T) {
	repo, sha := pushedBranch(t)
	in := strings.NewReader("refs/heads/feature/x " + sha + " refs/heads/feature/x " + strings.Repeat("0", 40) + "\n")
	if err := PrePush(t.Context(), repo, domain.Default(), "origin", in, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPrePushAUrlCountsEveryRemote(t *testing.T) {
	repo, sha := pushedBranch(t)
	in := "refs/heads/feature/x " + sha + " refs/heads/feature/x " + strings.Repeat("0", 40) + "\n"
	var probe gateProbe
	if err := PrePush(t.Context(), repo, domain.Default(), "https://github.com/acme/tool.git", strings.NewReader(in), probe.gate()); err != nil {
		t.Fatal(err)
	}
	if probe.calls != 1 || len(probe.touched) != 2 {
		t.Errorf("calls=%d touched=%v", probe.calls, probe.touched)
	}
}
