// Package create implements `gtide create`: start a new branch from an
// up-to-date base.
package create

import (
	"context"
	"fmt"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/domain/branch"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

type Options struct {
	// From is the start point: a branch, tag, or commit. Default: the base
	// branch resolved from configuration.
	From string
	// Local starts from the local branch and makes no network call.
	Local bool
	// Track creates the branch from an existing remote branch of the same
	// name and sets its upstream.
	Track bool
	// Push pushes the new branch and sets its upstream.
	Push bool
	// NoSwitch creates the branch without checking it out.
	NoSwitch bool
}

func Run(ctx context.Context, repo *repository.Repository, cfg domain.Config, name string, opts Options, p output.Printer) error {
	if err := branch.ValidName(name); err != nil {
		return apperr.Usage("invalid branch name: %v", err)
	}
	if err := checkOptions(opts); err != nil {
		return err
	}
	if opts.From != "" {
		if err := branch.ValidName(opts.From); err != nil {
			return apperr.Usage("invalid --from: %v", err)
		}
	}
	if branch.Contains(cfg.Branches.Protected, name) {
		return apperr.New(apperr.CodePolicy, fmt.Sprintf("%q is a protected branch name", name), "")
	}
	remote := cfg.Remote

	if err := ensureSafe(ctx, repo, !opts.NoSwitch); err != nil {
		return err
	}
	if exists, err := repo.LocalBranchExists(ctx, name); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect local branches", "", err)
	} else if exists {
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("branch %q already exists locally", name),
			fmt.Sprintf("switch to it with `git switch %s`", name))
	}

	start, track, err := resolveStart(ctx, repo, cfg, name, opts, p)
	if err != nil {
		return err
	}

	if err := repo.CreateBranch(ctx, name, start.ref, track, !opts.NoSwitch); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot create the branch", "", err)
	}
	verb := "Created"
	if !opts.NoSwitch {
		verb = "Created and switched to"
	}
	p.Success("%s %s from %s", verb, name, start.label)

	if opts.Push {
		p.Step("Pushing %s to %s", name, remote)
		if err := repo.PushBranch(ctx, remote, name); err != nil {
			return apperr.Wrap(apperr.CodeExternal, "push failed", "the branch exists locally; push it later with `git push -u "+remote+" "+name+"`", err)
		}
		p.Success("Pushed %s/%s and set it as upstream", remote, name)
	}
	return nil
}

func checkOptions(opts Options) error {
	if opts.Track && opts.From != "" {
		return apperr.Usage("--track and --from cannot be combined: --track starts from the remote branch of the same name")
	}
	if opts.Track && opts.Local {
		return apperr.Usage("--track needs the network; it cannot be combined with --local")
	}
	if opts.Track && opts.Push {
		return apperr.Usage("--push is not needed with --track: the branch already exists on the remote")
	}
	return nil
}

type startPoint struct {
	ref   string // what to pass to git
	label string // for display
}

func resolveStart(ctx context.Context, repo *repository.Repository, cfg domain.Config, name string, opts Options, p output.Printer) (startPoint, bool, error) {
	remote := cfg.Remote
	ext := func(msg, hint string, err error) error { return apperr.Wrap(apperr.CodeExternal, msg, hint, err) }

	if opts.Track {
		p.Step("Checking %s for %s", remote, name)
		ok, err := repo.RemoteHasBranch(ctx, remote, name)
		if err != nil {
			return startPoint{}, false, ext("cannot reach "+remote, "check your network and credentials", err)
		}
		if !ok {
			return startPoint{}, false, apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("%s/%s does not exist", remote, name),
				"create the branch without --track, or check the name")
		}
		ref := "refs/remotes/" + remote + "/" + name
		if err := repo.Fetch(ctx, remote, "+refs/heads/"+name+":"+ref); err != nil {
			return startPoint{}, false, ext(fmt.Sprintf("cannot fetch %s/%s", remote, name), "", err)
		}
		return startPoint{ref, remote + "/" + name}, true, nil
	}

	base := opts.From
	fromConfig := base == ""
	if fromConfig {
		base = cfg.BaseFor(name)
		p.Info("base: %s (from .tide.yml)", base)
	}
	trackingRef := "refs/remotes/" + remote + "/" + base

	if opts.Local {
		for _, c := range []startPoint{
			{"refs/heads/" + base, base},
			{trackingRef, remote + "/" + base + " (not refreshed)"},
		} {
			ok, err := repo.CommitExists(ctx, c.ref)
			if err != nil {
				return startPoint{}, false, ext("cannot resolve "+base, "", err)
			}
			if ok {
				return c, false, nil
			}
		}
		return startPoint{}, false, notFound(base, fromConfig)
	}

	// A remote branch is fetched fresh. An explicit --from that is not a remote
	// branch may still be a tag, a commit, or a local-only branch.
	if !fromConfig {
		p.Step("Checking %s for %s", remote, base)
		ok, err := repo.RemoteHasBranch(ctx, remote, base)
		if err != nil {
			return startPoint{}, false, ext("cannot reach "+remote, "check your network, or use --local", err)
		}
		if !ok {
			ok, err := repo.CommitExists(ctx, base)
			if err != nil {
				return startPoint{}, false, ext("cannot resolve "+base, "", err)
			}
			if !ok {
				return startPoint{}, false, notFound(base, false)
			}
			return startPoint{base, base + " (local)"}, false, nil
		}
	}
	p.Step("Fetching %s/%s", remote, base)
	if err := repo.Fetch(ctx, remote, "+refs/heads/"+base+":"+trackingRef); err != nil {
		return startPoint{}, false, ext(fmt.Sprintf("cannot fetch %s/%s", remote, base),
			"check the branch name, your network, and credentials; or use --local", err)
	}
	return startPoint{trackingRef, remote + "/" + base}, false, nil
}

func notFound(base string, fromConfig bool) error {
	hint := "check the name passed to --from"
	if fromConfig {
		hint = "check branches.default_base / branches.base in .tide.yml, or pass --from"
	}
	return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("cannot find %q to start from", base), hint)
}

// ensureSafe refuses states where switching branches could clobber work.
func ensureSafe(ctx context.Context, repo *repository.Repository, switching bool) error {
	op, err := repo.InProgressOperation(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect repository state", "", err)
	}
	if op != "" {
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("a %s is in progress", op),
			fmt.Sprintf("finish or abort it first (git %s --continue / --abort)", op))
	}
	if !switching {
		return nil
	}
	changes, err := repo.TrackedChanges(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read working tree status", "", err)
	}
	if len(changes) > 0 {
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("%d file(s) have uncommitted changes", len(changes)),
			"commit or stash them first, or use --no-switch")
	}
	return nil
}
