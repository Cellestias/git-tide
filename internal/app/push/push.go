// Package push implements `gtide push`: publish the current branch and set its
// upstream, refusing protected branches.
package push

import (
	"context"
	"errors"
	"fmt"

	appsecurity "github.com/cellestias/git-tide/internal/app/security"
	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/domain/policy"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

type Options struct {
	// Force allows rewriting the remote branch, guarded by a lease.
	Force bool
	// Confirm is asked before a forced push. Nil means "proceed".
	Confirm func(prompt string) (bool, error)
	// Security is the alerts check. It gets the files the push changes and may
	// stop it. Nil means no check.
	Security func(ctx context.Context, touched []string) error
}

func Run(ctx context.Context, repo *repository.Repository, cfg domain.Config, opts Options, p output.Printer) error {
	current, err := repo.CurrentBranch(ctx)
	if errors.Is(err, repository.ErrDetachedHead) {
		return apperr.New(apperr.CodeUnsafeRepo, "HEAD is detached; there is no branch to push",
			"create one first: git switch -c <branch>")
	}
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot determine current branch", "", err)
	}
	remotes, err := repo.Remotes(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot list remotes", "", err)
	}
	if !has(remotes, cfg.Remote) {
		return apperr.New(apperr.CodeConfig, fmt.Sprintf("remote %q is not configured", cfg.Remote),
			"add it with `gtide remote add <url>` or change `remote` in .tide.yml")
	}
	if d := (policy.Protection{Protected: cfg.Branches.Protected}).Push(current); !d.Allowed {
		// The first publish of a protected branch (it is not on the remote yet) is
		// how a new project starts; any later push to it is refused.
		exists, err := repo.RemoteHasBranch(ctx, cfg.Remote, current)
		if err != nil || exists || opts.Force {
			return apperr.New(apperr.CodePolicy, d.Reason, "")
		}
		p.Note("%s is not on %s yet: publishing it for the first time", current, cfg.Remote)
	}

	if changes, _ := repo.TrackedChanges(ctx); len(changes) > 0 {
		p.Warn("%d file(s) have uncommitted changes; they are not pushed", len(changes))
	}

	if opts.Security != nil {
		touched, err := repo.PushedPaths(ctx, cfg.Remote, "refs/heads/"+current)
		if err != nil {
			touched = nil // unknown files: nothing is exempt
		}
		if err := opts.Security(ctx, touched); err != nil {
			return err
		}
		defer appsecurity.MarkChecked()() // the pre-push hook would ask the same again
	}

	if opts.Force {
		if opts.Confirm != nil {
			ok, err := opts.Confirm(fmt.Sprintf("Force-push %s to %s, replacing its remote history? [y/N] ", current, cfg.Remote))
			if err != nil {
				return apperr.Wrap(apperr.CodeGeneral, "cannot read confirmation", "use --yes to skip the prompt", err)
			}
			if !ok {
				return apperr.New(apperr.CodeGeneral, "aborted; nothing was pushed", "")
			}
		}
		p.Step("Force-pushing %s to %s (with lease)", current, cfg.Remote)
		if err := repo.PushBranchWithLease(ctx, cfg.Remote, current); err != nil {
			return apperr.Wrap(apperr.CodeExternal, "push failed",
				"the remote branch has commits you have not fetched, or a hook rejected it; fetch, review, and retry", err)
		}
	} else {
		p.Step("Pushing %s to %s", current, cfg.Remote)
		if err := repo.PushBranch(ctx, cfg.Remote, current); err != nil {
			return apperr.Wrap(apperr.CodeExternal, "push failed",
				"if the remote has new commits, run `gtide update` first; use --force only after rewriting history", err)
		}
	}
	p.Success("Pushed %s to %s and set it as upstream", current, cfg.Remote)
	return nil
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
