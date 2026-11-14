package branches

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/domain/branch"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

// DeleteOptions selects what `gtide branch delete` removes.
type DeleteOptions struct {
	// Merged adds every local branch the base already holds to the names given.
	Merged bool
	// Remote also deletes each branch on the remote (network).
	Remote bool
	// Force deletes branches the base does not hold yet: unmerged work is lost.
	Force bool
	// Confirm is asked once, with the list. Nil means "proceed".
	Confirm func(prompt string) (bool, error)
}

// baseRef is the ref to compare against: the remote copy of the base, else the local one.
func baseRef(ctx context.Context, repo *repository.Repository, cfg domain.Config, name string) (base, ref string) {
	base = cfg.BaseFor(name)
	for _, c := range []string{"refs/remotes/" + cfg.Remote + "/" + base, "refs/heads/" + base} {
		if ok, err := repo.CommitExists(ctx, c); err == nil && ok {
			return base, c
		}
	}
	return base, ""
}

// Delete removes local branches (and optionally their remote copy). It refuses
// protected branches and the checked-out one, and, without Force, any branch
// with commits its base does not have.
func Delete(ctx context.Context, repo *repository.Repository, cfg domain.Config, names []string, opts DeleteOptions, p output.Printer) error {
	current, err := repo.CurrentBranch(ctx)
	if err != nil && !errors.Is(err, repository.ErrDetachedHead) {
		return apperr.Wrap(apperr.CodeExternal, "cannot determine current branch", "", err)
	}
	all, err := repo.ListBranches(ctx, cfg.Remote, false)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot list branches", "", err)
	}
	local := map[string]bool{}
	for _, r := range all {
		if !r.Remote {
			local[r.Name] = true
		}
	}

	var targets []string
	seen := map[string]bool{}
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			targets = append(targets, n)
		}
	}
	for _, n := range names {
		if err := branch.ValidName(n); err != nil {
			return apperr.Usage("invalid branch name %q: %v", n, err)
		}
		add(n)
	}

	merged := map[string]bool{}
	for n := range local {
		if n == current || branch.Contains(cfg.Branches.Protected, n) {
			continue
		}
		if base, ref := baseRef(ctx, repo, cfg, n); ref != "" && n != base {
			if ok, err := repo.IsAncestor(ctx, "refs/heads/"+n, ref); err == nil && ok {
				merged[n] = true
			}
		}
	}
	if opts.Merged {
		for n := range merged {
			add(n)
		}
	}
	if len(targets) == 0 {
		if opts.Merged {
			p.Info("No merged branches to delete")
			return nil
		}
		return apperr.Usage("name the branches to delete, or use --merged")
	}
	sort.Strings(targets)

	for _, n := range targets {
		switch {
		case branch.Contains(cfg.Branches.Protected, n):
			return apperr.New(apperr.CodePolicy, fmt.Sprintf("%q is a protected branch and is never deleted here", n), "")
		case n == current:
			return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("%q is checked out", n), "switch to another branch first: gtide change <branch>")
		case !local[n] && !opts.Remote:
			return apperr.New(apperr.CodeUsage, fmt.Sprintf("branch %q does not exist locally", n), "to delete a remote-only branch add --remote")
		case local[n] && !merged[n] && !opts.Force:
			base, _ := baseRef(ctx, repo, cfg, n)
			return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("%q has commits that %s does not have", n, base),
				"merge it first, or delete it anyway with --force (those commits would be lost)")
		}
	}

	if opts.Remote {
		remotes, err := repo.Remotes(ctx)
		if err != nil {
			return apperr.Wrap(apperr.CodeExternal, "cannot list remotes", "", err)
		}
		if !branch.Contains(remotes, cfg.Remote) {
			return apperr.New(apperr.CodeConfig, fmt.Sprintf("remote %q is not configured", cfg.Remote), "add it with `gtide remote add <url>`")
		}
	}

	if opts.Confirm != nil {
		where := "locally"
		if opts.Remote {
			where = "locally and on " + cfg.Remote
		}
		ok, err := opts.Confirm(fmt.Sprintf("Delete %d branch%s %s: %s? [y/N] ", len(targets), plural(len(targets)), where, strings.Join(targets, ", ")))
		if err != nil {
			return apperr.Wrap(apperr.CodeGeneral, "cannot read confirmation", "use --yes to skip the prompt", err)
		}
		if !ok {
			return apperr.New(apperr.CodeGeneral, "aborted; nothing was deleted", "")
		}
	}

	for _, n := range targets {
		if local[n] {
			if err := repo.DeleteLocalBranch(ctx, n); err != nil {
				return apperr.Wrap(apperr.CodeExternal, fmt.Sprintf("cannot delete %s", n), "", err)
			}
			p.Success("Deleted %s", n)
		}
		if opts.Remote {
			if err := repo.DeleteRemoteBranch(ctx, cfg.Remote, n); err != nil {
				return apperr.Wrap(apperr.CodeExternal, fmt.Sprintf("cannot delete %s/%s", cfg.Remote, n),
					"it may not exist there, or the remote refused (protected on the provider)", err)
			}
			p.Success("Deleted %s/%s", cfg.Remote, n)
		}
	}
	return nil
}

// Rename renames a local branch. Protected branches are neither renamed nor
// the target of a rename, and the remote copy is left alone.
func Rename(ctx context.Context, repo *repository.Repository, cfg domain.Config, oldName, newName string, p output.Printer) error {
	for _, n := range []string{oldName, newName} {
		if err := branch.ValidName(n); err != nil {
			return apperr.Usage("invalid branch name %q: %v", n, err)
		}
	}
	if oldName == newName {
		return apperr.Usage("the new name is the same as the old one")
	}
	for _, n := range []string{oldName, newName} {
		if branch.Contains(cfg.Branches.Protected, n) {
			return apperr.New(apperr.CodePolicy, fmt.Sprintf("%q is a protected branch name", n), "")
		}
	}
	if ok, err := repo.LocalBranchExists(ctx, oldName); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect local branches", "", err)
	} else if !ok {
		return apperr.New(apperr.CodeUsage, fmt.Sprintf("branch %q does not exist locally", oldName), "")
	}
	if ok, err := repo.LocalBranchExists(ctx, newName); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect local branches", "", err)
	} else if ok {
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("branch %q already exists", newName), "")
	}
	if err := repo.RenameBranch(ctx, oldName, newName); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot rename the branch", "", err)
	}
	p.Success("Renamed %s to %s", oldName, newName)
	if ok, err := repo.RemoteBranchExists(ctx, cfg.Remote, oldName); err == nil && ok {
		p.Note("%s/%s still has the old name: publish the new one with `gtide push`, then `gtide branch delete --remote %s`", cfg.Remote, oldName, oldName)
	}
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
