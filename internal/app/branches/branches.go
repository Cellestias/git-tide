// Package branches implements `tide branch`: a table of branches enriched with
// Tide's view of them (base branch, protection, staleness).
package branches

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/domain/branch"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/github"
	"github.com/cellestias/git-tide/internal/output"
)

// Sort orders.
const (
	SortDate  = "date"
	SortName  = "name"
	SortAhead = "ahead"
)

// GitHub is the slice of the GitHub client this use case needs.
type GitHub interface {
	ListProtectedBranches(ctx context.Context, repo github.Repo) (map[string]bool, bool, error)
	ListOpenPullRequests(ctx context.Context, repo github.Repo) ([]github.PullRequest, bool, error)
}

type Options struct {
	Remote    bool // include remote-only branches (fetches first)
	GitHub    bool // add the GitHub column (needs the API)
	Mine      bool // only branches whose last author is the configured user.email
	StaleDays int  // only branches without commits for more than N days (0 = off)
	Merged    bool // only branches with nothing the base lacks
	Sort      string
	Now       time.Time // zero means time.Now()
}

type row struct {
	ref        repository.BranchRef
	base       string
	hasBase    bool
	ahead      int
	behind     int
	protected  bool
	merged     bool
	remoteOnly bool
}

func Run(ctx context.Context, repo *repository.Repository, cfg domain.Config, gh GitHub, opts Options, p output.Printer) error {
	sortBy := opts.Sort
	if sortBy == "" {
		sortBy = SortDate
	}
	if sortBy != SortDate && sortBy != SortName && sortBy != SortAhead {
		return apperr.Usage("invalid --sort %q: use %s, %s or %s", opts.Sort, SortDate, SortName, SortAhead)
	}
	if opts.StaleDays < 0 {
		return apperr.Usage("--stale must be a positive number of days")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	var email string
	if opts.Mine {
		if email = repo.UserEmail(ctx); email == "" {
			return apperr.New(apperr.CodeConfig, "user.email is not set in Git", "set it with `git config user.email you@example.com`")
		}
	}

	remote := cfg.Remote
	if opts.Remote {
		remotes, err := repo.Remotes(ctx)
		if err != nil {
			return apperr.Wrap(apperr.CodeExternal, "cannot list remotes", "", err)
		}
		if !branch.Contains(remotes, remote) {
			return apperr.New(apperr.CodeConfig, fmt.Sprintf("remote %q is not configured in this repository", remote),
				"add it with `git remote add` or change `remote` in .tide.yml")
		}
		p.Note("Fetching %s", remote)
		if err := repo.FetchPrune(ctx, remote); err != nil {
			return apperr.Wrap(apperr.CodeExternal, fmt.Sprintf("cannot fetch %s", remote),
				"check your network and credentials, or run without --remote", err)
		}
	}

	refs, err := repo.ListBranches(ctx, remote, opts.Remote)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot list branches", "", err)
	}
	local := map[string]bool{}
	for _, r := range refs {
		if !r.Remote {
			local[r.Name] = true
		}
	}

	baseRefs := map[string]string{} // base name -> resolved ref ("" when none)
	resolveBase := func(base string) string {
		if ref, ok := baseRefs[base]; ok {
			return ref
		}
		ref := ""
		for _, c := range []string{"refs/remotes/" + remote + "/" + base, "refs/heads/" + base} {
			if ok, err := repo.CommitExists(ctx, c); err == nil && ok {
				ref = c
				break
			}
		}
		baseRefs[base] = ref
		return ref
	}

	var rows []row
	for _, r := range refs {
		if r.Remote && local[r.Name] {
			continue // the local branch represents it
		}
		x := row{ref: r, remoteOnly: r.Remote, base: cfg.BaseFor(r.Name)}
		x.protected = branch.Contains(cfg.Branches.Protected, r.Name)
		if baseRef := resolveBase(x.base); baseRef != "" && r.Name != x.base {
			if a, b, err := repo.AheadBehind(ctx, baseRef, r.Ref(remote)); err == nil {
				x.hasBase, x.ahead, x.behind = true, a, b
				x.merged = a == 0 && b > 0
			}
		}
		rows = append(rows, x)
	}

	rows = filter(rows, opts, email, now)
	sortRows(rows, sortBy)

	var ghCol *githubInfo
	if opts.GitHub && gh != nil {
		ghCol = loadGitHub(ctx, repo, cfg, gh, p)
	}
	render(p, rows, ghCol, now)
	if len(rows) == 0 {
		p.Note("No branches match.")
	}
	return nil
}

func filter(rows []row, opts Options, email string, now time.Time) []row {
	var out []row
	for _, r := range rows {
		if opts.Mine && !strings.EqualFold(r.ref.AuthorEmail, email) {
			continue
		}
		if opts.StaleDays > 0 && now.Sub(r.ref.Updated) <= time.Duration(opts.StaleDays)*24*time.Hour {
			continue
		}
		if opts.Merged && !r.merged {
			continue
		}
		out = append(out, r)
	}
	return out
}

func sortRows(rows []row, by string) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch by {
		case SortName:
			return a.ref.Name < b.ref.Name
		case SortAhead:
			if a.ahead != b.ahead {
				return a.ahead > b.ahead
			}
		}
		if !a.ref.Updated.Equal(b.ref.Updated) {
			return a.ref.Updated.After(b.ref.Updated)
		}
		return a.ref.Name < b.ref.Name
	})
}

type githubInfo struct {
	protected    map[string]bool
	protectedErr bool
	prs          map[string]int // head branch -> PR number
	prsErr       bool
}

// loadGitHub degrades gracefully: any failure becomes a warning and a "?" in
// the column, never a failed command.
func loadGitHub(ctx context.Context, repo *repository.Repository, cfg domain.Config, gh GitHub, p output.Printer) *githubInfo {
	info := &githubInfo{}
	ghRepo, err := github.RepoFromRemote(ctx, repo, cfg.Remote)
	if err != nil {
		p.Warn("GitHub column unavailable: %v", err)
		info.protectedErr, info.prsErr = true, true
		return info
	}
	p.Note("Querying GitHub for %s", ghRepo)
	prot, truncated, err := gh.ListProtectedBranches(ctx, ghRepo)
	if err != nil {
		p.Warn("cannot read branch protection: %s (set GITHUB_TOKEN for private repositories)", clean(err.Error()))
		info.protectedErr = true
	}
	if truncated {
		p.Warn("GitHub branch list truncated; protection shown for the first branches only")
	}
	info.protected = prot

	prs, truncated, err := gh.ListOpenPullRequests(ctx, ghRepo)
	if err != nil {
		p.Warn("cannot read pull requests: %s", clean(err.Error()))
		info.prsErr = true
	}
	if truncated {
		p.Warn("GitHub pull request list truncated")
	}
	info.prs = map[string]int{}
	for _, pr := range prs {
		if strings.EqualFold(pr.HeadRepo, ghRepo.String()) { // same-repo PRs only
			info.prs[pr.HeadRef] = pr.Number
		}
	}
	return info
}

func (g *githubInfo) cell(r row) string {
	if g.protectedErr && g.prsErr {
		return "?"
	}
	var parts []string
	if g.protectedErr {
		parts = append(parts, "?")
	} else if g.protected[r.ref.Name] {
		parts = append(parts, "protected")
	}
	if g.prsErr {
		parts = append(parts, "PR ?")
	} else if n, ok := g.prs[r.ref.Name]; ok {
		parts = append(parts, fmt.Sprintf("PR #%d", n))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func render(p output.Printer, rows []row, gh *githubInfo, now time.Time) {
	tw := tabwriter.NewWriter(p.Out, 0, 4, 2, ' ', 0)
	header := "BRANCH\tUPDATED\tLAST AUTHOR\tUPSTREAM\tBASE\tSTATUS"
	if gh != nil {
		header += "\tGITHUB"
	}
	fmt.Fprintln(tw, header)
	for _, r := range rows {
		marker := "  "
		if r.ref.Current {
			marker = "* "
		}
		line := fmt.Sprintf("%s%s\t%s\t%s\t%s\t%s\t%s",
			marker, clean(r.ref.Name), ago(now, r.ref.Updated), clean(r.ref.AuthorName),
			upstreamCell(r), baseCell(r), statusCell(r))
		if gh != nil {
			line += "\t" + gh.cell(r)
		}
		fmt.Fprintln(tw, line)
	}
	tw.Flush()
}

func upstreamCell(r row) string {
	switch {
	case r.remoteOnly:
		return "-"
	case r.ref.UpGone:
		return "gone"
	case r.ref.Upstream == "":
		return "none"
	}
	return aheadBehind(r.ref.UpAhead, r.ref.UpBehind, "in sync")
}

func baseCell(r row) string {
	if !r.hasBase {
		return "-"
	}
	return r.base + " " + aheadBehind(r.ahead, r.behind, "=")
}

func statusCell(r row) string {
	var flags []string
	if r.protected {
		flags = append(flags, "protected")
	}
	if r.merged {
		flags = append(flags, "merged")
	}
	if r.remoteOnly {
		flags = append(flags, "remote-only")
	}
	if len(flags) == 0 {
		return "-"
	}
	return strings.Join(flags, ", ")
}

func aheadBehind(ahead, behind int, same string) string {
	switch {
	case ahead == 0 && behind == 0:
		return same
	case behind == 0:
		return fmt.Sprintf("↑%d", ahead)
	case ahead == 0:
		return fmt.Sprintf("↓%d", behind)
	}
	return fmt.Sprintf("↑%d ↓%d", ahead, behind)
}

// ago renders a coarse relative time.
func ago(now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	unit := func(n int, name string) string {
		if n == 1 {
			return fmt.Sprintf("1 %s ago", name)
		}
		return fmt.Sprintf("%d %ss ago", n, name)
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return unit(int(d/time.Minute), "minute")
	case d < 24*time.Hour:
		return unit(int(d/time.Hour), "hour")
	case d < 30*24*time.Hour:
		return unit(int(d/(24*time.Hour)), "day")
	case d < 365*24*time.Hour:
		return unit(int(d/(30*24*time.Hour)), "month")
	}
	return unit(int(d/(365*24*time.Hour)), "year")
}

// clean strips control characters: branch names and author names come from
// repository content and must not inject terminal escape sequences.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
