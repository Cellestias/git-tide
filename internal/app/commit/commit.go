// Package commit implements `tide commit` and `tide save`: validate, optionally
// stage, and commit, with Tide's policy checked before anything changes.
package commit

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cellestias/git-tide/internal/app/hooks"
	"github.com/cellestias/git-tide/internal/apperr"
	dcommit "github.com/cellestias/git-tide/internal/domain/commit"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/domain/policy"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

type Options struct {
	Message string
	Body    string
	Type    string // with Message, builds "type(scope): message"
	Scope   string
	// Files are staged first (`tide save`). Relative paths are resolved
	// against Cwd. When Files is set, nothing else may already be staged.
	Files []string
	Cwd   string
	// All stages modifications and deletions of tracked files (`tide commit -a`).
	All bool
	// Everything stages every change, new files included (`tide save` without -f).
	Everything bool
}

func Run(ctx context.Context, repo *repository.Repository, cfg domain.Config, opts Options, p output.Printer) error {
	if op, err := repo.InProgressOperation(ctx); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect repository state", "", err)
	} else if op != "" {
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("a %s is in progress", op),
			"finish it with `git commit` (or abort it) before using tide commit")
	}

	current, err := repo.CurrentBranch(ctx)
	if err != nil && !errors.Is(err, repository.ErrDetachedHead) {
		return apperr.Wrap(apperr.CodeExternal, "cannot determine current branch", "", err)
	}
	if d := (policy.Protection{Protected: cfg.Branches.Protected}).Commit(current); !d.Allowed {
		return apperr.New(apperr.CodePolicy, d.Reason, "")
	}

	message, err := BuildMessage(opts.Type, opts.Scope, opts.Message)
	if err != nil {
		return err
	}
	rules := dcommit.Rules{RequireScope: cfg.Commits.RequireScope}
	if err := rules.Validate(message); err != nil {
		return apperr.New(apperr.CodePolicy, "invalid commit message: "+err.Error(), "example: "+rules.Example())
	}

	paths, err := resolvePaths(repo.Root, opts.Cwd, opts.Files)
	if err != nil {
		return err
	}
	if len(paths) > 0 {
		staged, err := repo.StagedFiles(ctx)
		if err != nil {
			return apperr.Wrap(apperr.CodeExternal, "cannot inspect staged changes", "", err)
		}
		if other := outside(staged, paths); len(other) > 0 {
			return apperr.New(apperr.CodeUnsafeRepo,
				fmt.Sprintf("other files are already staged: %s", list(other)),
				"unstage them (git restore --staged <file>) or commit them first with `tide commit`")
		}
	}

	staging := len(paths) > 0 || opts.All || opts.Everything
	var snapshot string
	if staging {
		if snapshot, err = repo.IndexSnapshot(ctx); err != nil {
			return apperr.Wrap(apperr.CodeExternal, "cannot snapshot the index", "", err)
		}
		switch {
		case len(paths) > 0:
			err = repo.StageFiles(ctx, paths)
		case opts.Everything:
			err = repo.StageAll(ctx)
		default:
			err = repo.StageTracked(ctx)
		}
		if err != nil {
			undo(ctx, repo, snapshot, p)
			return apperr.Wrap(apperr.CodeExternal, "cannot stage the files", "check that every path exists or is tracked, and is not ignored", err)
		}
	}

	staged, err := repo.StagedFiles(ctx)
	if err != nil {
		undo(ctx, repo, snapshot, p)
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect staged changes", "", err)
	}
	if len(staged) == 0 {
		hint := "stage changes with `git add`, or use `tide save -f <file>`"
		if staging {
			hint = "the given files have no changes"
		}
		return apperr.New(apperr.CodeUnsafeRepo, "nothing to commit", hint)
	}

	if opts.Everything {
		p.Note("staged %d file(s): %s", len(staged), list(staged))
	}
	if err := hooks.PreCommit(ctx, repo, cfg); err != nil {
		if staging {
			undo(ctx, repo, snapshot, p)
			p.Note("staging was undone; nothing is staged")
		}
		return err
	}

	if err := repo.Commit(ctx, message, opts.Body); err != nil {
		if staging {
			undo(ctx, repo, snapshot, p)
		}
		return apperr.Wrap(apperr.CodeExternal, "commit failed", "a commit hook may have rejected it; see the message above", err)
	}
	short, _ := repo.ShortHead(ctx)
	p.Success("Committed %s on %s: %s (%d file%s)", short, branchLabel(current), dcommit.Subject(message), len(staged), plural(len(staged)))
	return nil
}

// BuildMessage composes the commit subject. With a type it builds
// "type(scope): message"; without one the message is used as given.
func BuildMessage(typ, scope, message string) (string, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return "", apperr.Usage("a commit message is required: -m \"feat: add login form\"")
	}
	if typ == "" {
		if scope != "" {
			return "", apperr.Usage("--scope needs --type")
		}
		return message, nil
	}
	if scope != "" {
		return fmt.Sprintf("%s(%s): %s", typ, scope, message), nil
	}
	return fmt.Sprintf("%s: %s", typ, message), nil
}

// resolvePaths turns user paths (relative to cwd) into literal paths relative
// to the repository root, rejecting anything outside it.
func resolvePaths(root, cwd string, files []string) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	evalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeExternal, "cannot resolve the repository root", "", err)
	}
	evalCwd := evalRoot
	if cwd != "" {
		if evalCwd, err = filepath.EvalSymlinks(cwd); err != nil {
			return nil, apperr.Wrap(apperr.CodeExternal, "cannot resolve the current directory", "", err)
		}
	}
	var out []string
	for _, f := range files {
		if strings.TrimSpace(f) == "" || strings.ContainsRune(f, 0) {
			return nil, apperr.Usage("invalid file path %q", f)
		}
		abs := f
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(evalCwd, f)
		}
		rel, err := filepath.Rel(evalRoot, filepath.Clean(abs))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, apperr.Usage("%q is outside the repository", f)
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out, nil
}

// outside returns the staged paths not covered by any requested path.
func outside(staged, paths []string) []string {
	var other []string
	for _, s := range staged {
		covered := false
		for _, p := range paths {
			if p == "." || s == p || strings.HasPrefix(s, strings.TrimSuffix(p, "/")+"/") {
				covered = true
				break
			}
		}
		if !covered {
			other = append(other, s)
		}
	}
	return other
}

func list(files []string) string {
	const max = 5
	if len(files) > max {
		return strings.Join(files[:max], ", ") + fmt.Sprintf(" and %d more", len(files)-max)
	}
	return strings.Join(files, ", ")
}

func undo(ctx context.Context, repo *repository.Repository, snapshot string, p output.Printer) {
	if snapshot == "" {
		return
	}
	if err := repo.RestoreIndex(ctx, snapshot); err != nil {
		p.Warn("could not restore the index: %v", err)
	}
}

func branchLabel(b string) string {
	if b == "" {
		return "a detached HEAD"
	}
	return b
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
