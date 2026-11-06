// Package doctor implements `gtide doctor`: it diagnoses Git, repository,
// configuration, and hook setup without changing anything.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/cellestias/git-tide/internal/config"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/hooks"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/github"
)

type Level int

const (
	OK Level = iota
	Warn
	Fail
)

type Check struct {
	Level  Level
	Name   string
	Detail string
	Hint   string
}

// MinGit is the oldest supported Git version.
const MinGitMajor, MinGitMinor = 2, 30

var gitVersion = regexp.MustCompile(`git version (\d+)\.(\d+)`)

// Run executes every check and returns the results in display order.
func Run(ctx context.Context, ex executor.Executor, dir string) []Check {
	var checks []Check
	add := func(l Level, name, detail, hint string) {
		checks = append(checks, Check{Level: l, Name: name, Detail: detail, Hint: hint})
	}

	res, err := ex.Run(ctx, executor.Command{Args: []string{"--version"}, Dir: dir})
	if err != nil {
		add(Fail, "git", "cannot run git", "install Git and make sure it is on PATH")
		return checks
	}
	if major, minor, ok := parseVersion(res.Stdout); !ok {
		add(Warn, "git", strings.TrimSpace(res.Stdout), "could not parse the Git version")
	} else if major < MinGitMajor || (major == MinGitMajor && minor < MinGitMinor) {
		add(Fail, "git", strings.TrimSpace(res.Stdout), fmt.Sprintf("Git %d.%d or newer is required", MinGitMajor, MinGitMinor))
	} else {
		add(OK, "git", strings.TrimSpace(res.Stdout), "")
	}

	repo, err := repository.Open(ctx, ex, dir)
	if err != nil {
		add(Fail, "repository", err.Error(), "run gtide inside a Git repository")
		return checks
	}
	add(OK, "repository", repo.Root, "")

	cfg, err := config.Load(repo.Root)
	switch {
	case err != nil:
		add(Fail, "config", err.Error(), "run `gtide config validate` for details")
		return checks
	case !cfg.Found:
		add(Warn, "config", config.FileName+" not found; defaults apply", "run `gtide init`")
	default:
		add(OK, "config", cfg.Path, "")
	}
	c := cfg.Config

	if url, err := repo.RemoteURL(ctx, c.Remote); err != nil {
		add(Warn, "remote", fmt.Sprintf("remote %q is not configured", c.Remote), "`gtide update` and `gtide review` need it")
	} else if gh, err := github.ParseRemoteURL(executor.Redact(url)); err != nil {
		add(Warn, "remote", fmt.Sprintf("%q is not a github.com remote", c.Remote), "`gtide review` supports github.com only")
	} else {
		add(OK, "remote", fmt.Sprintf("%s → %s", c.Remote, gh), "")
	}

	base := c.Branches.DefaultBase
	local, _ := repo.LocalBranchExists(ctx, base)
	tracked, _ := repo.RemoteBranchExists(ctx, c.Remote, base)
	if local || tracked {
		add(OK, "base branch", base, "")
	} else {
		add(Warn, "base branch", fmt.Sprintf("%q not found locally or on %s", base, c.Remote), "check branches.default_base or run `git fetch`")
	}

	checks = append(checks, hookChecks(ctx, repo, c.Hooks.Manage)...)

	if _, err := exec.LookPath("gtide"); err != nil {
		add(Warn, "gtide on PATH", "not found", "hooks skip their checks when `gtide` is not on PATH")
	} else {
		add(OK, "gtide on PATH", "found", "")
	}
	return checks
}

func hookChecks(ctx context.Context, repo *repository.Repository, manage bool) []Check {
	if !manage {
		return []Check{{Level: OK, Name: "hooks", Detail: "not managed (hooks.manage: false)"}}
	}
	dir, err := repo.HooksDir(ctx)
	if err != nil {
		return []Check{{Level: Fail, Name: "hooks", Detail: err.Error()}}
	}
	statuses, err := hooks.Inspect(dir)
	if err != nil {
		return []Check{{Level: Fail, Name: "hooks", Detail: err.Error()}}
	}
	var out []Check
	for _, s := range statuses {
		name := "hook " + s.Hook
		switch {
		case s.State == hooks.Current && s.Chained:
			out = append(out, Check{Level: OK, Name: name, Detail: "installed; chains to existing hook"})
		case s.State == hooks.Current:
			out = append(out, Check{Level: OK, Name: name, Detail: "installed"})
		case s.State == hooks.Outdated:
			out = append(out, Check{Level: Warn, Name: name, Detail: "outdated", Hint: "run `gtide init` to update"})
		case s.State == hooks.Foreign:
			out = append(out, Check{Level: Warn, Name: name, Detail: "a hook exists that Tide does not manage", Hint: "run `gtide init` to chain it"})
		default:
			out = append(out, Check{Level: Warn, Name: name, Detail: "not installed", Hint: "run `gtide init`"})
		}
	}
	return out
}

func parseVersion(s string) (major, minor int, ok bool) {
	m := gitVersion.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(m[1])
	minor, err2 := strconv.Atoi(m[2])
	return major, minor, errors.Join(err1, err2) == nil
}
