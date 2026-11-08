package pr

import (
	"context"
	"errors"
	"strings"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
)

// Status prints the pull request or merge request of the current branch, if
// there is one. An open request wins over a closed or merged one.
func Status(ctx context.Context, repo *repository.Repository, cfg domain.Config, c provider.Provider, p output.Printer) error {
	noun := c.Noun()
	hosted, err := hostedRepo(ctx, repo, cfg, c)
	if err != nil {
		return err
	}
	current, err := repo.CurrentBranch(ctx)
	if errors.Is(err, repository.ErrDetachedHead) {
		return apperr.New(apperr.CodeUnsafeRepo, "HEAD is detached; there is no branch to look a "+noun+" up for",
			"switch to a branch first: git switch <branch>")
	}
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot determine current branch", "", err)
	}

	p.Note("Querying %s for %s", providerLabel(c), hosted)
	found, _, err := c.ListPullRequests(ctx, hosted, provider.ListOptions{State: provider.StateAll, Head: current, Limit: 20})
	if err != nil {
		return readError(err, c, hosted, noun+" list")
	}
	var x *provider.PullRequest
	for i := range found {
		// GitLab matches the source branch name in any project: keep same-repository requests only
		if !strings.EqualFold(found[i].HeadRepo, hosted.String()) {
			continue
		}
		if x == nil || (x.State != provider.StateOpen && found[i].State == provider.StateOpen) {
			x = &found[i]
		}
	}
	if x == nil {
		p.Line("No %s for branch %s.", noun, clean(current))
		p.Info("open one with: gtide pr")
		return nil
	}

	p.Line("%s %s", p.Bold(ref(c, x.Number)), p.Bold(clean(x.Title)))
	p.Line("state:     %s", paintState(p, stateLabel(*x), *x))
	p.Line("branches:  %s → %s", clean(x.HeadRef), clean(x.BaseRef))
	if review, err := c.Reviews(ctx, hosted, x.Number); err != nil {
		p.Warn("cannot read reviews: %s", clean(err.Error()))
	} else {
		p.Line("review:    %s", paintReview(p, reviewLine(review, nil), review))
	}
	p.Line("requested: %s", joinOrDash(x.Reviewers))
	p.Line("url:       %s", p.Dim(clean(x.URL)))
	return nil
}
