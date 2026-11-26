package repository

import (
	"context"
	"errors"
	"strings"
)

var errBadRev = errors.New("a revision cannot start with \"-\"")

// ResolveCommit returns the full hash of the commit rev names, or an error when
// it names none. A rev that starts with "-" is refused, so it can never be read
// as an option.
func (r *Repository) ResolveCommit(ctx context.Context, rev string) (string, error) {
	if strings.HasPrefix(rev, "-") {
		return "", errBadRev
	}
	out, err := r.git(ctx, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	return strings.TrimSpace(out), err
}

// RevList lists the commits of the range from..to (those of to that from lacks),
// oldest first, as full hashes. Both ends must have been checked with ResolveCommit.
func (r *Repository) RevList(ctx context.Context, from, to string) ([]string, error) {
	out, err := r.git(ctx, "rev-list", "--reverse", "--topo-order", from+".."+to)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// CommitMessage returns the full message of a commit.
func (r *Repository) CommitMessage(ctx context.Context, rev string) (string, error) {
	out, err := r.git(ctx, "log", "-1", "--format=%B", rev, "--")
	return strings.TrimSpace(out), err
}

// CommitAdditions returns the lines a commit adds. A merge commit adds none of its own.
func (r *Repository) CommitAdditions(ctx context.Context, rev string) ([]AddedLine, error) {
	out, err := r.git(ctx, "-c", "core.quotepath=false", "show", "--format=", "--no-color", "--no-ext-diff", "--no-textconv",
		"--no-renames", "--unified=0", "--diff-filter=ACMR", rev, "--")
	if err != nil {
		return nil, err
	}
	return parseAdditions(out)
}

// CherryPick applies commits, in order, each as a commit of its own that keeps
// the author and the message. It stops on the first conflict and leaves the
// cherry-pick in progress, for `gtide continue` or `gtide abort`.
func (r *Repository) CherryPick(ctx context.Context, commits []string) error {
	_, err := r.git(ctx, append([]string{"cherry-pick"}, commits...)...)
	return err
}

// Revert makes a new commit that takes back each of commits, in order. It stops
// on the first conflict and leaves the revert in progress.
func (r *Repository) Revert(ctx context.Context, commits []string) error {
	_, err := r.git(ctx, append([]string{"-c", "core.editor=true", "revert", "--no-edit"}, commits...)...)
	return err
}

// PathInCommit reports whether rev holds a file or folder at path.
func (r *Repository) PathInCommit(ctx context.Context, rev, path string) (bool, error) {
	out, err := r.git(ctx, "ls-tree", "-r", "--name-only", "-z", rev, "--", path)
	if err != nil {
		return false, err
	}
	return strings.Trim(out, "\x00") != "", nil
}

// StageFromCommit puts the version of paths held by rev in the index. The work
// tree is not touched, so what the files hold now can still be looked at.
func (r *Repository) StageFromCommit(ctx context.Context, rev string, paths []string) error {
	_, err := r.git(ctx, append([]string{"--literal-pathspecs", "restore", "--source=" + rev, "--staged", "--"}, paths...)...)
	return err
}

// CheckoutFromIndex overwrites the work-tree copy of paths with what the index holds.
func (r *Repository) CheckoutFromIndex(ctx context.Context, paths []string) error {
	_, err := r.git(ctx, append([]string{"--literal-pathspecs", "restore", "--worktree", "--"}, paths...)...)
	return err
}
