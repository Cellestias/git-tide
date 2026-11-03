package hooks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
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
	if err := PrePush(cfg, line("refs/heads/feature/x")); err != nil {
		t.Fatal(err)
	}
	if err := PrePush(cfg, line("refs/tags/v1")); err != nil {
		t.Fatalf("tags are not branches: %v", err)
	}
	if err := PrePush(cfg, strings.NewReader("\n")); err != nil {
		t.Fatalf("blank input: %v", err)
	}
	if apperr.CodeOf(PrePush(cfg, line("refs/heads/stage"))) != apperr.CodePolicy {
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
