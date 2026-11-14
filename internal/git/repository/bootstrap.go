package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/cellestias/git-tide/internal/domain/branch"
	"github.com/cellestias/git-tide/internal/domain/remote"
	"github.com/cellestias/git-tide/internal/git/executor"
)

// Init creates a repository in dir whose first branch is named branchName.
func Init(ctx context.Context, ex executor.Executor, dir, branchName string) (*Repository, error) {
	if err := branch.ValidName(branchName); err != nil {
		return nil, err
	}
	if _, err := ex.Run(ctx, executor.Command{Args: []string{"init", "--quiet", "--initial-branch=" + branchName}, Dir: dir}); err != nil {
		return nil, err
	}
	return Open(ctx, ex, dir)
}

// Clone clones url into target, which Git creates; parent must exist.
func Clone(ctx context.Context, ex executor.Executor, parent, url, target string) error {
	if err := remote.ValidURL(url); err != nil {
		return err
	}
	_, err := ex.Run(ctx, executor.Command{Args: []string{"clone", "--quiet", "--", url, target}, Dir: parent})
	return err
}

// HasCommits reports whether HEAD points at a commit. A fresh repository has none.
func (r *Repository) HasCommits(ctx context.Context) (bool, error) {
	_, err := r.git(ctx, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err == nil {
		return true, nil
	}
	if executor.ExitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// UnbornBranch returns the branch HEAD names before its first commit, or "".
func (r *Repository) UnbornBranch(ctx context.Context) string {
	out, err := r.git(ctx, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// AddRemote registers a new remote. It never replaces an existing one.
func (r *Repository) AddRemote(ctx context.Context, name, url string) error {
	if err := remote.ValidName(name); err != nil {
		return err
	}
	if err := remote.ValidURL(url); err != nil {
		return err
	}
	_, err := r.git(ctx, "remote", "add", "--", name, strings.TrimSpace(url))
	return err
}

// SetRemoteURL changes the URL of an existing remote.
func (r *Repository) SetRemoteURL(ctx context.Context, name, url string) error {
	if err := remote.ValidName(name); err != nil {
		return err
	}
	if err := remote.ValidURL(url); err != nil {
		return err
	}
	_, err := r.git(ctx, "remote", "set-url", "--", name, strings.TrimSpace(url))
	return err
}

// RemoteLines returns "name URL" for each remote, in Git's order.
func (r *Repository) RemoteLines(ctx context.Context) ([][2]string, error) {
	names, err := r.Remotes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([][2]string, 0, len(names))
	for _, n := range names {
		u, err := r.RemoteConfiguredURL(ctx, n)
		if err != nil {
			return nil, fmt.Errorf("read the URL of %s: %w", n, err)
		}
		out = append(out, [2]string{n, u})
	}
	return out, nil
}
