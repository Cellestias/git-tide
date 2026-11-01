// Package review implements `tide review`.
package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/github"
	"github.com/cellestias/git-tide/internal/output"
)

// PullRequests is the slice of the GitHub client this use case needs.
type PullRequests interface {
	GetPullRequest(ctx context.Context, repo github.Repo, number int) (github.PullRequest, error)
}

type Options struct {
	// Ref is a PR number, "#number", or URL.
	Ref string
	// Reset discards local commits on an existing review branch.
	Reset bool
}

// BranchName is the predictable local branch used to review a PR.
func BranchName(number int) string { return fmt.Sprintf("tide/review/pr-%d", number) }

func Run(ctx context.Context, repo *repository.Repository, cfg domain.Config, prs PullRequests, opts Options, p output.Printer) error {
	ref, err := github.ParsePRRef(opts.Ref)
	if err != nil {
		return apperr.Usage("%v", err)
	}

	remoteURL, err := repo.RemoteURL(ctx, cfg.Remote)
	if err != nil {
		return apperr.Wrap(apperr.CodeConfig, fmt.Sprintf("remote %q is not configured", cfg.Remote),
			"add it with `git remote add` or change `remote` in .tide.yml", err)
	}
	ghRepo, err := github.ParseRemoteURL(remoteURL)
	if err != nil {
		// url.<base>.insteadOf may have rewritten the URL into something that is
		// not GitHub-shaped (a mirror, a local path); retry with the URL as written.
		if raw, rerr := repo.RemoteConfiguredURL(ctx, cfg.Remote); rerr == nil {
			var perr error
			if ghRepo, perr = github.ParseRemoteURL(raw); perr == nil {
				err = nil
			}
		}
	}
	if err != nil {
		return apperr.Wrap(apperr.CodeConfig, fmt.Sprintf("remote %q is not a supported GitHub remote", cfg.Remote),
			"only github.com remotes are supported", err)
	}
	if ref.Repo != nil && !strings.EqualFold(ref.Repo.String(), ghRepo.String()) {
		return apperr.Usage("pull request belongs to %s but remote %q is %s", ref.Repo, cfg.Remote, ghRepo)
	}

	current, err := repo.CurrentBranch(ctx)
	if err != nil && !errors.Is(err, repository.ErrDetachedHead) {
		return apperr.Wrap(apperr.CodeExternal, "cannot determine current branch", "", err)
	}
	if err := ensureSafe(ctx, repo); err != nil {
		return err
	}

	p.Step("Reading %s#%d", ghRepo, ref.Number)
	pr, err := prs.GetPullRequest(ctx, ghRepo, ref.Number)
	if errors.Is(err, github.ErrNotFound) {
		return apperr.New(apperr.CodeExternal, fmt.Sprintf("%s#%d not found or not accessible", ghRepo, ref.Number),
			"for private repositories set GITHUB_TOKEN")
	}
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read pull request", "", err)
	}

	hidden := fmt.Sprintf("refs/tide/pr/%d", ref.Number)
	p.Step("Fetching refs/pull/%d/head", ref.Number)
	if err := repo.Fetch(ctx, cfg.Remote, fmt.Sprintf("+refs/pull/%d/head:%s", ref.Number, hidden)); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot fetch the pull request head", "check your network and credentials", err)
	}

	name := BranchName(ref.Number)
	if err := checkoutReview(ctx, repo, name, hidden, current, opts.Reset); err != nil {
		return err
	}

	p.Success("On review branch %s", name)
	p.Info("title:  %s", clean(pr.Title))
	p.Info("author: %s", clean(pr.Author))
	p.Info("state:  %s", clean(pr.State))
	p.Info("source: %s", clean(pr.HeadRef))
	p.Info("target: %s", clean(pr.BaseRef))
	p.Info("next:   git diff %s/%s...HEAD    (return with: git switch %s)", cfg.Remote, clean(pr.BaseRef), orDash(current))
	return nil
}

func checkoutReview(ctx context.Context, repo *repository.Repository, name, ref, current string, reset bool) error {
	exists, err := repo.LocalBranchExists(ctx, name)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect local branches", "", err)
	}
	wrap := func(err error) error {
		return apperr.Wrap(apperr.CodeExternal, "cannot check out the review branch", "", err)
	}
	switch {
	case !exists:
		if err := repo.CheckoutBranchAt(ctx, name, ref, false); err != nil {
			return wrap(err)
		}
	case reset:
		if err := repo.CheckoutBranchAt(ctx, name, ref, true); err != nil {
			return wrap(err)
		}
	default:
		ok, err := repo.IsAncestor(ctx, "refs/heads/"+name, ref)
		if err != nil {
			return wrap(err)
		}
		if !ok {
			return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("local %s has commits that are not in the pull request", name),
				"re-run with --reset to discard them, or rename the branch to keep them")
		}
		if current != name {
			if err := repo.Checkout(ctx, name); err != nil {
				return wrap(err)
			}
		}
		if err := repo.MergeFastForward(ctx, ref); err != nil {
			return wrap(err)
		}
	}
	return nil
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
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("%d file(s) have uncommitted changes", len(changes)),
			"commit or stash them before switching branches")
	}
	return nil
}

// clean strips control characters so PR-controlled text cannot inject
// terminal escape sequences.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

func orDash(s string) string {
	if s == "" {
		return "<previous branch>"
	}
	return s
}
