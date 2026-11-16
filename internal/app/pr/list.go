package pr

import (
	"context"

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
	JSON     bool   // print the requests as a JSON array on stdout
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
	if opts.JSON {
		return writeList(p, prs)
	}
	if len(prs) == 0 {
		p.Note("No %ss match.", c.Noun())
		return nil
	}

	rows := [][]string{{"ID", "TITLE", "AUTHOR", "STATE", "SOURCE", "TARGET"}}
	for _, x := range prs {
		rows = append(rows, []string{ref(c, x.Number), ellipsize(clean(x.Title), maxListTitle),
			clean(x.Author), stateLabel(x), clean(x.HeadRef), clean(x.BaseRef)})
	}
	p.Table(rows, func(row, col int, cell string) string {
		switch col {
		case 0, 3:
			return paintState(p, cell, prs[row-1])
		}
		return cell
	})
	return nil
}
