// Package history implements `gtide commit`: a table of the commits of a
// branch, filtered by who wrote them or by the file they touched.
package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	appcommit "github.com/cellestias/git-tide/internal/app/commit"
	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/domain/branch"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

// DefaultLimit is how many commits are shown when --limit is not given.
const DefaultLimit = 30

type Options struct {
	Me     bool   // only commits authored with the configured user.email
	Member string // only commits whose author name or email contains this
	Branch string // default: the current branch
	Remote bool   // read the remote copy of the branch (fetches it first)
	File   string // only commits that touched this file; the whole history is read
	Limit  int    // 0 means every commit
	Cwd    string // relative File paths are resolved against it

	All    bool   // the whole history, not only what the branch adds to its base
	Base   string // base to subtract (default: resolved from .tide.yml)
	Merges bool   // include merge commits
	Since  string // "7d", "2w", "3m", "12h" or any date Git understands
	Until  string
	JSON   bool // print JSON on stdout instead of a table
}

func Run(ctx context.Context, repo *repository.Repository, cfg domain.Config, opts Options, p output.Printer) error {
	if opts.Me && opts.Member != "" {
		return apperr.Usage("--me and --member cannot be combined")
	}
	if opts.Limit < 0 {
		return apperr.Usage("--limit must not be negative")
	}

	q := repository.LogQuery{Limit: opts.Limit}
	switch {
	case opts.Me:
		email := repo.UserEmail(ctx)
		if email == "" {
			return apperr.New(apperr.CodeConfig, "user.email is not set in Git", "set it with `git config user.email you@example.com`")
		}
		q.Author = "<" + email + ">"
	case strings.TrimSpace(opts.Member) != "":
		q.Author = strings.TrimSpace(opts.Member)
	case opts.Member != "":
		return apperr.Usage("--member needs a name or an email")
	}

	if opts.File != "" {
		paths, err := appcommit.ResolvePaths(repo.Root, opts.Cwd, []string{opts.File})
		if err != nil {
			return err
		}
		q.Path = paths[0]
		// Git follows renames of a single file only; a directory is listed as is.
		info, err := os.Stat(filepath.Join(repo.Root, filepath.FromSlash(q.Path)))
		q.Follow = err != nil || !info.IsDir() // a deleted file is still a file
	}
	q.Merges = opts.Merges
	var err error
	if q.Since, err = gitDate("--since", opts.Since); err != nil {
		return err
	}
	if q.Until, err = gitDate("--until", opts.Until); err != nil {
		return err
	}

	name, err := branchName(ctx, repo, opts.Branch)
	if err != nil {
		return err
	}
	rev, label, err := resolveRev(ctx, repo, cfg.Remote, name, opts.Remote, p)
	if err != nil {
		return err
	}
	q.Rev = rev

	// A file's history and --all read everything; otherwise only what the
	// branch adds to its base, so inherited history does not drown the list.
	scope := ""
	if !opts.All && q.Path == "" {
		base, ref, err := baseRef(ctx, repo, cfg, name, opts.Base)
		if err != nil {
			return err
		}
		switch {
		case ref == "":
			p.Note("base branch %q not found; showing the whole history", base)
		case base != name:
			q.Exclude, scope = ref, base
		}
	}

	q.Files = opts.JSON
	entries, err := repo.Log(ctx, q)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read the commit history", "", err)
	}
	if opts.JSON {
		return writeJSON(p, entries)
	}
	if len(entries) == 0 {
		p.Info("No commits match on %s%s", label, describe(q, opts, scope))
		return nil
	}

	rows := [][]string{{"COMMIT", "DATE", "AUTHOR", "MESSAGE"}}
	for _, e := range entries {
		rows = append(rows, []string{e.Hash, e.Date.Format("2006-01-02"), e.AuthorName, e.Subject})
	}
	p.Note("%d commit%s on %s%s", len(entries), plural(len(entries)), label, describe(q, opts, scope))
	p.Table(rows, func(_, col int, cell string) string {
		switch col {
		case 0:
			return p.Yellow(cell)
		case 1:
			return p.Dim(cell)
		}
		return cell
	})
	if q.Limit > 0 && len(entries) == q.Limit {
		p.Note("showing the latest %d; raise --limit to see more", q.Limit)
	}
	if scope != "" {
		p.Note("commits already in %s are left out; use --all for the whole history", scope)
	}
	return nil
}

func branchName(ctx context.Context, repo *repository.Repository, given string) (string, error) {
	if given != "" {
		if err := branch.ValidName(given); err != nil {
			return "", apperr.Usage("%v", err)
		}
		return given, nil
	}
	current, err := repo.CurrentBranch(ctx)
	switch {
	case errors.Is(err, repository.ErrDetachedHead):
		return "", apperr.New(apperr.CodeUsage, "HEAD is detached", "name a branch with --branch")
	case err != nil:
		return "", apperr.Wrap(apperr.CodeExternal, "cannot determine current branch", "", err)
	}
	return current, nil
}

// resolveRev returns the fully qualified ref to read and a name to show.
func resolveRev(ctx context.Context, repo *repository.Repository, remote, name string, onRemote bool, p output.Printer) (rev, label string, err error) {
	remoteRef := "refs/remotes/" + remote + "/" + name
	remoteLabel := remote + "/" + name

	if onRemote {
		remotes, err := repo.Remotes(ctx)
		if err != nil {
			return "", "", apperr.Wrap(apperr.CodeExternal, "cannot list remotes", "", err)
		}
		if !branch.Contains(remotes, remote) {
			return "", "", apperr.New(apperr.CodeConfig, fmt.Sprintf("remote %q is not configured in this repository", remote),
				"add it with `git remote add` or change `remote` in .tide.yml")
		}
		p.Note("Fetching %s", remoteLabel)
		if err := repo.Fetch(ctx, remote, "+refs/heads/"+name+":"+remoteRef); err != nil {
			return "", "", apperr.Wrap(apperr.CodeExternal, fmt.Sprintf("cannot fetch %s", remoteLabel),
				"check that the branch exists on the remote, your network and your credentials", err)
		}
		return remoteRef, remoteLabel, nil
	}

	if ok, err := repo.LocalBranchExists(ctx, name); err != nil {
		return "", "", apperr.Wrap(apperr.CodeExternal, "cannot look up the branch", "", err)
	} else if ok {
		return "refs/heads/" + name, name, nil
	}
	if ok, err := repo.RemoteBranchExists(ctx, remote, name); err != nil {
		return "", "", apperr.Wrap(apperr.CodeExternal, "cannot look up the branch", "", err)
	} else if ok {
		return remoteRef, remoteLabel, nil
	}
	return "", "", apperr.New(apperr.CodeUsage, fmt.Sprintf("branch %q not found", name),
		"see `gtide branch --remote` for the branches that exist")
}

// describe names the active filters, so the header says what was asked.
func describe(q repository.LogQuery, opts Options, scope string) string {
	var parts []string
	switch {
	case opts.Me:
		parts = append(parts, "by you")
	case q.Author != "":
		parts = append(parts, "by "+q.Author)
	}
	if q.Path != "" {
		parts = append(parts, "touching "+q.Path)
	}
	if scope != "" {
		parts = append(parts, "not in "+scope)
	}
	if opts.Since != "" {
		parts = append(parts, "since "+opts.Since)
	}
	if opts.Until != "" {
		parts = append(parts, "until "+opts.Until)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// baseRef resolves the base to subtract: the --base flag, else the one from
// .tide.yml. The remote copy wins, as in `gtide branch`. ref is "" when the
// configured base does not exist; an explicit --base that does not exist fails.
func baseRef(ctx context.Context, repo *repository.Repository, cfg domain.Config, name, given string) (base, ref string, err error) {
	base = given
	if base == "" {
		base = cfg.BaseFor(name)
	} else if err := branch.ValidName(base); err != nil {
		return "", "", apperr.Usage("%v", err)
	}
	for _, c := range []string{"refs/remotes/" + cfg.Remote + "/" + base, "refs/heads/" + base} {
		if ok, err := repo.CommitExists(ctx, c); err == nil && ok {
			return base, c, nil
		}
	}
	if given != "" {
		return "", "", apperr.New(apperr.CodeUsage, fmt.Sprintf("base branch %q not found", given), "")
	}
	return base, "", nil
}

var shortDate = regexp.MustCompile(`^(\d+)([hdwm])$`)

// gitDate turns "7d" into "7 days ago" and leaves anything else to Git, which
// reads dates like "2026-01-31" and "last friday".
func gitDate(flag, v string) (string, error) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "-") {
		return "", apperr.Usage("invalid %s %q", flag, v)
	}
	m := shortDate.FindStringSubmatch(v)
	if m == nil {
		return v, nil
	}
	n, _ := strconv.Atoi(m[1])
	unit := map[string]string{"h": "hour", "d": "day", "w": "week", "m": "month"}[m[2]]
	if n != 1 {
		unit += "s"
	}
	return fmt.Sprintf("%d %s ago", n, unit), nil
}

type jsonCommit struct {
	Hash    string     `json:"hash"`
	Author  string     `json:"author"`
	Email   string     `json:"email"`
	Date    time.Time  `json:"date"`
	Subject string     `json:"subject"`
	Files   []jsonFile `json:"files"`
}

type jsonFile struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Binary  bool   `json:"binary"`
}

func toJSONFiles(files []repository.FileChange) []jsonFile {
	out := make([]jsonFile, 0, len(files))
	for _, f := range files {
		out = append(out, jsonFile{f.Path, f.Added, f.Deleted, f.Binary})
	}
	return out
}

func writeJSON(p output.Printer, entries []repository.LogEntry) error {
	out := make([]jsonCommit, 0, len(entries))
	for _, e := range entries {
		out = append(out, jsonCommit{e.Hash, e.AuthorName, e.AuthorEmail, e.Date, e.Subject, toJSONFiles(e.Files)})
	}
	return encode(p, out)
}

func encode(p output.Printer, v any) error {
	enc := json.NewEncoder(p.Out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return apperr.Wrap(apperr.CodeGeneral, "cannot write JSON", "", err)
	}
	return nil
}
