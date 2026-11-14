// Package pull implements `gtide pull`: bring the remote copy of the current
// branch into it.
package pull

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cellestias/git-tide/internal/app/update"
	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/domain/branch"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

// Run fetches the branch the current branch follows (its upstream, else the
// same name on the configured remote) and integrates it: a fast-forward when
// possible, otherwise a merge or rebase per update.strategy. Like update, it
// stops before touching uncommitted work and never stashes or resets.
func Run(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
	current, err := repo.CurrentBranch(ctx)
	if errors.Is(err, repository.ErrDetachedHead) {
		return apperr.New(apperr.CodeUnsafeRepo, "HEAD is detached; there is no branch to pull", "check out a branch first: git switch <branch>")
	}
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot determine current branch", "", err)
	}
	if err := update.EnsureSafe(ctx, repo, "gtide pull"); err != nil {
		return err
	}
	remotes, err := repo.Remotes(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot list remotes", "", err)
	}

	remote, name := cfg.Remote, current
	if up := repo.Upstream(ctx); up != "" {
		if r, b, ok := strings.Cut(up, "/"); ok && branch.Contains(remotes, r) && branch.ValidName(b) == nil {
			remote, name = r, b
		}
	}
	if !branch.Contains(remotes, remote) {
		return apperr.New(apperr.CodeConfig, fmt.Sprintf("remote %q is not configured in this repository", remote),
			"add it with `gtide remote add <url>` or change `remote` in .tide.yml")
	}

	tracking := "refs/remotes/" + remote + "/" + name
	p.Step("Fetching %s/%s", remote, name)
	if err := repo.Fetch(ctx, remote, "+refs/heads/"+name+":"+tracking); err != nil {
		if exists, lsErr := repo.RemoteHasBranch(ctx, remote, name); lsErr == nil && !exists {
			return apperr.New(apperr.CodeUsage, fmt.Sprintf("%s is not on %s yet", name, remote), "publish it first with `gtide push`")
		}
		return apperr.Wrap(apperr.CodeExternal, fmt.Sprintf("cannot fetch %s/%s", remote, name), "check your network and credentials", err)
	}

	ahead, behind, err := repo.AheadBehind(ctx, tracking, "refs/heads/"+current)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot compare with the remote", "", err)
	}
	switch {
	case behind == 0 && ahead == 0:
		p.Success("%s is up to date with %s/%s", current, remote, name)
		return nil
	case behind == 0:
		p.Success("%s is %d commit%s ahead of %s/%s; nothing to pull (publish with `gtide push`)", current, ahead, plural(ahead), remote, name)
		return nil
	case ahead == 0:
		p.Step("Fast-forwarding %s to %s/%s (%d commit%s)", current, remote, name, behind, plural(behind))
		if err := repo.MergeFastForward(ctx, tracking); err != nil {
			return apperr.Wrap(apperr.CodeUnsafeRepo, "cannot fast-forward", "run `git status` and fix what it reports, then retry", err)
		}
		p.Success("Pulled %d commit%s from %s/%s", behind, plural(behind), remote, name)
		return nil
	}

	p.Step("%s and %s/%s have diverged (%d local, %d remote); integrating (%s)", current, remote, name, ahead, behind, cfg.Update.Strategy)
	if cfg.Update.Strategy == domain.StrategyRebase {
		err = repo.Rebase(ctx, tracking)
	} else {
		err = repo.Merge(ctx, tracking)
	}
	if err != nil {
		return update.IntegrationFailure(ctx, repo, cfg.Update.Strategy, err)
	}
	p.Success("Pulled %d commit%s from %s/%s", behind, plural(behind), remote, name)
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
