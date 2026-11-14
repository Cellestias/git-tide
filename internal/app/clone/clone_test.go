package clone

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cellestias/git-tide/internal/app/initrepo"
	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/testutil"
)

func TestDirName(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/o/app.git":   "app",
		"https://github.com/o/app":       "app",
		"https://github.com/o/app.git/":  "app",
		"git@github.com:o/app.git":       "app",
		"git@gitlab.com:group/sub/x.git": "x",
		"host:app.git":                   "app",
		"/srv/git/app.git":               "app",
	} {
		if got := DirName(url); got != want {
			t.Errorf("DirName(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestRunClonesAndInits(t *testing.T) {
	_, bare := testutil.NewRepoWithRemote(t)
	parent := t.TempDir()
	res, err := Run(t.Context(), executor.Exec{}, parent, bare, "", initrepo.Options{NoHooks: true})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(parent, DirName(bare))
	if res.Dir != want {
		t.Fatalf("dir = %q, want %q", res.Dir, want)
	}
	for _, f := range []string{"README.md", ".tide.yml", "secret.tide.yml", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(res.Dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if !res.Init.ConfigCreated || res.Init.Base != "main" {
		t.Errorf("init = %+v", res.Init)
	}
}

func TestRunRefusesANonEmptyFolderAndBadInput(t *testing.T) {
	_, bare := testutil.NewRepoWithRemote(t)
	parent := t.TempDir()
	testutil.WriteFile(t, parent, "taken/file.txt", "x\n")
	if _, err := Run(t.Context(), executor.Exec{}, parent, bare, "taken", initrepo.Options{}); apperr.CodeOf(err) != apperr.CodeUnsafeRepo {
		t.Errorf("non-empty folder: err = %v, want unsafe repo", err)
	}
	for _, url := range []string{"ext::sh -c id", "--upload-pack=x", ""} {
		if _, err := Run(t.Context(), executor.Exec{}, parent, url, "dest", initrepo.Options{}); apperr.CodeOf(err) != apperr.CodeUsage {
			t.Errorf("url %q: err = %v, want usage error", url, err)
		}
	}
	if _, err := Run(t.Context(), executor.Exec{}, parent, bare, "..", initrepo.Options{}); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("folder ..: err = %v, want usage error", err)
	}
}

func TestRunKeepsACommittedConfig(t *testing.T) {
	clone, bare := testutil.NewRepoWithRemote(t)
	testutil.WriteFile(t, clone, ".tide.yml", "version: 1\nbranches:\n  default_base: main\n  protected: [main, stage]\n")
	testutil.Commit(t, clone, "chore: add tide config")
	testutil.Run(t, clone, "push", "-q")
	parent := t.TempDir()
	res, err := Run(t.Context(), executor.Exec{}, parent, bare, "copy", initrepo.Options{NoHooks: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Init.ConfigCreated {
		t.Error("a committed .tide.yml must be kept")
	}
}
