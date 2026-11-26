// Package restore implements `gtide restore`: bring files from another branch
// or commit into the work tree, without merging anything else.
package restore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	appcommit "github.com/cellestias/git-tide/internal/app/commit"
	"github.com/cellestias/git-tide/internal/app/hooks"
	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/domain/policy"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

const maxShown = 5

// Options says what to bring and from where.
type Options struct {
	From  string   // a branch, a remote-tracking branch, a tag or a commit
	Files []string // files or folders, relative to Cwd
	Cwd   string
	// Confirm is asked before local changes are overwritten. Nil means "proceed".
	Confirm func(prompt string) (bool, error)
}

// Run replaces the files with their version in From and stages them, so
// `gtide save` commits them. Nothing else of From comes along, and a file From
// lacks is not deleted. The staged lines go through the same conflict-marker and
// secret checks as `gtide save`; a failed check leaves the files as they were.
func Run(ctx context.Context, repo *repository.Repository, cfg domain.Config, opts Options, p output.Printer) error {
	current, err := repo.CurrentBranch(ctx)
	if errors.Is(err, repository.ErrDetachedHead) {
		return apperr.New(apperr.CodeUnsafeRepo, "HEAD is detached; there is no branch to put the files on", "check out a branch first")
	} else if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot determine current branch", "", err)
	}
	if d := (policy.Protection{Protected: cfg.Branches.Protected}).Commit(current); !d.Allowed {
		return apperr.New(apperr.CodePolicy, d.Reason, "work on a branch of your own (`gtide create <branch>`); a protected branch only takes commits that come through a Pull Request")
	}
	if op, err := repo.InProgressOperation(ctx); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect repository state", "", err)
	} else if op != "" {
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("a %s is in progress", op), "finish it with `gtide continue` or give it up with `gtide abort` first")
	}

	if strings.TrimSpace(opts.From) == "" {
		return apperr.Usage("say where the files come from with --from <branch|tag|commit>")
	}
	from, err := repo.ResolveCommit(ctx, opts.From)
	if err != nil || from == "" {
		return apperr.Usage("%q is not a commit, a branch or a tag of this repository", opts.From)
	}
	paths, err := appcommit.ResolvePaths(repo.Root, opts.Cwd, opts.Files)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return apperr.Usage("give the files to bring with -f <file> (repeatable; a folder takes everything under it)")
	}
	var missing []string
	for _, path := range paths {
		if ok, err := repo.PathInCommit(ctx, from, path); err != nil {
			return apperr.Wrap(apperr.CodeExternal, "cannot read "+opts.From, "", err)
		} else if !ok {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		return apperr.New(apperr.CodeUsage, fmt.Sprintf("%s has no %s", opts.From, list(missing)),
			"a file that the other side lacks is not deleted here; remove it with `git rm`")
	}

	if opts.Confirm != nil {
		entries, err := repo.Status(ctx)
		if err != nil {
			return apperr.Wrap(apperr.CodeExternal, "cannot read working tree status", "", err)
		}
		var at []string
		for _, e := range entries {
			if covered(e.Path, paths) {
				at = append(at, e.Path)
			}
		}
		if len(at) > 0 {
			ok, err := opts.Confirm(fmt.Sprintf("Overwrite the local changes in %s with the version from %s? [y/N] ", list(at), opts.From))
			if err != nil {
				return apperr.Wrap(apperr.CodeGeneral, "cannot read confirmation", "use --yes to skip the prompt", err)
			}
			if !ok {
				return apperr.New(apperr.CodeGeneral, "aborted; the files were not changed", "")
			}
		}
	}

	p.Step("Bringing %s from %s", list(paths), opts.From)
	snapshot, err := repo.IndexSnapshot(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot snapshot the index", "", err)
	}
	undo := func() { _ = repo.RestoreIndex(ctx, snapshot) }
	if err := repo.StageFromCommit(ctx, from, paths); err != nil {
		undo()
		return apperr.Wrap(apperr.CodeExternal, "cannot read the files from "+opts.From, "", err)
	}
	added, err := repo.StagedAdditions(ctx)
	if err != nil {
		undo()
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect staged changes", "", err)
	}
	if err := hooks.CheckAdditions(cfg, added, "the files from "+opts.From); err != nil {
		undo()
		p.Note("nothing was changed")
		return err
	}
	if err := repo.CheckoutFromIndex(ctx, paths); err != nil {
		undo()
		return apperr.Wrap(apperr.CodeExternal, "cannot write the files", "", err)
	}
	p.Success("Brought %s from %s; the changes are staged", list(paths), opts.From)
	p.Note("commit them with `gtide save`")
	return nil
}

// covered reports whether path is one of the requested paths or under one.
func covered(path string, paths []string) bool {
	for _, p := range paths {
		if p == "." || path == p || strings.HasPrefix(path, strings.TrimSuffix(p, "/")+"/") {
			return true
		}
	}
	return false
}

func list(items []string) string {
	if len(items) > maxShown {
		return strings.Join(items[:maxShown], ", ") + fmt.Sprintf(" and %d more", len(items)-maxShown)
	}
	return strings.Join(items, ", ")
}
