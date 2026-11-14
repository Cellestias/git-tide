package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/cellestias/git-tide/internal/domain/branch"
)

// StatusEntry is one path of `git status`: X is the index column and Y the
// work-tree column, as in the porcelain format.
type StatusEntry struct {
	X, Y byte
	Path string
	From string // the old path of a rename or copy
}

// Staged reports whether the index holds a change for this path.
func (e StatusEntry) Staged() bool { return e.X != ' ' && e.X != '?' && e.X != '!' && !e.Conflict() }

// Unstaged reports whether the work tree differs from the index.
func (e StatusEntry) Unstaged() bool { return e.Y != ' ' && e.Y != '?' && e.Y != '!' && !e.Conflict() }

// Untracked reports whether Git does not know the path.
func (e StatusEntry) Untracked() bool { return e.X == '?' && e.Y == '?' }

// Conflict reports an unmerged path.
func (e StatusEntry) Conflict() bool {
	return e.X == 'U' || e.Y == 'U' || (e.X == 'A' && e.Y == 'A') || (e.X == 'D' && e.Y == 'D')
}

// Status lists every changed path, untracked files included. Ignored files are left out.
func (r *Repository) Status(ctx context.Context) ([]StatusEntry, error) {
	out, err := r.git(ctx, "-c", "core.quotepath=false", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var entries []StatusEntry
	parts := strings.Split(out, "\x00")
	for i := 0; i < len(parts); i++ {
		rec := parts[i]
		if len(rec) < 4 {
			continue
		}
		e := StatusEntry{X: rec[0], Y: rec[1], Path: rec[3:]}
		if (e.X == 'R' || e.X == 'C' || e.Y == 'R' || e.Y == 'C') && i+1 < len(parts) {
			i++
			e.From = parts[i]
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// Upstream returns the remote-tracking ref the current branch follows (for
// example "origin/feature/x"), or "" when it has none.
func (r *Repository) Upstream(ctx context.Context) string {
	out, err := r.git(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// ChangeStat returns the changed files with line counts: the index against HEAD
// when staged, otherwise the work tree against the index. A path limits it.
func (r *Repository) ChangeStat(ctx context.Context, staged bool, path string) ([]FileChange, error) {
	args := []string{"-c", "core.quotepath=false", "--literal-pathspecs", "diff", "--numstat", "--no-renames"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--")
	if path != "" {
		args = append(args, path)
	}
	out, err := r.git(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseNumstat(out), nil
}

// ChangePatch returns the full textual diff, without colour.
func (r *Repository) ChangePatch(ctx context.Context, staged bool, path string) (string, error) {
	args := []string{"-c", "core.quotepath=false", "--literal-pathspecs", "diff", "--no-color", "--no-ext-diff", "--no-textconv"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--")
	if path != "" {
		args = append(args, path)
	}
	return r.git(ctx, args...)
}

// DeleteLocalBranch removes a local branch. Callers decide whether it is safe:
// this forces the deletion, so unmerged work is lost.
func (r *Repository) DeleteLocalBranch(ctx context.Context, name string) error {
	if err := branch.ValidName(name); err != nil {
		return err
	}
	_, err := r.git(ctx, "branch", "--delete", "--force", "--", name)
	return err
}

// DeleteRemoteBranch removes a branch from the remote (network).
func (r *Repository) DeleteRemoteBranch(ctx context.Context, remote, name string) error {
	if err := branch.ValidName(name); err != nil {
		return err
	}
	_, err := r.git(ctx, "push", "--delete", "--", remote, name)
	return err
}

// RenameBranch renames a local branch.
func (r *Repository) RenameBranch(ctx context.Context, oldName, newName string) error {
	if err := branch.ValidName(oldName); err != nil {
		return err
	}
	if err := branch.ValidName(newName); err != nil {
		return err
	}
	_, err := r.git(ctx, "branch", "--move", "--", oldName, newName)
	return err
}

// StashEntry is one saved stash.
type StashEntry struct {
	Ref     string // stash@{0}
	When    string // relative date
	Message string
}

// StashPush saves local changes. Untracked files are included only when asked.
func (r *Repository) StashPush(ctx context.Context, message string, untracked bool) error {
	args := []string{"stash", "push", "--quiet"}
	if untracked {
		args = append(args, "--include-untracked")
	}
	if message != "" {
		args = append(args, "--message", message)
	}
	_, err := r.git(ctx, args...)
	return err
}

// StashList lists the stashes, newest first.
func (r *Repository) StashList(ctx context.Context) ([]StashEntry, error) {
	out, err := r.git(ctx, "stash", "list", "--format=%gd%x00%cr%x00%gs")
	if err != nil {
		return nil, err
	}
	var list []StashEntry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.SplitN(line, "\x00", 3)
		if len(f) == 3 {
			list = append(list, StashEntry{Ref: f[0], When: f[1], Message: f[2]})
		}
	}
	return list, nil
}

// StashPop applies a stash and drops it when it applies cleanly.
func (r *Repository) StashPop(ctx context.Context, ref string) error {
	_, err := r.git(ctx, "stash", "pop", "--quiet", ref)
	return err
}

// StashDrop deletes a stash.
func (r *Repository) StashDrop(ctx context.Context, ref string) error {
	_, err := r.git(ctx, "stash", "drop", "--quiet", ref)
	return err
}

// TagInfo is one tag.
type TagInfo struct {
	Name      string
	Annotated bool
	Date      string
	Subject   string
}

// Tags lists tags, newest version first.
func (r *Repository) Tags(ctx context.Context) ([]TagInfo, error) {
	out, err := r.git(ctx, "tag", "--list", "--sort=-version:refname",
		"--format=%(refname:short)%00%(objecttype)%00%(creatordate:short)%00%(subject)")
	if err != nil {
		return nil, err
	}
	var tags []TagInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.SplitN(line, "\x00", 4)
		if len(f) == 4 {
			tags = append(tags, TagInfo{Name: f[0], Annotated: f[1] == "tag", Date: f[2], Subject: f[3]})
		}
	}
	return tags, nil
}

// TagExists reports whether the local tag exists.
func (r *Repository) TagExists(ctx context.Context, name string) (bool, error) {
	return r.refExists(ctx, "refs/tags/"+name)
}

// CreateTag makes an annotated tag on HEAD. It never moves an existing tag.
func (r *Repository) CreateTag(ctx context.Context, name, message string) error {
	_, err := r.git(ctx, "tag", "--annotate", "--message", message, "--", name)
	return err
}

// DeleteLocalTag removes a local tag.
func (r *Repository) DeleteLocalTag(ctx context.Context, name string) error {
	_, err := r.git(ctx, "tag", "--delete", "--", name)
	return err
}

// PushTag publishes one tag (network).
func (r *Repository) PushTag(ctx context.Context, remote, name string) error {
	_, err := r.git(ctx, "push", "--", remote, "refs/tags/"+name+":refs/tags/"+name)
	return err
}

// DeleteRemoteTag removes a tag from the remote (network).
func (r *Repository) DeleteRemoteTag(ctx context.Context, remote, name string) error {
	_, err := r.git(ctx, "push", "--", remote, ":refs/tags/"+name)
	return err
}

// ContainedInRemote reports whether any remote-tracking branch already holds rev.
func (r *Repository) ContainedInRemote(ctx context.Context, rev string) (bool, error) {
	out, err := r.git(ctx, "branch", "--remotes", "--contains", rev)
	if err != nil {
		return false, err
	}
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.Contains(l, "->") { // skip "origin/HEAD -> origin/main"
			return true, nil
		}
	}
	return false, nil
}

// ParentCount returns how many parents rev has: 0 for a root commit, 2 or more for a merge.
func (r *Repository) ParentCount(ctx context.Context, rev string) (int, error) {
	out, err := r.git(ctx, "rev-list", "--parents", "-n", "1", rev)
	if err != nil {
		return 0, err
	}
	return len(strings.Fields(out)) - 1, nil
}

// ResetSoftToParent moves the branch to the parent of HEAD and keeps the
// changes of the undone commit staged.
func (r *Repository) ResetSoftToParent(ctx context.Context) error {
	_, err := r.git(ctx, "reset", "--soft", "HEAD~1")
	return err
}

// Amend replaces the last commit with the index. An empty message keeps the old one.
func (r *Repository) Amend(ctx context.Context, paragraphs ...string) error {
	args := []string{"commit", "--quiet", "--amend"}
	has := false
	for _, p := range paragraphs {
		if p != "" {
			args = append(args, "-m", p)
			has = true
		}
	}
	if !has {
		args = append(args, "--no-edit")
	}
	_, err := r.git(ctx, args...)
	return err
}

// LastSubject returns the subject of rev.
func (r *Repository) LastSubject(ctx context.Context, rev string) (string, error) {
	out, err := r.git(ctx, "log", "-1", "--format=%s", rev, "--")
	if err != nil {
		return "", fmt.Errorf("read the subject of %s: %w", rev, err)
	}
	return strings.TrimSpace(out), nil
}
