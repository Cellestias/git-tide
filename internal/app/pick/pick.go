// Package pick implements `gtide pick` and `gtide revert`: copy commits of
// another branch onto the current one, or take commits of this one back, each
// as a commit of its own and under the rules of the repository.
package pick

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cellestias/git-tide/internal/app/hooks"
	"github.com/cellestias/git-tide/internal/app/update"
	"github.com/cellestias/git-tide/internal/apperr"
	dcommit "github.com/cellestias/git-tide/internal/domain/commit"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/domain/policy"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

const maxShown = 5

// Commit is a commit a command acted on.
type Commit struct {
	Hash    string `json:"commit"`
	Subject string `json:"subject"`
}

// Skipped is a commit left alone, and why.
type Skipped struct {
	Hash   string `json:"commit"`
	Reason string `json:"reason"`
}

// Result is what a command did, for --json.
type Result struct {
	Done    []Commit  `json:"done"`
	Skipped []Skipped `json:"skipped"`
}

// guard checks the repository is in a state where commits may be added to the
// current branch: a branch that is not protected, nothing half done, and no
// uncommitted change that Git would have to mix the new commits with.
func guard(ctx context.Context, repo *repository.Repository, cfg domain.Config, command string) (string, error) {
	current, err := repo.CurrentBranch(ctx)
	if errors.Is(err, repository.ErrDetachedHead) {
		return "", apperr.New(apperr.CodeUnsafeRepo, "HEAD is detached; there is no branch to add commits to", "check out a branch first")
	} else if err != nil {
		return "", apperr.Wrap(apperr.CodeExternal, "cannot determine current branch", "", err)
	}
	if started, err := repo.HasCommits(ctx); err != nil {
		return "", apperr.Wrap(apperr.CodeExternal, "cannot inspect the repository", "", err)
	} else if !started {
		return "", apperr.New(apperr.CodeUnsafeRepo, "the repository has no commit yet", "")
	}
	if d := (policy.Protection{Protected: cfg.Branches.Protected}).Commit(current); !d.Allowed {
		return "", apperr.New(apperr.CodePolicy, d.Reason, "work on a branch of your own (`gtide create <branch>`); a protected branch only takes commits that come through a Pull Request")
	}
	if err := update.EnsureSafe(ctx, repo, command); err != nil {
		return "", err
	}
	return current, nil
}

// resolve turns what the user typed into commits: a commit-ish (a hash, a
// branch, a tag), or a range <from>..<to>, which stands for the commits of <to>
// that <from> lacks. A range is listed oldest first, or newest first with
// newestFirst, and a commit named twice counts once. A merge commit is refused:
// which of its parents to follow is a decision for the user.
func resolve(ctx context.Context, repo *repository.Repository, specs []string, newestFirst bool) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(h string) {
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	for _, spec := range specs {
		if strings.Contains(spec, "...") {
			return nil, apperr.Usage("%q: use <from>..<to> with two dots", spec)
		}
		if from, to, isRange := strings.Cut(spec, ".."); isRange {
			a, err := commitOf(ctx, repo, from)
			if err != nil {
				return nil, err
			}
			b, err := commitOf(ctx, repo, to)
			if err != nil {
				return nil, err
			}
			list, err := repo.RevList(ctx, a, b)
			if err != nil {
				return nil, apperr.Wrap(apperr.CodeExternal, fmt.Sprintf("cannot list %s", spec), "", err)
			}
			if len(list) == 0 {
				return nil, apperr.Usage("%s holds no commit: %s has nothing that %s lacks", spec, to, from)
			}
			if newestFirst {
				for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
					list[i], list[j] = list[j], list[i]
				}
			}
			for _, h := range list {
				add(h)
			}
			continue
		}
		h, err := commitOf(ctx, repo, spec)
		if err != nil {
			return nil, err
		}
		add(h)
	}
	if len(out) == 0 {
		return nil, apperr.Usage("give at least one commit")
	}
	for _, h := range out {
		if n, err := repo.ParentCount(ctx, h); err != nil {
			return nil, apperr.Wrap(apperr.CodeExternal, "cannot read a commit", "", err)
		} else if n > 1 {
			return nil, apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("%s is a merge commit; it has no single change to follow", short(h)),
				"pick or revert it with git, choosing the parent: git cherry-pick -m 1 "+short(h))
		}
	}
	return out, nil
}

func commitOf(ctx context.Context, repo *repository.Repository, rev string) (string, error) {
	h, err := repo.ResolveCommit(ctx, rev)
	if err != nil || h == "" {
		return "", apperr.Usage("%q is not a commit, a branch or a tag of this repository", rev)
	}
	return h, nil
}

func short(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// inProgress turns a pick or revert that stopped on conflicts into the error
// that says how to go on.
func inProgress(ctx context.Context, repo *repository.Repository, verb string, cause error) error {
	if op, _ := repo.InProgressOperation(ctx); op != "" {
		msg := fmt.Sprintf("conflicts while running the %s", verb)
		if files, _ := repo.UnmergedFiles(ctx); len(files) > 0 {
			shown := files
			if len(shown) > maxShown {
				shown = shown[:maxShown]
			}
			msg += ": " + strings.Join(shown, ", ")
			if len(files) > len(shown) {
				msg += fmt.Sprintf(" and %d more", len(files)-len(shown))
			}
		}
		return apperr.New(apperr.CodeGeneral, msg,
			"resolve the conflicts, `git add` the files, then `gtide continue`; or give up with `gtide abort`")
	}
	return apperr.Wrap(apperr.CodeExternal, fmt.Sprintf("the %s failed", verb), "the repository was left as Git reports it; run `gtide status`", cause)
}

// Pick copies commits onto the current branch, oldest first, keeping their
// author and message. Before anything changes, each message is checked against
// the Conventional Commits rules and each commit's lines against the secret scan
// and the conflict-marker check, since Git runs no commit hook for a cherry-pick.
// A commit the branch already holds, or holds under another hash, is skipped.
func Pick(ctx context.Context, repo *repository.Repository, cfg domain.Config, specs []string, res *Result, p output.Printer) error {
	current, err := guard(ctx, repo, cfg, "gtide pick")
	if err != nil {
		return err
	}
	commits, err := resolve(ctx, repo, specs, false)
	if err != nil {
		return err
	}

	var todo []string
	for _, h := range commits {
		if in, err := repo.IsAncestor(ctx, h, "HEAD"); err != nil {
			return apperr.Wrap(apperr.CodeExternal, "cannot compare with the current branch", "", err)
		} else if in {
			res.Skipped = append(res.Skipped, Skipped{h, "already in " + current})
			continue
		}
		if same, err := repo.Cherry(ctx, "HEAD", h); err == nil && same[h] {
			res.Skipped = append(res.Skipped, Skipped{h, "an equivalent change is already in " + current})
			continue
		}
		todo = append(todo, h)
	}
	for _, s := range res.Skipped {
		p.Note("skipping %s: %s", short(s.Hash), s.Reason)
	}
	if len(todo) == 0 {
		p.Success("Nothing to pick: %s already has these changes", current)
		return nil
	}

	rules := dcommit.Rules{RequireScope: cfg.Commits.RequireScope}
	for _, h := range todo {
		msg, err := repo.CommitMessage(ctx, h)
		if err != nil {
			return apperr.Wrap(apperr.CodeExternal, "cannot read a commit message", "", err)
		}
		if err := rules.Validate(msg); err != nil {
			return apperr.New(apperr.CodePolicy, fmt.Sprintf("commit %s: invalid commit message: %s", short(h), err),
				"reword it on its own branch first; example: "+rules.Example())
		}
		added, err := repo.CommitAdditions(ctx, h)
		if err != nil {
			return apperr.Wrap(apperr.CodeExternal, "cannot read the changes of a commit", "", err)
		}
		if err := hooks.CheckAdditions(cfg, added, "commit "+short(h)); err != nil {
			return err
		}
	}

	p.Step("Picking %d commit%s onto %s", len(todo), plural(len(todo)), current)
	if err := repo.CherryPick(ctx, todo); err != nil {
		return inProgress(ctx, repo, "cherry-pick", err)
	}
	for _, h := range todo {
		subject, _ := repo.LastSubject(ctx, h)
		res.Done = append(res.Done, Commit{h, subject})
	}
	p.Success("Picked %d commit%s onto %s", len(todo), plural(len(todo)), current)
	return nil
}

// Revert makes a new commit that takes back each given commit, newest first
// for a range. Unlike `gtide undo`, it works on commits already published: it
// adds to the history instead of rewriting it.
func Revert(ctx context.Context, repo *repository.Repository, cfg domain.Config, specs []string, res *Result, p output.Printer) error {
	current, err := guard(ctx, repo, cfg, "gtide revert")
	if err != nil {
		return err
	}
	commits, err := resolve(ctx, repo, specs, true)
	if err != nil {
		return err
	}
	for _, h := range commits {
		if in, err := repo.IsAncestor(ctx, h, "HEAD"); err != nil {
			return apperr.Wrap(apperr.CodeExternal, "cannot compare with the current branch", "", err)
		} else if !in {
			return apperr.New(apperr.CodeUsage, fmt.Sprintf("%s is not in %s, so there is nothing here to take back", short(h), current),
				"check out the branch that holds it, or use `gtide history` to find the commit")
		}
	}
	p.Step("Reverting %d commit%s on %s", len(commits), plural(len(commits)), current)
	if err := repo.Revert(ctx, commits); err != nil {
		return inProgress(ctx, repo, "revert", err)
	}
	for _, h := range commits {
		subject, _ := repo.LastSubject(ctx, h)
		res.Done = append(res.Done, Commit{h, subject})
	}
	p.Success("Reverted %d commit%s on %s", len(commits), plural(len(commits)), current)
	return nil
}
