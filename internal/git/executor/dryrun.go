package executor

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/cellestias/git-tide/internal/dryrun"
)

// DryRun runs the Git commands that only read and records, without running,
// the ones that change the repository or the remote. A skipped command answers
// with success and no output, so the use case goes on to the next step.
//
// What stages files is the exception: it runs against a throwaway copy of the
// index (GIT_INDEX_FILE), and every read uses that copy too, so the next step
// sees what was staged and the real index is never touched. Anything else a
// skipped command would have changed is not seen: the state read afterwards is
// the one before the run.
type DryRun struct {
	Inner Executor
	Plan  *dryrun.Plan
	// st is shared by the copies of the value, which are passed around by value.
	st *dryState
}

type dryState struct {
	mu    sync.Mutex
	index string
	err   error
	done  bool
}

// NewDryRun returns a DryRun over inner that records into plan.
func NewDryRun(inner Executor, plan *dryrun.Plan) DryRun {
	return DryRun{Inner: inner, Plan: plan, st: &dryState{}}
}

// Close removes the copy of the index.
func (d DryRun) Close() {
	d.st.mu.Lock()
	defer d.st.mu.Unlock()
	if d.st.index != "" {
		_ = os.Remove(d.st.index)
		d.st.index = ""
	}
}

// scratchIndex copies the repository's index to a temporary file, once.
func (d DryRun) scratchIndex(ctx context.Context, dir string) (string, error) {
	d.st.mu.Lock()
	defer d.st.mu.Unlock()
	if d.st.done {
		return d.st.index, d.st.err
	}
	d.st.done = true
	res, err := d.Inner.Run(ctx, Command{Args: []string{"rev-parse", "--path-format=absolute", "--git-path", "index"}, Dir: dir})
	if err != nil {
		d.st.err = err
		return "", err
	}
	src := strings.TrimSpace(res.Stdout)
	tmp, err := os.CreateTemp("", "gtide-dry-index-*")
	if err != nil {
		d.st.err = err
		return "", err
	}
	data, rerr := os.ReadFile(src) //nolint:gosec // the path comes from git for this repository
	if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		d.st.err = rerr
		return "", rerr
	}
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		d.st.err = werr
		return "", werr
	}
	d.st.index = tmp.Name()
	return d.st.index, nil
}

func (d DryRun) Run(ctx context.Context, c Command) (Result, error) {
	if d.st == nil {
		d.st = &dryState{}
	}
	read, stages := ReadOnly(c.Args), stagesOnly(c.Args)
	if !read && !stages {
		d.Plan.Add("git " + RedactArgs(display(c.Args)))
		return Result{}, nil
	}
	if idx, err := d.scratchIndex(ctx, c.Dir); err == nil && idx != "" {
		c.Env = append(append([]string{}, c.Env...), "GIT_INDEX_FILE="+idx)
	}
	if stages {
		d.Plan.Add("git " + RedactArgs(display(c.Args)))
	}
	return d.Inner.Run(ctx, c)
}

// stagesOnly reports whether the invocation changes nothing but the index.
func stagesOnly(args []string) bool {
	rest := stripGlobal(args)
	if len(rest) == 0 {
		return false
	}
	switch rest[0] {
	case "add", "read-tree":
		return true
	}
	return false
}

// display drops the options that only shape how Git prints.
func display(args []string) []string {
	rest := stripGlobal(args)
	if len(rest) == 0 {
		return args
	}
	return rest
}

func stripGlobal(args []string) []string {
	i := 0
	for i < len(args) {
		switch a := args[i]; {
		case a == "-c" || a == "-C":
			i += 2
		case a == "--literal-pathspecs" || a == "--no-pager" || a == "--no-optional-locks":
			i++
		default:
			return args[i:]
		}
	}
	return nil
}

func has(args []string, opts ...string) bool {
	for _, a := range args {
		for _, o := range opts {
			if a == o {
				return true
			}
		}
	}
	return false
}

func positional(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

// ReadOnly reports whether the Git invocation leaves the repository, its refs
// and the remote as they are. Anything not known to be read-only counts as a
// change: a wrong answer here must lean to "skip".
func ReadOnly(args []string) bool {
	rest := stripGlobal(args)
	if len(rest) == 0 {
		return false
	}
	sub, tail := rest[0], rest[1:]
	switch sub {
	case "--version", "version", "rev-parse", "diff", "log", "show", "status", "rev-list", "merge-base", "cherry",
		"ls-files", "ls-remote", "check-ignore", "for-each-ref", "cat-file", "merge-tree", "commit-tree", "write-tree",
		"describe", "name-rev", "shortlog", "ls-tree", "show-ref", "diff-tree", "diff-index", "diff-files", "count-objects", "var":
		return true
	case "symbolic-ref":
		return has(tail, "--short", "-q") || len(positional(tail)) <= 1
	case "config":
		return has(tail, "--get", "--get-all", "--get-regexp", "--list", "-l")
	case "tag":
		return has(tail, "--list", "-l") || len(positional(tail)) == 0
	case "branch":
		return has(tail, "--list", "-l", "--remotes", "-r", "--all", "-a", "--show-current", "--contains", "--merged", "--no-merged") ||
			len(positional(tail)) == 0 && !has(tail, "--delete", "-d", "-D", "--move", "-m", "-M", "--copy", "-c", "-C")
	case "stash":
		return len(tail) > 0 && (tail[0] == "list" || tail[0] == "show")
	case "remote":
		p := positional(tail)
		return len(p) == 0 || p[0] == "get-url" || p[0] == "show"
	}
	return false
}
