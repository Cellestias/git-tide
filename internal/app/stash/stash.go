// Package stash implements `gtide stash`: put local changes aside and bring
// them back.
package stash

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

var refRe = regexp.MustCompile(`^stash@\{\d{1,4}\}$`)

// ParseRef accepts "", a number ("2") or "stash@{2}" and returns the stash ref.
func ParseRef(arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	switch {
	case arg == "":
		return "stash@{0}", nil
	case refRe.MatchString(arg):
		return arg, nil
	case regexp.MustCompile(`^\d{1,4}$`).MatchString(arg):
		return "stash@{" + arg + "}", nil
	}
	return "", apperr.Usage("invalid stash %q; use a number from `gtide stash list`, such as 0 or stash@{0}", arg)
}

func ensureIdle(ctx context.Context, repo *repository.Repository) error {
	op, err := repo.InProgressOperation(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect repository state", "", err)
	}
	if op != "" {
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("a %s is in progress", op), "finish it with `gtide continue` or give it up with `gtide abort` first")
	}
	return nil
}

// Push saves the tracked changes (and the untracked files when asked) and
// leaves the work tree clean. Ignored files, such as secret.tide.yml, are never touched.
func Push(ctx context.Context, repo *repository.Repository, message string, untracked bool, p output.Printer) error {
	if err := ensureIdle(ctx, repo); err != nil {
		return err
	}
	entries, err := repo.Status(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read the working tree status", "", err)
	}
	n, skipped := 0, 0
	for _, e := range entries {
		switch {
		case e.Conflict():
			return apperr.New(apperr.CodeUnsafeRepo, "there are unresolved conflicts", "resolve them first, then `gtide continue`")
		case e.Untracked():
			if untracked {
				n++
			} else {
				skipped++
			}
		default:
			n++
		}
	}
	if n == 0 {
		if skipped > 0 {
			p.Info("Nothing tracked to stash; %d untracked file%s left in place (add --untracked to include them)", skipped, plural(skipped))
			return nil
		}
		p.Info("No local changes to stash")
		return nil
	}
	if err := repo.StashPush(ctx, strings.TrimSpace(message), untracked); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot stash the changes", "", err)
	}
	p.Success("Stashed %d file%s; the working tree is clean", n, plural(n))
	if skipped > 0 {
		p.Note("%d untracked file%s stayed in place (use --untracked to stash them too)", skipped, plural(skipped))
	}
	p.Note("bring them back with `gtide stash pop`")
	return nil
}

// List prints the stashes, newest first.
func List(ctx context.Context, repo *repository.Repository, asJSON bool, p output.Printer) error {
	list, err := repo.StashList(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot list the stashes", "", err)
	}
	if asJSON {
		return writeJSON(p, list)
	}
	if len(list) == 0 {
		p.Info("No stashes")
		return nil
	}
	rows := [][]string{{"STASH", "WHEN", "MESSAGE"}}
	for _, s := range list {
		rows = append(rows, []string{s.Ref, s.When, s.Message})
	}
	p.Table(rows, func(_, col int, cell string) string {
		switch col {
		case 0:
			return p.Yellow(cell)
		case 1:
			return p.Dim(cell)
		}
		return cell
	})
	return nil
}

func exists(ctx context.Context, repo *repository.Repository, ref string) error {
	list, err := repo.StashList(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot list the stashes", "", err)
	}
	for _, s := range list {
		if s.Ref == ref {
			return nil
		}
	}
	return apperr.New(apperr.CodeUsage, fmt.Sprintf("%s does not exist", ref), "see `gtide stash list`")
}

// Pop applies a stash and drops it. When the changes conflict with the work
// tree, Git keeps the stash and Tide says so.
func Pop(ctx context.Context, repo *repository.Repository, ref string, p output.Printer) error {
	if err := ensureIdle(ctx, repo); err != nil {
		return err
	}
	if err := exists(ctx, repo, ref); err != nil {
		return err
	}
	if err := repo.StashPop(ctx, ref); err != nil {
		return apperr.Wrap(apperr.CodeGeneral, "the stash did not apply cleanly",
			fmt.Sprintf("it is still saved as %s; resolve the conflicts (see `gtide status`), then `git stash drop %s`", ref, ref), err)
	}
	p.Success("Applied %s and removed it from the list", ref)
	return nil
}

// Drop deletes a stash for good, after confirm says yes. A nil confirm proceeds.
func Drop(ctx context.Context, repo *repository.Repository, ref string, confirm func(string) (bool, error), p output.Printer) error {
	if err := exists(ctx, repo, ref); err != nil {
		return err
	}
	if confirm != nil {
		ok, err := confirm(fmt.Sprintf("Delete %s for good? [y/N] ", ref))
		if err != nil {
			return apperr.Wrap(apperr.CodeGeneral, "cannot read confirmation", "use --yes to skip the prompt", err)
		}
		if !ok {
			return apperr.New(apperr.CodeGeneral, "aborted; nothing was deleted", "")
		}
	}
	if err := repo.StashDrop(ctx, ref); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot drop the stash", "", err)
	}
	p.Success("Deleted %s", ref)
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

type jsonStash struct {
	Ref     string    `json:"ref"` // stash@{0}
	Date    time.Time `json:"date"`
	Message string    `json:"message"`
}

func writeJSON(p output.Printer, list []repository.StashEntry) error {
	out := make([]jsonStash, 0, len(list))
	for _, s := range list {
		out = append(out, jsonStash{s.Ref, s.At, s.Message})
	}
	if err := p.JSON(out); err != nil {
		return apperr.Wrap(apperr.CodeGeneral, "cannot write JSON", "", err)
	}
	return nil
}
