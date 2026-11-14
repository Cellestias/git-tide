// Package status implements `gtide status` and `gtide diff`: what changed, and
// where the branch stands against its upstream and its base.
package status

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	appcommit "github.com/cellestias/git-tide/internal/app/commit"
	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

// maxShown caps each list; the counts always stay exact.
const maxShown = 15

// Run prints the branch, how far it is from its upstream and its base, any
// operation in progress, and the changed files in three groups. It reads local
// data only: no fetch, so the remote numbers are as of the last fetch.
func Run(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
	current, err := repo.CurrentBranch(ctx)
	detached := errors.Is(err, repository.ErrDetachedHead)
	if err != nil && !detached {
		return apperr.Wrap(apperr.CodeExternal, "cannot determine current branch", "", err)
	}
	entries, err := repo.Status(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read the working tree status", "", err)
	}

	if detached {
		head, _ := repo.ShortHead(ctx)
		p.Line("%s HEAD detached at %s", p.Bold("On"), head)
	} else {
		p.Line("%s %s", p.Bold("On"), p.Bold(current))
		reportDistance(ctx, repo, cfg, current, p)
	}
	if op, _ := repo.InProgressOperation(ctx); op != "" {
		p.Warn("a %s is in progress: finish it with `gtide continue` or give up with `gtide abort`", op)
	}

	var conflicts, staged, unstaged, untracked []string
	for _, e := range entries {
		switch {
		case e.Conflict():
			conflicts = append(conflicts, label(e, e.X, e.Y))
		case e.Untracked():
			untracked = append(untracked, "?  "+e.Path)
		default:
			if e.Staged() {
				staged = append(staged, label(e, e.X, ' '))
			}
			if e.Unstaged() {
				unstaged = append(unstaged, label(e, ' ', e.Y))
			}
		}
	}
	group(p, "Conflicts", conflicts, p.Red)
	group(p, "Staged", staged, p.Green)
	group(p, "Not staged", unstaged, p.Yellow)
	group(p, "Untracked", untracked, p.Dim)
	if len(entries) == 0 {
		p.Success("working tree clean")
		return nil
	}
	switch {
	case len(conflicts) > 0:
		p.Note("resolve the conflicts, then `gtide continue`")
	case len(staged) > 0 || len(unstaged) > 0 || len(untracked) > 0:
		p.Note("commit with `gtide save -m \"type: message\"` (or `-f <file>` for some files); see changes with `gtide diff`")
	}
	return nil
}

func label(e repository.StatusEntry, x, y byte) string {
	code := strings.TrimSpace(string([]byte{x, y}))
	if code == "" {
		code = "?"
	}
	path := e.Path
	if e.From != "" {
		path = e.From + " -> " + e.Path
	}
	return fmt.Sprintf("%-2s %s", code, path)
}

func group(p output.Printer, title string, lines []string, paint func(string) string) {
	if len(lines) == 0 {
		return
	}
	p.Line("%s (%d)", p.Bold(title), len(lines))
	for i, l := range lines {
		if i == maxShown {
			p.Line("   %s", p.Dim(fmt.Sprintf("... and %d more", len(lines)-maxShown)))
			break
		}
		p.Line("   %s", paint(l))
	}
}

// reportDistance prints the upstream and base lines when they can be computed.
func reportDistance(ctx context.Context, repo *repository.Repository, cfg domain.Config, current string, p output.Printer) {
	if up := repo.Upstream(ctx); up != "" {
		if a, b, err := repo.AheadBehind(ctx, up, "refs/heads/"+current); err == nil {
			p.Line("   %s %s: %s", p.Dim("upstream"), up, distance(a, b))
		}
	} else {
		p.Line("   %s %s", p.Dim("upstream"), "none yet (publish with `gtide push`)")
	}
	base := cfg.BaseFor(current)
	if base == current {
		return
	}
	for _, ref := range []string{"refs/remotes/" + cfg.Remote + "/" + base, "refs/heads/" + base} {
		if ok, err := repo.CommitExists(ctx, ref); err == nil && ok {
			if a, b, err := repo.AheadBehind(ctx, ref, "refs/heads/"+current); err == nil {
				p.Line("   %s %s: %s", p.Dim("base    "), base, distance(a, b))
			}
			return
		}
	}
}

func distance(ahead, behind int) string {
	switch {
	case ahead == 0 && behind == 0:
		return "up to date"
	case behind == 0:
		return fmt.Sprintf("%d ahead", ahead)
	case ahead == 0:
		return fmt.Sprintf("%d behind", behind)
	}
	return fmt.Sprintf("%d ahead, %d behind", ahead, behind)
}

// DiffOptions picks what `gtide diff` shows.
type DiffOptions struct {
	Staged bool   // the index against HEAD instead of the work tree against the index
	File   string // limit to this path, relative to Cwd
	Patch  bool   // the full diff instead of the per-file summary
	Cwd    string
}

// Diff prints the changed files with their added and deleted lines, or the full patch.
func Diff(ctx context.Context, repo *repository.Repository, opts DiffOptions, p output.Printer) error {
	path := ""
	if opts.File != "" {
		paths, err := appcommit.ResolvePaths(repo.Root, opts.Cwd, []string{opts.File})
		if err != nil {
			return err
		}
		path = paths[0]
		if _, err := os.Lstat(filepath.Join(repo.Root, filepath.FromSlash(path))); err != nil && !opts.Staged {
			p.Note("%s is not in the working tree; showing what Git knows about it", path)
		}
	}
	where := "working tree against the index"
	if opts.Staged {
		where = "staged changes against the last commit"
	}

	if opts.Patch {
		out, err := repo.ChangePatch(ctx, opts.Staged, path)
		if err != nil {
			return apperr.Wrap(apperr.CodeExternal, "cannot read the diff", "", err)
		}
		if strings.TrimSpace(out) == "" {
			p.Info("No changes (%s)", where)
			return nil
		}
		_, _ = fmt.Fprint(p.Out, out)
		return nil
	}

	files, err := repo.ChangeStat(ctx, opts.Staged, path)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read the diff", "", err)
	}
	if len(files) == 0 {
		p.Info("No changes (%s)", where)
		if !opts.Staged {
			p.Note("untracked files are not part of a diff; they show in `gtide status`")
		}
		return nil
	}
	rows := [][]string{{"FILE", "CHANGES"}}
	added, deleted := 0, 0
	for _, f := range files {
		change := fmt.Sprintf("+%d -%d", f.Added, f.Deleted)
		if f.Binary {
			change = "binary"
		}
		rows = append(rows, []string{f.Path, change})
		added, deleted = added+f.Added, deleted+f.Deleted
	}
	p.Note("%s", where)
	p.Table(rows, nil)
	p.Line("%s", p.Dim(fmt.Sprintf("%d file%s, +%d -%d  (--patch for the full diff)", len(files), plural(len(files)), added, deleted)))
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
