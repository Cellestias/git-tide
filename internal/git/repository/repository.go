// Package repository exposes the Git operations Tide use cases need. It is not
// a general Git abstraction: add a method only when a use case calls it.
package repository

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cellestias/git-tide/internal/domain/branch"
	"github.com/cellestias/git-tide/internal/git/executor"
)

// ErrDetachedHead is returned by CurrentBranch when HEAD is not on a branch.
var ErrDetachedHead = errors.New("HEAD is detached")

// ErrNotRepository is returned by Open outside a Git work tree.
var ErrNotRepository = errors.New("not inside a Git repository")

type Repository struct {
	Root string
	exec executor.Executor
}

// Open locates the repository containing dir.
func Open(ctx context.Context, ex executor.Executor, dir string) (*Repository, error) {
	res, err := ex.Run(ctx, executor.Command{Args: []string{"rev-parse", "--show-toplevel"}, Dir: dir})
	if err != nil {
		if executor.ExitCode(err) > 0 {
			return nil, ErrNotRepository
		}
		return nil, err
	}
	root := strings.TrimSpace(res.Stdout)
	if root == "" {
		return nil, ErrNotRepository
	}
	return &Repository{Root: root, exec: ex}, nil
}

// New builds a Repository for a known root (useful in tests).
func New(ex executor.Executor, root string) *Repository {
	return &Repository{Root: root, exec: ex}
}

func (r *Repository) git(ctx context.Context, args ...string) (string, error) {
	res, err := r.exec.Run(ctx, executor.Command{Args: args, Dir: r.Root})
	return res.Stdout, err
}

// CurrentBranch returns the checked-out branch, or ErrDetachedHead.
func (r *Repository) CurrentBranch(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		if executor.ExitCode(err) == 1 {
			return "", ErrDetachedHead
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// TrackedChanges lists paths with staged or unstaged changes to tracked files.
// Untracked files are ignored: Git itself refuses to overwrite them.
func (r *Repository) TrackedChanges(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=no")
	if err != nil {
		return nil, err
	}
	var paths []string
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		paths = append(paths, e[3:])
		if e[0] == 'R' || e[0] == 'C' { // rename/copy entries carry the origin path next
			i++
		}
	}
	return paths, nil
}

// InProgressOperation names a merge, rebase, cherry-pick, or revert that has
// not been finished, or returns "".
func (r *Repository) InProgressOperation(ctx context.Context) (string, error) {
	for _, op := range []struct{ name, path string }{
		{"merge", "MERGE_HEAD"},
		{"rebase", "rebase-merge"},
		{"rebase", "rebase-apply"},
		{"cherry-pick", "CHERRY_PICK_HEAD"},
		{"revert", "REVERT_HEAD"},
	} {
		p, err := r.gitPath(ctx, op.path)
		if err != nil {
			return "", err
		}
		if exists(p) {
			return op.name, nil
		}
	}
	return "", nil
}

func (r *Repository) gitPath(ctx context.Context, name string) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--git-path", name)
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(r.Root, p)
	}
	return p, nil
}

// HooksDir returns the active hooks directory, honouring core.hooksPath.
func (r *Repository) HooksDir(ctx context.Context) (string, error) {
	return r.gitPath(ctx, "hooks")
}

// Remotes lists configured remote names.
func (r *Repository) Remotes(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "remote")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// RemoteURL returns the fetch URL of a remote.
func (r *Repository) RemoteURL(ctx context.Context, remote string) (string, error) {
	out, err := r.git(ctx, "remote", "get-url", remote)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// RemoteConfiguredURL returns the URL exactly as written in the configuration,
// before any url.<base>.insteadOf rewriting.
func (r *Repository) RemoteConfiguredURL(ctx context.Context, remote string) (string, error) {
	out, err := r.git(ctx, "config", "--get", "remote."+remote+".url")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (r *Repository) refExists(ctx context.Context, ref string) (bool, error) {
	_, err := r.git(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err == nil {
		return true, nil
	}
	if executor.ExitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// LocalBranchExists reports whether refs/heads/name exists.
func (r *Repository) LocalBranchExists(ctx context.Context, name string) (bool, error) {
	if err := branch.ValidName(name); err != nil {
		return false, err
	}
	return r.refExists(ctx, "refs/heads/"+name)
}

// RemoteBranchExists reports whether refs/remotes/<remote>/<name> exists locally.
func (r *Repository) RemoteBranchExists(ctx context.Context, remote, name string) (bool, error) {
	if err := branch.ValidName(name); err != nil {
		return false, err
	}
	return r.refExists(ctx, "refs/remotes/"+remote+"/"+name)
}

// RemoteDefaultBranch returns the branch origin/HEAD points to, or "".
func (r *Repository) RemoteDefaultBranch(ctx context.Context, remote string) string {
	out, err := r.git(ctx, "symbolic-ref", "--short", "-q", "refs/remotes/"+remote+"/HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(out), remote+"/")
}

// Fetch runs `git fetch <remote> <refspecs...>`. Refspecs are built by callers
// from validated branch names.
func (r *Repository) Fetch(ctx context.Context, remote string, refspecs ...string) error {
	args := append([]string{"fetch", "--no-tags", "--", remote}, refspecs...)
	_, err := r.git(ctx, args...)
	return err
}

func (r *Repository) Checkout(ctx context.Context, name string) error {
	if err := branch.ValidName(name); err != nil {
		return err
	}
	_, err := r.git(ctx, "checkout", "--quiet", name, "--")
	return err
}

// CheckoutBranchAt creates (or, with reset, resets) branch name to commit-ish
// start and checks it out.
func (r *Repository) CheckoutBranchAt(ctx context.Context, name, start string, reset bool) error {
	if err := branch.ValidName(name); err != nil {
		return err
	}
	flag := "-b"
	if reset {
		flag = "-B"
	}
	_, err := r.git(ctx, "checkout", "--quiet", flag, name, start, "--")
	return err
}

// MergeFastForward fast-forwards the current branch to ref or fails.
func (r *Repository) MergeFastForward(ctx context.Context, ref string) error {
	_, err := r.git(ctx, "merge", "--ff-only", "--quiet", ref)
	return err
}

// Merge merges ref into the current branch with a merge commit when needed.
func (r *Repository) Merge(ctx context.Context, ref string) error {
	_, err := r.git(ctx, "merge", "--no-edit", ref)
	return err
}

// Rebase rebases the current branch onto ref.
func (r *Repository) Rebase(ctx context.Context, ref string) error {
	_, err := r.git(ctx, "rebase", ref)
	return err
}

// IsAncestor reports whether ancestor is reachable from descendant.
func (r *Repository) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	_, err := r.git(ctx, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	if executor.ExitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// AddedLine is one line added in the index relative to HEAD.
type AddedLine struct {
	Path string
	Line int
	Text string
}

// StagedAdditions returns the lines added by the staged changes. Binary files
// and deletions produce no lines.
func (r *Repository) StagedAdditions(ctx context.Context) ([]AddedLine, error) {
	out, err := r.git(ctx, "-c", "core.quotepath=false", "diff", "--cached", "--no-color", "--no-ext-diff",
		"--no-renames", "--unified=0", "--diff-filter=ACMR")
	if err != nil {
		return nil, err
	}
	return parseAdditions(out)
}

func parseAdditions(diff string) ([]AddedLine, error) {
	var (
		lines []AddedLine
		path  string
		next  int
	)
	sc := bufio.NewScanner(strings.NewReader(diff))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "+++ "):
			// Git appends a tab after paths containing spaces.
			p := strings.TrimSuffix(strings.TrimPrefix(l, "+++ "), "\t")
			if strings.HasPrefix(p, "\"") {
				if u, err := strconv.Unquote(p); err == nil {
					p = u
				}
			}
			path = strings.TrimPrefix(p, "b/")
			if p == "/dev/null" {
				path = ""
			}
		case strings.HasPrefix(l, "@@ "):
			n, err := hunkNewStart(l)
			if err != nil {
				return nil, err
			}
			next = n
		case strings.HasPrefix(l, "+") && path != "":
			lines = append(lines, AddedLine{Path: path, Line: next, Text: l[1:]})
			next++
		}
	}
	return lines, sc.Err()
}

// hunkNewStart extracts c from "@@ -a,b +c,d @@".
func hunkNewStart(header string) (int, error) {
	i := strings.Index(header, " +")
	if i < 0 {
		return 0, fmt.Errorf("malformed hunk header %q", header)
	}
	rest := header[i+2:]
	end := strings.IndexAny(rest, ", ")
	if end < 0 {
		return 0, fmt.Errorf("malformed hunk header %q", header)
	}
	return strconv.Atoi(rest[:end])
}

// Head returns the full commit hash HEAD points to.
func (r *Repository) Head(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--verify", "HEAD")
	return strings.TrimSpace(out), err
}

// UpdateBranchFromRef fast-forwards (or creates) local branch name to ref
// without touching the work tree. It fails if the update is not a fast-forward
// or if the branch is checked out.
func (r *Repository) UpdateBranchFromRef(ctx context.Context, name, ref string) error {
	if err := branch.ValidName(name); err != nil {
		return err
	}
	return r.Fetch(ctx, ".", ref+":refs/heads/"+name)
}

// Ahead counts the commits reachable from head but not from base, and returns
// the subjects of the non-merge ones in chronological order.
func (r *Repository) Ahead(ctx context.Context, base, head string) (int, []string, error) {
	out, err := r.git(ctx, "rev-list", "--count", base+".."+head)
	if err != nil {
		return 0, nil, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, nil, fmt.Errorf("parse commit count %q: %w", out, err)
	}
	out, err = r.git(ctx, "log", "--no-merges", "--reverse", "--format=%s%x00", base+".."+head)
	if err != nil {
		return 0, nil, err
	}
	var subjects []string
	for _, s := range strings.Split(out, "\x00") {
		if s = strings.TrimSpace(s); s != "" {
			subjects = append(subjects, s)
		}
	}
	return n, subjects, nil
}

// PushBranch pushes refs/heads/name to remote and sets it as upstream. Git
// runs the pre-push hook, so Tide's own policy applies.
func (r *Repository) PushBranch(ctx context.Context, remote, name string) error {
	if err := branch.ValidName(name); err != nil {
		return err
	}
	_, err := r.git(ctx, "push", "--set-upstream", "--", remote, "refs/heads/"+name+":refs/heads/"+name)
	return err
}

// CommitExists reports whether rev (a branch, tag, remote-tracking ref, or hash)
// resolves to a commit.
func (r *Repository) CommitExists(ctx context.Context, rev string) (bool, error) {
	return r.refExists(ctx, rev)
}

// RemoteHasBranch asks the remote (over the network) whether refs/heads/name exists.
func (r *Repository) RemoteHasBranch(ctx context.Context, remote, name string) (bool, error) {
	if err := branch.ValidName(name); err != nil {
		return false, err
	}
	_, err := r.git(ctx, "ls-remote", "--exit-code", "--heads", "--", remote, "refs/heads/"+name)
	if err == nil {
		return true, nil
	}
	if executor.ExitCode(err) == 2 { // --exit-code: no matching refs
		return false, nil
	}
	return false, err
}

// CreateBranch creates branch name at start. Without track the new branch has
// no upstream, even when start is a remote-tracking ref. With checkout the
// work tree switches to it.
func (r *Repository) CreateBranch(ctx context.Context, name, start string, track, checkout bool) error {
	if err := branch.ValidName(name); err != nil {
		return err
	}
	trackFlag := "--no-track"
	if track {
		trackFlag = "--track"
	}
	if checkout {
		_, err := r.git(ctx, "switch", trackFlag, "-c", name, start)
		return err
	}
	_, err := r.git(ctx, "branch", trackFlag, name, start)
	return err
}

// PreviousBranch returns the branch checked out before the current one
// (Git's "@{-1}"), or "" when there is none or it was a detached HEAD.
func (r *Repository) PreviousBranch(ctx context.Context) string {
	out, err := r.git(ctx, "rev-parse", "--abbrev-ref", "@{-1}")
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(out)
	if name == "" || branch.ValidName(name) != nil {
		return ""
	}
	if ok, _ := r.refExists(ctx, "refs/heads/"+name); !ok {
		return ""
	}
	return name
}
