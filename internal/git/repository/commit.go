package repository

import (
	"context"
	"strings"
)

// StagedFiles lists the paths that differ between the index and HEAD.
func (r *Repository) StagedFiles(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "-c", "core.quotepath=false", "diff", "--cached", "--name-only", "-z", "--no-renames")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// StageFiles adds paths (relative to the repository root) to the index. Paths
// are literal: pathspec magic and globs are not interpreted.
func (r *Repository) StageFiles(ctx context.Context, paths []string) error {
	_, err := r.git(ctx, append([]string{"--literal-pathspecs", "add", "--"}, paths...)...)
	return err
}

// StageTracked stages modifications and deletions of tracked files only.
func (r *Repository) StageTracked(ctx context.Context) error {
	_, err := r.git(ctx, "add", "--update")
	return err
}

// IndexSnapshot saves the current index as a tree object, for RestoreIndex.
func (r *Repository) IndexSnapshot(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "write-tree")
	return strings.TrimSpace(out), err
}

// RestoreIndex resets the index to a tree from IndexSnapshot. The work tree is
// not touched.
func (r *Repository) RestoreIndex(ctx context.Context, tree string) error {
	_, err := r.git(ctx, "read-tree", tree)
	return err
}

// Commit commits the index. Git runs its hooks; Tide never skips them. Each
// non-empty paragraph becomes one -m, so the first is the subject.
func (r *Repository) Commit(ctx context.Context, paragraphs ...string) error {
	args := []string{"commit", "--quiet"}
	for _, p := range paragraphs {
		if p != "" {
			args = append(args, "-m", p)
		}
	}
	_, err := r.git(ctx, args...)
	return err
}

// ShortHead returns the abbreviated hash of HEAD.
func (r *Repository) ShortHead(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--short", "HEAD")
	return strings.TrimSpace(out), err
}
