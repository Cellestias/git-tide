package repository

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/cellestias/git-tide/internal/git/executor"
)

// MinCompareGit is the oldest Git whose `merge-tree --write-tree` takes
// --merge-base, which the cherry-pick simulation needs.
const (
	MinCompareMajor = 2
	MinCompareMinor = 40
)

var gitVersion = regexp.MustCompile(`git version (\d+)\.(\d+)`)

// GitVersion returns the major and minor version of the Git binary.
func (r *Repository) GitVersion(ctx context.Context) (major, minor int, err error) {
	out, err := r.git(ctx, "version")
	if err != nil {
		return 0, 0, err
	}
	m := gitVersion.FindStringSubmatch(out)
	if m == nil {
		return 0, 0, fmt.Errorf("cannot parse %q", strings.TrimSpace(out))
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, nil
}

// MergeBase returns the best common ancestor of a and b, or "" when they share none.
func (r *Repository) MergeBase(ctx context.Context, a, b string) (string, error) {
	out, err := r.git(ctx, "merge-base", a, b)
	if err != nil {
		if executor.ExitCode(err) == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Cherry looks at the non-merge commits of head that upstream lacks and
// reports, by full hash, whether upstream already holds an equivalent patch
// (a commit cherry-picked earlier under another hash).
func (r *Repository) Cherry(ctx context.Context, upstream, head string) (map[string]bool, error) {
	out, err := r.git(ctx, "cherry", upstream, head)
	if err != nil {
		return nil, err
	}
	equivalent := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if mark, hash, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			equivalent[hash] = mark == "-"
		}
	}
	return equivalent, nil
}

// PickResult is the outcome of a simulated cherry-pick.
type PickResult struct {
	Tree      string   // the resulting tree; with conflicts, the one with markers
	Conflicts []string // paths that conflict; empty when the pick is clean
}

// SimulatePick applies commit onto the commit-ish onto in memory, the way
// `git cherry-pick` would, without touching the work tree, the index or any
// ref. It needs Git 2.40 or later.
func (r *Repository) SimulatePick(ctx context.Context, onto, commit string) (PickResult, error) {
	out, err := r.git(ctx, "-c", "core.quotepath=false", "merge-tree", "--write-tree", "--name-only", "--no-messages",
		"--merge-base="+commit+"^", onto, commit)
	if err != nil && executor.ExitCode(err) != 1 {
		return PickResult{}, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	res := PickResult{Tree: strings.TrimSpace(lines[0])}
	if err != nil { // exit 1: conflicts, listed after the tree
		for _, l := range lines[1:] {
			if l = strings.TrimSpace(l); l != "" {
				res.Conflicts = append(res.Conflicts, l)
			}
		}
	}
	return res, nil
}

// CommitTree wraps tree in a throwaway commit on top of parent, so it can be the
// "onto" of the next SimulatePick. The object is unreachable and Git's garbage
// collection removes it; no ref changes.
func (r *Repository) CommitTree(ctx context.Context, tree, parent string) (string, error) {
	out, err := r.git(ctx, "-c", "user.name=gtide", "-c", "user.email=gtide@localhost",
		"commit-tree", tree, "-p", parent, "-m", "gtide compare")
	return strings.TrimSpace(out), err
}

// Tree returns the tree object id of a commit-ish.
func (r *Repository) Tree(ctx context.Context, rev string) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--verify", rev+"^{tree}")
	return strings.TrimSpace(out), err
}

// ChangedFiles lists the paths that differ between two commit-ishes.
func (r *Repository) ChangedFiles(ctx context.Context, from, to string) ([]string, error) {
	out, err := r.git(ctx, "-c", "core.quotepath=false", "diff", "--name-only", "--no-renames", from, to, "--")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(out, "\n") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}
