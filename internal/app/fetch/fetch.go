// Package fetch implements `gtide fetch`: refresh the remote-tracking branches
// and change nothing else.
package fetch

import (
	"context"
	"fmt"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/domain/branch"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

// Status says where the current branch stands against the branch it follows.
type Status struct {
	Remote   string `json:"remote"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
}

// Run fetches every branch of remote (the one in .tide.yml when empty) and drops
// the remote-tracking branches whose branch is gone. The work tree, the index
// and the local branches are not touched, so it is safe with uncommitted work.
func Run(ctx context.Context, repo *repository.Repository, cfg domain.Config, remote string, st *Status, p output.Printer) error {
	if remote == "" {
		remote = cfg.Remote
	}
	remotes, err := repo.Remotes(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot list remotes", "", err)
	}
	if !branch.Contains(remotes, remote) {
		return apperr.New(apperr.CodeConfig, fmt.Sprintf("remote %q is not configured in this repository", remote),
			"add it with `gtide remote add <url>` or change `remote` in .tide.yml")
	}
	p.Step("Fetching %s", remote)
	if err := repo.FetchPrune(ctx, remote); err != nil {
		return apperr.Wrap(apperr.CodeExternal, fmt.Sprintf("cannot fetch %s", remote), "check your network and credentials", err)
	}
	st.Remote = remote
	if up := repo.Upstream(ctx); up != "" {
		if a, b, err := repo.AheadBehind(ctx, up, "HEAD"); err == nil {
			st.Upstream, st.Ahead, st.Behind = up, a, b
			p.Success("Fetched %s; the branch is %d ahead and %d behind %s", remote, a, b, up)
			if b > 0 {
				p.Note("bring it in with `gtide pull`")
			}
			return nil
		}
	}
	p.Success("Fetched %s", remote)
	return nil
}
