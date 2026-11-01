// Package update implements `tide update`.
package update

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/domain/branch"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

type Options struct {
	// Target overrides the base branch resolved from configuration.
	Target string
}

// Run updates the base branch from the remote and integrates it into the
// current branch. It never stashes, resets, or discards work: if the working
// tree could be affected it stops before doing anything.
func Run(ctx context.Context, repo *repository.Repository, cfg domain.Config, opts Options, p output.Printer) error {
	current, err := repo.CurrentBranch(ctx)
	if errors.Is(err, repository.ErrDetachedHead) {
		return apperr.New(apperr.CodeUnsafeRepo, "HEAD is detached; nothing to update", "check out a branch first: git switch <branch>")
	}
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot determine current branch", "", err)
	}

	base := opts.Target
	if base == "" {
		base = cfg.BaseFor(current)
	} else if err := branch.ValidName(base); err != nil {
		return apperr.Usage("invalid base branch: %v", err)
	}
	remote := cfg.Remote

	if err := ensureSafe(ctx, repo); err != nil {
		return err
	}
	remotes, err := repo.Remotes(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot list remotes", "", err)
	}
	if !branch.Contains(remotes, remote) {
		return apperr.New(apperr.CodeConfig, fmt.Sprintf("remote %q is not configured in this repository", remote),
			"add it with `git remote add` or change `remote` in .tide.yml")
	}

	trackingRef := "refs/remotes/" + remote + "/" + base
	p.Step("Fetching %s/%s", remote, base)
	if err := repo.Fetch(ctx, remote, "+refs/heads/"+base+":"+trackingRef); err != nil {
		return apperr.Wrap(apperr.CodeExternal, fmt.Sprintf("cannot fetch %s/%s", remote, base), "check the branch name, your network, and credentials", err)
	}

	before, _ := repo.Head(ctx)

	if current == base {
		p.Step("Fast-forwarding %s to %s/%s", base, remote, base)
		if err := repo.MergeFastForward(ctx, trackingRef); err != nil {
			return apperr.Wrap(apperr.CodeUnsafeRepo, fmt.Sprintf("local %s has diverged from %s/%s", base, remote, base),
				"integrate manually: git merge or git rebase "+remote+"/"+base, err)
		}
		return report(ctx, repo, p, before, base)
	}

	p.Step("Updating local %s", base)
	if err := repo.UpdateBranchFromRef(ctx, base, trackingRef); err != nil {
		return apperr.Wrap(apperr.CodeUnsafeRepo,
			fmt.Sprintf("cannot fast-forward local %s to %s/%s", base, remote, base),
			"it may have local commits or be checked out in another worktree; fix it manually, then re-run", err)
	}

	p.Step("Integrating %s into %s (%s)", base, current, cfg.Update.Strategy)
	if cfg.Update.Strategy == domain.StrategyRebase {
		err = repo.Rebase(ctx, "refs/heads/"+base)
	} else {
		err = repo.Merge(ctx, "refs/heads/"+base)
	}
	if err != nil {
		return integrationFailure(ctx, repo, cfg.Update.Strategy, err)
	}
	return report(ctx, repo, p, before, base)
}

func ensureSafe(ctx context.Context, repo *repository.Repository) error {
	op, err := repo.InProgressOperation(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect repository state", "", err)
	}
	if op != "" {
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("a %s is in progress", op),
			fmt.Sprintf("finish or abort it first (git %s --continue / --abort)", op))
	}
	changes, err := repo.TrackedChanges(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read working tree status", "", err)
	}
	if len(changes) > 0 {
		shown := changes
		if len(shown) > 5 {
			shown = shown[:5]
		}
		msg := "uncommitted changes would be at risk: " + strings.Join(shown, ", ")
		if len(changes) > len(shown) {
			msg += fmt.Sprintf(" and %d more", len(changes)-len(shown))
		}
		return apperr.New(apperr.CodeUnsafeRepo, msg, "commit or stash them, then re-run `tide update`")
	}
	return nil
}

func integrationFailure(ctx context.Context, repo *repository.Repository, strategy string, cause error) error {
	op, _ := repo.InProgressOperation(ctx)
	if op != "" {
		verb := op
		return apperr.New(apperr.CodeGeneral, fmt.Sprintf("conflicts while integrating the base branch (%s)", strategy),
			fmt.Sprintf("resolve the conflicts, git add the files, then `git %[1]s --continue`; or give up with `git %[1]s --abort`", verb))
	}
	return apperr.Wrap(apperr.CodeExternal, "integration failed", "the repository was left as Git reports it; run `git status`", cause)
}

func report(ctx context.Context, repo *repository.Repository, p output.Printer, before, base string) error {
	after, _ := repo.Head(ctx)
	if before != "" && before == after {
		p.Success("Already up to date with %s", base)
		return nil
	}
	p.Success("Updated to include %s", base)
	return nil
}
