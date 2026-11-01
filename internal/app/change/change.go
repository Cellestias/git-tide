// Package change implements `tide change`: switch branches safely.
package change

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/domain/branch"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

const maxCandidates = 10

type Options struct {
	// Carry lets Git carry uncommitted changes to the other branch. Git still
	// refuses if they would conflict.
	Carry bool
}

// target is the branch to switch to.
type target struct {
	name       string
	remoteOnly bool
	ref        repository.BranchRef
}

// Run switches to the branch matching query. No network call is made: remote
// branches are the ones already fetched (see `tide branch --remote`).
func Run(ctx context.Context, repo *repository.Repository, cfg domain.Config, query string, opts Options, p output.Printer) error {
	if strings.HasPrefix(query, "-") && query != "-" {
		return apperr.Usage("invalid branch %q", query)
	}
	if err := ensureSafe(ctx, repo, opts.Carry); err != nil {
		return err
	}
	refs, err := repo.ListBranches(ctx, cfg.Remote, true)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot list branches", "", err)
	}

	t, err := resolve(ctx, repo, refs, query)
	if err != nil {
		return err
	}
	if err := branch.ValidName(t.name); err != nil {
		return apperr.Usage("invalid branch name: %v", err)
	}

	current, _ := repo.CurrentBranch(ctx)
	if current == t.name {
		p.Info("already on %s", clean(t.name))
		return nil
	}

	if t.remoteOnly {
		err = repo.CreateBranch(ctx, t.name, "refs/remotes/"+cfg.Remote+"/"+t.name, true, true)
	} else {
		err = repo.Checkout(ctx, t.name)
	}
	if err != nil {
		return apperr.Wrap(apperr.CodeUnsafeRepo, fmt.Sprintf("cannot switch to %s", clean(t.name)),
			"Git refused; your changes would be overwritten. Commit or stash them first.", err)
	}

	if t.remoteOnly {
		p.Success("Switched to new branch %s tracking %s/%s", clean(t.name), cfg.Remote, clean(t.name))
	} else {
		p.Success("Switched to %s", clean(t.name))
	}
	hints(ctx, repo, cfg, t, p)
	return nil
}

func resolve(ctx context.Context, repo *repository.Repository, refs []repository.BranchRef, query string) (target, error) {
	if query == "-" {
		prev := repo.PreviousBranch(ctx)
		if prev == "" {
			return target{}, apperr.New(apperr.CodeUnsafeRepo, "there is no previous branch to go back to", "name the branch instead")
		}
		query = prev
	}

	local := map[string]repository.BranchRef{}
	remoteOnly := map[string]repository.BranchRef{}
	for _, r := range refs {
		if !r.Remote {
			local[r.Name] = r
		}
	}
	for _, r := range refs {
		if r.Remote {
			if _, ok := local[r.Name]; !ok {
				remoteOnly[r.Name] = r
			}
		}
	}

	if r, ok := local[query]; ok {
		return target{name: query, ref: r}, nil
	}
	if r, ok := remoteOnly[query]; ok {
		return target{name: query, remoteOnly: true, ref: r}, nil
	}

	needle := strings.ToLower(query)
	var matches []target
	for n, r := range local {
		if strings.Contains(strings.ToLower(n), needle) {
			matches = append(matches, target{name: n, ref: r})
		}
	}
	for n, r := range remoteOnly {
		if strings.Contains(strings.ToLower(n), needle) {
			matches = append(matches, target{name: n, remoteOnly: true, ref: r})
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].name < matches[j].name })

	switch len(matches) {
	case 0:
		return target{}, apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("no branch matches %q", clean(query)),
			"list branches with `tide branch --remote`, or create one with `tide create`")
	case 1:
		return matches[0], nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%q matches %d branches:", clean(query), len(matches))
	for i, m := range matches {
		if i == maxCandidates {
			fmt.Fprintf(&b, "\n  ... and %d more", len(matches)-maxCandidates)
			break
		}
		suffix := ""
		if m.remoteOnly {
			suffix = " (remote)"
		}
		fmt.Fprintf(&b, "\n  %s%s", clean(m.name), suffix)
	}
	return target{}, apperr.New(apperr.CodeUnsafeRepo, b.String(), "be more specific, or use the full name")
}

func ensureSafe(ctx context.Context, repo *repository.Repository, carry bool) error {
	op, err := repo.InProgressOperation(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect repository state", "", err)
	}
	if op != "" {
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("a %s is in progress", op),
			fmt.Sprintf("finish or abort it first (git %s --continue / --abort)", op))
	}
	if carry {
		return nil
	}
	changes, err := repo.TrackedChanges(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read working tree status", "", err)
	}
	if len(changes) > 0 {
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("%d file(s) have uncommitted changes", len(changes)),
			"commit or stash them first, or use --carry to let Git bring them along")
	}
	return nil
}

// hints tells the user what to do next. They are advisory: failures to compute
// them are ignored.
func hints(ctx context.Context, repo *repository.Repository, cfg domain.Config, t target, p output.Printer) {
	if branch.Contains(cfg.Branches.Protected, t.name) {
		p.Info("protected branch: direct commits and pushes are blocked by the hooks")
	}
	if t.ref.UpGone {
		p.Info("upstream is gone: it was deleted on %s", cfg.Remote)
	} else if t.ref.UpBehind > 0 {
		p.Info("↓%d behind %s: run `git pull`", t.ref.UpBehind, clean(t.ref.Upstream))
	}

	base := cfg.BaseFor(t.name)
	if base == t.name {
		return
	}
	for _, baseRef := range []string{"refs/remotes/" + cfg.Remote + "/" + base, "refs/heads/" + base} {
		if ok, err := repo.CommitExists(ctx, baseRef); err != nil || !ok {
			continue
		}
		headRef := "refs/heads/" + t.name
		if _, behind, err := repo.AheadBehind(ctx, baseRef, headRef); err == nil && behind > 0 {
			p.Info("↓%d behind %s: run `tide update`", behind, clean(base))
		}
		return
	}
}

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
