package pr

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
)

// Limits of `gtide pr list`.
const (
	DefaultListLimit = 20
	MaxListLimit     = 100
	maxListTitle     = 60
)

// ListOptions are the filters of `gtide pr list`.
type ListOptions struct {
	State    string // open (default), closed, merged or all
	Author   string // login, or "me"
	Assignee string // login, or "me"
	Limit    int    // 0 means DefaultListLimit
}

// List prints the repository's pull requests or merge requests as a table.
func List(ctx context.Context, repo *repository.Repository, cfg domain.Config, c provider.Provider, opts ListOptions, p output.Printer) error {
	state := opts.State
	if state == "" {
		state = provider.StateOpen
	}
	if !provider.ValidState(state) {
		return apperr.Usage("invalid --state %q: use open, closed, merged or all", opts.State)
	}
	limit := opts.Limit
	if limit == 0 {
		limit = DefaultListLimit
	}
	if limit < 0 || limit > MaxListLimit {
		return apperr.Usage("--limit must be between 1 and %d", MaxListLimit)
	}
	hosted, err := hostedRepo(ctx, repo, cfg, c)
	if err != nil {
		return err
	}
	author, err := resolveUser(ctx, c, "--author", opts.Author)
	if err != nil {
		return err
	}
	assignee, err := resolveUser(ctx, c, "--assignee", opts.Assignee)
	if err != nil {
		return err
	}

	p.Note("Querying %s for %s", providerLabel(c), hosted)
	prs, truncated, err := c.ListPullRequests(ctx, hosted, provider.ListOptions{State: state, Author: author, Assignee: assignee, Limit: limit})
	if err != nil {
		return readError(err, c, hosted, c.Noun()+" list")
	}
	if truncated {
		p.Warn("%s list truncated; narrow it with --state, --author or --assignee", c.Noun())
	}
	if len(prs) == 0 {
		p.Note("No %ss match.", c.Noun())
		return nil
	}

	tw := tabwriter.NewWriter(p.Out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tTITLE\tAUTHOR\tSTATE\tSOURCE\tTARGET")
	for _, x := range prs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", ref(c, x.Number), ellipsize(clean(x.Title), maxListTitle),
			clean(x.Author), stateLabel(x), clean(x.HeadRef), clean(x.BaseRef))
	}
	_ = tw.Flush()
	return nil
}
