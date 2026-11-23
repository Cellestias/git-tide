package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/dryrun"
)

type recorder struct {
	ran  [][]string
	envs [][]string
}

func (r *recorder) Run(_ context.Context, c Command) (Result, error) {
	r.ran = append(r.ran, c.Args)
	r.envs = append(r.envs, c.Env)
	return Result{Stdout: "real"}, nil
}

func TestReadOnly(t *testing.T) {
	read := [][]string{
		{"rev-parse", "--verify", "HEAD"}, {"status", "--porcelain=v1"}, {"-c", "core.quotepath=false", "diff", "--no-color"},
		{"--literal-pathspecs", "ls-files"}, {"tag", "--list"}, {"branch", "--list", "feature/*"}, {"branch", "--remotes"},
		{"stash", "list"}, {"remote"}, {"remote", "get-url", "origin"}, {"config", "--get", "user.email"},
		{"symbolic-ref", "--short", "HEAD"}, {"merge-tree", "--write-tree", "a", "b"}, {"log", "-1"}, {"--version"},
	}
	for _, a := range read {
		if !ReadOnly(a) {
			t.Errorf("%v should be read-only", a)
		}
	}
	write := [][]string{
		{"commit", "-m", "x"}, {"push", "--", "origin"}, {"fetch", "--no-tags"}, {"checkout", "--quiet", "x"}, {"branch", "--delete", "x"},
		{"branch", "new"}, {"branch", "--move", "a", "b"}, {"tag", "--annotate", "v1"}, {"tag", "--delete", "v1"}, {"stash", "pop"},
		{"stash"}, {"remote", "add", "o", "u"}, {"remote", "set-url", "o", "u"}, {"config", "user.name", "x"}, {"add", "--all"},
		{"reset", "--soft", "HEAD~1"}, {"merge", "--ff-only"}, {"rebase", "x"}, {"read-tree", "x"}, {"init"}, {"clone", "u"}, {},
		{"-c", "core.editor=true", "commit", "--amend"}, {"symbolic-ref", "HEAD", "refs/heads/x"},
	}
	for _, a := range write {
		if ReadOnly(a) {
			t.Errorf("%v must count as a change", a)
		}
	}
}

func TestDryRunRunsReadsAndRecordsChanges(t *testing.T) {
	inner := &recorder{}
	plan := &dryrun.Plan{}
	d := NewDryRun(inner, plan)

	res, err := d.Run(context.Background(), Command{Args: []string{"status", "--porcelain=v1"}})
	if err != nil || res.Stdout != "real" {
		t.Fatalf("a read must reach git: %+v, %v", res, err)
	}
	res, err = d.Run(context.Background(), Command{Args: []string{"-c", "user.name=gtide", "commit", "-m", "feat: x"}})
	if err != nil || res.Stdout != "" {
		t.Fatalf("a change must answer with success and no output: %+v, %v", res, err)
	}
	for _, args := range inner.ran {
		if args[0] == "commit" {
			t.Fatalf("git ran %v; a commit must be skipped", inner.ran)
		}
	}
	steps := plan.Steps()
	if len(steps) != 1 || !strings.HasPrefix(steps[0], "git commit -m") || strings.Contains(steps[0], "user.name") {
		t.Errorf("steps = %v", steps)
	}
}

func TestDryRunHidesCredentials(t *testing.T) {
	plan := &dryrun.Plan{}
	d := NewDryRun(&recorder{}, plan)
	_, _ = d.Run(context.Background(), Command{Args: []string{"push", "--", "https://user:tok@github.com/a/b.git"}})
	if s := plan.Steps()[0]; strings.Contains(s, "tok") {
		t.Errorf("a credential reached the plan: %s", s)
	}
}

func TestDryRunStagesOnACopyOfTheIndex(t *testing.T) {
	inner := &recorder{}
	plan := &dryrun.Plan{}
	d := NewDryRun(inner, plan)
	defer d.Close()

	if _, err := d.Run(context.Background(), Command{Args: []string{"add", "--all"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Run(context.Background(), Command{Args: []string{"diff", "--cached"}}); err != nil {
		t.Fatal(err)
	}
	var addEnv, diffEnv string
	for i, args := range inner.ran {
		for _, e := range inner.envs[i] {
			if strings.HasPrefix(e, "GIT_INDEX_FILE=") {
				if args[0] == "add" {
					addEnv = e
				} else if args[0] == "diff" {
					diffEnv = e
				}
			}
		}
	}
	if addEnv == "" || addEnv != diffEnv {
		t.Errorf("staging and the read after it must share a copy of the index: add=%q diff=%q", addEnv, diffEnv)
	}
	if steps := plan.Steps(); len(steps) != 1 || steps[0] != "git add --all" {
		t.Errorf("steps = %v", steps)
	}
}
