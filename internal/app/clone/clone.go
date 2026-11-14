// Package clone implements `gtide clone`: git clone, then gtide init in the copy.
package clone

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cellestias/git-tide/internal/app/initrepo"
	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/domain/remote"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
)

// DirName is the folder Git would clone url into: the last path segment
// without a trailing ".git".
func DirName(url string) string {
	u := strings.TrimRight(strings.TrimSpace(url), "/\\")
	if i := strings.LastIndexAny(u, "/:\\"); i >= 0 {
		u = u[i+1:]
	}
	return strings.TrimSuffix(u, ".git")
}

// Result is what Run did: the folder, and the outcome of init in it.
type Result struct {
	Dir  string
	Init initrepo.Result
}

// Run clones url into target (default: DirName(url), under dir) and runs init in
// the copy. A target that exists and is not empty is refused before Git runs.
func Run(ctx context.Context, ex executor.Executor, dir, url, target string, opts initrepo.Options) (Result, error) {
	if err := remote.ValidURL(url); err != nil {
		return Result{}, apperr.Usage("%v", err)
	}
	if target == "" {
		target = DirName(url)
	}
	if target == "" || target == "." || target == ".." || strings.HasPrefix(target, "-") {
		return Result{}, apperr.Usage("cannot tell where to clone %q; give the folder name", url)
	}
	abs := target
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(dir, target)
	}
	if entries, err := os.ReadDir(abs); err == nil && len(entries) > 0 {
		return Result{}, apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("%s already exists and is not empty", abs), "choose another folder: gtide clone <url> <folder>")
	}

	if err := repository.Clone(ctx, ex, dir, url, abs); err != nil {
		return Result{}, apperr.Wrap(apperr.CodeExternal, "cannot clone the repository",
			"check the URL, your network and your credentials", err)
	}
	repo, err := repository.Open(ctx, ex, abs)
	if err != nil {
		return Result{Dir: abs}, apperr.Wrap(apperr.CodeExternal, "cannot open the clone", "", err)
	}
	res, err := initrepo.Run(ctx, repo, opts)
	return Result{Dir: abs, Init: res}, err
}
