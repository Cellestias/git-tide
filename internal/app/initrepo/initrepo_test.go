package initrepo

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cellestias/git-tide/internal/config"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/hooks"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/testutil"
)

func open(t *testing.T, dir string) *repository.Repository {
	t.Helper()
	repo, err := repository.Open(context.Background(), executor.Exec{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestInitCreatesValidConfigAndHooks(t *testing.T) {
	dir := testutil.NewRepo(t)
	res, err := Run(context.Background(), open(t, dir), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.ConfigCreated || res.Base != "main" {
		t.Fatalf("res = %+v", res)
	}
	loaded, err := config.Load(dir)
	if err != nil || !loaded.Found {
		t.Fatalf("generated config invalid: %v", err)
	}
	if len(res.HookChanges) != len(hooks.Managed) {
		t.Errorf("hooks = %+v", res.HookChanges)
	}
}

func TestInitDoesNotOverwriteConfig(t *testing.T) {
	dir := testutil.NewRepo(t)
	custom := "version: 1\nbranches:\n  default_base: stage\n  protected: [stage]\n"
	testutil.WriteFile(t, dir, config.FileName, custom)
	res, err := Run(context.Background(), open(t, dir), Options{Base: "other"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, config.FileName))
	if res.ConfigCreated || string(got) != custom {
		t.Fatalf("config overwritten: created=%v content=%q", res.ConfigCreated, got)
	}
}

func TestInitBaseSelectionAndNoHooks(t *testing.T) {
	dir := testutil.NewRepo(t)
	res, err := Run(context.Background(), open(t, dir), Options{Base: "stage", NoHooks: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Base != "stage" || !res.HooksSkipped || len(res.HookChanges) != 0 {
		t.Fatalf("res = %+v", res)
	}
	if _, err := Run(context.Background(), open(t, testutil.NewRepo(t)), Options{Base: "-bad"}); err == nil {
		t.Error("invalid base accepted")
	}
}

func TestInitDetectsRemoteDefaultBranch(t *testing.T) {
	dir, _ := testutil.NewRepoWithRemote(t)
	testutil.Run(t, dir, "remote", "set-head", "origin", "main")
	res, err := Run(context.Background(), open(t, dir), Options{NoHooks: true})
	if err != nil || res.Base != "main" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
