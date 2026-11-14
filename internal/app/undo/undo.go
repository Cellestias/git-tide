// Package undo implements `gtide undo` and `gtide amend`: fix the last commit,
// but only while nobody else could have it.
package undo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	appcommit "github.com/cellestias/git-tide/internal/app/commit"
	"github.com/cellestias/git-tide/internal/app/hooks"
	"github.com/cellestias/git-tide/internal/apperr"
	dcommit "github.com/cellestias/git-tide/internal/domain/commit"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/domain/policy"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

// lastCommit checks the last commit may be rewritten: a branch is checked out,
// no merge or rebase is running, the commit is not a merge, and no
// remote-tracking branch has it, since rewriting a pushed commit would diverge
// from everyone who fetched it.
func lastCommit(ctx context.Context, repo *repository.Repository, verb string) (hash string, err error) {
	if _, err := repo.CurrentBranch(ctx); errors.Is(err, repository.ErrDetachedHead) {
		return "", apperr.New(apperr.CodeUnsafeRepo, "HEAD is detached; there is no branch whose last commit to "+verb, "check out a branch first")
	} else if err != nil {
		return "", apperr.Wrap(apperr.CodeExternal, "cannot determine current branch", "", err)
	}
	if op, err := repo.InProgressOperation(ctx); err != nil {
		return "", apperr.Wrap(apperr.CodeExternal, "cannot inspect repository state", "", err)
	} else if op != "" {
		return "", apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("a %s is in progress", op), "finish it with `gtide continue` or give it up with `gtide abort` first")
	}
	if started, err := repo.HasCommits(ctx); err != nil {
		return "", apperr.Wrap(apperr.CodeExternal, "cannot inspect the repository", "", err)
	} else if !started {
		return "", apperr.New(apperr.CodeUnsafeRepo, "there is no commit to "+verb, "")
	}
	if n, err := repo.ParentCount(ctx, "HEAD"); err != nil {
		return "", apperr.Wrap(apperr.CodeExternal, "cannot read the last commit", "", err)
	} else if n > 1 {
		return "", apperr.New(apperr.CodeUnsafeRepo, "the last commit is a merge; it is not rewritten here", "use `git revert -m 1 HEAD` to take a merge back")
	}
	if pushed, err := repo.ContainedInRemote(ctx, "HEAD"); err != nil {
		return "", apperr.Wrap(apperr.CodeExternal, "cannot check the remote branches", "", err)
	} else if pushed {
		return "", apperr.New(apperr.CodePolicy, "the last commit is already on the remote; rewriting it would diverge from everyone who fetched it",
			"make a new commit that fixes it, or `git revert HEAD`")
	}
	short, _ := repo.ShortHead(ctx)
	return short, nil
}

// Undo takes the last commit back: the branch moves to its parent and the
// changes of the commit stay staged, so nothing is lost and `gtide save` can
// commit them again.
func Undo(ctx context.Context, repo *repository.Repository, p output.Printer) error {
	short, err := lastCommit(ctx, repo, "undo")
	if err != nil {
		return err
	}
	if n, err := repo.ParentCount(ctx, "HEAD"); err == nil && n == 0 {
		return apperr.New(apperr.CodeUnsafeRepo, "the last commit is the first of the repository; there is nothing before it to go back to",
			"change it with `git commit --amend` if it is still local")
	}
	subject, _ := repo.LastSubject(ctx, "HEAD")
	if err := repo.ResetSoftToParent(ctx); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot undo the commit", "", err)
	}
	p.Success("Undid %s %s; its changes are staged", short, subject)
	p.Note("commit them again with `gtide save -m \"type: message\"`, or unstage with `git restore --staged .`")
	return nil
}

// AmendOptions describes what to change in the last commit.
type AmendOptions struct {
	Type, Scope, Message, Body string
	Files                      []string // stage these before amending
	Cwd                        string
}

// Amend replaces the last commit with the index (plus Files), and its message
// when one is given. The message is checked against the Conventional Commits
// rules and the staged lines against the secret scan, as in `gtide save`.
func Amend(ctx context.Context, repo *repository.Repository, cfg domain.Config, opts AmendOptions, p output.Printer) error {
	short, err := lastCommit(ctx, repo, "amend")
	if err != nil {
		return err
	}
	current, _ := repo.CurrentBranch(ctx)
	if d := protection(cfg, current); d != "" {
		return apperr.New(apperr.CodePolicy, d, "amend on a working branch; a protected branch only takes commits that come through a Pull Request")
	}

	message := ""
	if strings.TrimSpace(opts.Message) != "" {
		if message, err = appcommit.BuildMessage(opts.Type, opts.Scope, opts.Message); err != nil {
			return err
		}
		rules := dcommit.Rules{RequireScope: cfg.Commits.RequireScope}
		if err := rules.Validate(message); err != nil {
			return apperr.New(apperr.CodePolicy, "invalid commit message: "+err.Error(), "example: "+rules.Example())
		}
	} else if opts.Type != "" || opts.Scope != "" {
		return apperr.Usage("--type and --scope build the message from -m; give -m too")
	}

	paths, err := appcommit.ResolvePaths(repo.Root, opts.Cwd, opts.Files)
	if err != nil {
		return err
	}
	var snapshot string
	if len(paths) > 0 {
		if snapshot, err = repo.IndexSnapshot(ctx); err != nil {
			return apperr.Wrap(apperr.CodeExternal, "cannot snapshot the index", "", err)
		}
		if err := repo.StageFiles(ctx, paths); err != nil {
			_ = repo.RestoreIndex(ctx, snapshot)
			return apperr.Wrap(apperr.CodeExternal, "cannot stage the files", "", err)
		}
	}
	restore := func() {
		if snapshot != "" {
			_ = repo.RestoreIndex(ctx, snapshot)
			p.Note("staging was undone")
		}
	}

	staged, err := repo.StagedFiles(ctx)
	if err != nil {
		restore()
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect staged changes", "", err)
	}
	if len(staged) == 0 && message == "" {
		return apperr.New(apperr.CodeUnsafeRepo, "nothing to amend", "stage changes (`git add`, or -f <file>), or give a new message with -m")
	}
	if err := hooks.PreCommit(ctx, repo, cfg); err != nil {
		restore()
		return err
	}
	var paragraphs []string
	if message != "" {
		paragraphs = []string{message, opts.Body}
	}
	if err := repo.Amend(ctx, paragraphs...); err != nil {
		restore()
		return apperr.Wrap(apperr.CodeExternal, "amend failed", "a commit hook may have rejected it; see the message above", err)
	}
	now, _ := repo.ShortHead(ctx)
	subject, _ := repo.LastSubject(ctx, "HEAD")
	if len(staged) == 0 {
		p.Success("Amended %s into %s: %s", short, now, subject)
		return nil
	}
	p.Success("Amended %s into %s: %s (%d file%s folded in)", short, now, subject, len(staged), plural(len(staged)))
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// protection explains why the last commit of branch may not be amended, or "".
func protection(cfg domain.Config, current string) string {
	if d := (policy.Protection{Protected: cfg.Branches.Protected}).Commit(current); !d.Allowed {
		return d.Reason
	}
	return ""
}
