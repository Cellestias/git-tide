// Package resolve implements `tide continue` and `tide abort`: finish or give
// up a merge, rebase, cherry-pick, or revert that stopped on conflicts.
package resolve

import (
	"context"
	"fmt"
	"strings"

	"github.com/cellestias/git-tide/internal/app/hooks"
	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

const maxShown = 5

// Continue resumes the operation in progress once every conflict is resolved
// and staged. Tide's pre-commit checks (conflict markers, secrets) run first.
func Continue(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
	op, err := inProgress(ctx, repo, "continue")
	if err != nil {
		return err
	}
	unmerged, err := repo.UnmergedFiles(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read conflict state", "", err)
	}
	if len(unmerged) > 0 {
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("%d file(s) still have conflicts: %s", len(unmerged), list(unmerged)),
			"edit them to remove the conflict markers, then `git add <file>` and run `tide continue` again")
	}
	if err := hooks.PreCommit(ctx, repo, cfg); err != nil {
		return err
	}

	p.Step("Continuing the %s", op)
	if err := repo.ContinueOperation(ctx, op); err != nil {
		if again, _ := repo.InProgressOperation(ctx); again != "" {
			if left, _ := repo.UnmergedFiles(ctx); len(left) > 0 {
				return apperr.New(apperr.CodeGeneral, fmt.Sprintf("the %s stopped on new conflicts: %s", op, list(left)),
					"resolve them, `git add` the files, then `tide continue` again; or give up with `tide abort`")
			}
		}
		return apperr.Wrap(apperr.CodeExternal, fmt.Sprintf("cannot continue the %s", op),
			"a commit hook may have rejected it; see the message above", err)
	}
	p.Success("Finished the %s", op)
	return nil
}

type Options struct {
	// Confirm is asked before anything is discarded. Nil means "proceed".
	Confirm func(prompt string) (bool, error)
}

// Abort gives up the operation in progress and restores the state before it started.
func Abort(ctx context.Context, repo *repository.Repository, opts Options, p output.Printer) error {
	op, err := inProgress(ctx, repo, "abort")
	if err != nil {
		return err
	}
	if opts.Confirm != nil {
		ok, err := opts.Confirm(fmt.Sprintf("Abort the %s? Conflict resolutions made so far are discarded. [y/N] ", op))
		if err != nil {
			return apperr.Wrap(apperr.CodeGeneral, "cannot read confirmation", "use --yes to skip the prompt", err)
		}
		if !ok {
			return apperr.New(apperr.CodeGeneral, fmt.Sprintf("aborted; the %s is still in progress", op), "")
		}
	}
	p.Step("Aborting the %s", op)
	if err := repo.AbortOperation(ctx, op); err != nil {
		return apperr.Wrap(apperr.CodeExternal, fmt.Sprintf("cannot abort the %s", op), "run `git status` to see the state Git reports", err)
	}
	p.Success("Aborted the %s; the branch is back to how it was", op)
	return nil
}

func inProgress(ctx context.Context, repo *repository.Repository, verb string) (string, error) {
	op, err := repo.InProgressOperation(ctx)
	if err != nil {
		return "", apperr.Wrap(apperr.CodeExternal, "cannot inspect repository state", "", err)
	}
	if op == "" {
		return "", apperr.New(apperr.CodeUnsafeRepo,
			fmt.Sprintf("nothing to %s: no merge, rebase, cherry-pick or revert is in progress", verb), "")
	}
	return op, nil
}

func list(files []string) string {
	if len(files) > maxShown {
		return strings.Join(files[:maxShown], ", ") + fmt.Sprintf(" and %d more", len(files)-maxShown)
	}
	return strings.Join(files, ", ")
}
