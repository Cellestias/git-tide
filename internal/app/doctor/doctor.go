// Package doctor implements `gtide doctor`: it diagnoses Git, repository,
// configuration, and hook setup without changing anything.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cellestias/git-tide/internal/config"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/domain/security"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/hooks"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/provider"
	"github.com/cellestias/git-tide/internal/providers"
)

type Level int

const (
	OK Level = iota
	Warn
	Fail
)

// String is the name of the level: "ok", "warn" or "fail".
func (l Level) String() string {
	switch l {
	case Warn:
		return "warn"
	case Fail:
		return "fail"
	}
	return "ok"
}

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

	providerName := ""
	resolved, rerr := repo.RemoteURL(ctx, c.Remote)
	if rerr != nil {
		add(Warn, "remote", fmt.Sprintf("remote %q is not configured", c.Remote), "`gtide update`, `gtide pr` and `gtide review` need it; add it with `gtide remote add <url>`")
	} else {
		configured, _ := repo.RemoteConfiguredURL(ctx, c.Remote)
		name, err := providers.Resolve(c.Provider, configured, executor.Redact(resolved))
		providerName = name
		if err != nil {
			add(Warn, "provider", fmt.Sprintf("cannot detect the provider from remote %q", c.Remote), "set `provider: github` or `provider: gitlab` in .tide.yml")
		} else if p, err := providers.New(name, provider.Host(configured), ""); err != nil {
			add(Fail, "provider", err.Error(), "")
		} else if hosted, err := p.ParseRemoteURL(executor.Redact(resolved)); err != nil {
			if hosted, err = p.ParseRemoteURL(executor.Redact(configured)); err != nil {
				add(Warn, "remote", fmt.Sprintf("%q is not a %s remote", c.Remote, name), "check the remote URL or the `provider` setting")
			} else {
				add(OK, "remote", fmt.Sprintf("%s → %s (%s)", c.Remote, hosted, name), "")
			}
		} else {
			add(OK, "remote", fmt.Sprintf("%s → %s (%s)", c.Remote, hosted, name), "")
		}
	}

	checks = append(checks, securityChecks(c, providerName)...)
	checks = append(checks, secretFileChecks(ctx, repo, ex)...)
	checks = append(checks, envFileChecks(ctx, repo)...)
	if !c.Secrets.Enabled {
		add(Warn, "secret scanning", "is off (secrets.enabled: false): a commit is not checked for secrets", "set `secrets.enabled: true` in .tide.yml")
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

// secretFileChecks inspects secret.tide.yml: present, valid, and not committed.
func secretFileChecks(ctx context.Context, repo *repository.Repository, _ executor.Executor) []Check {
	local, err := config.LoadLocal(repo.Root)
	switch {
	case err != nil:
		return []Check{{Level: Fail, Name: config.SecretFileName, Detail: err.Error()}}
	case !local.Found:
		return []Check{{Level: Warn, Name: config.SecretFileName, Detail: "not found; provider commands read the token from the environment", Hint: "run `gtide init`"}}
	}
	var out []Check
	if tracked, err := repo.IsTracked(ctx, config.SecretFileName); err == nil && tracked {
		out = append(out, Check{Level: Fail, Name: config.SecretFileName, Detail: "is tracked by Git", Hint: "run `git rm --cached " + config.SecretFileName + "` and commit"})
	} else if ignored, err := repo.IsIgnored(ctx, config.SecretFileName); err == nil && !ignored {
		out = append(out, Check{Level: Warn, Name: config.SecretFileName, Detail: "is not in .gitignore", Hint: "run `gtide init` to add it"})
	} else {
		out = append(out, Check{Level: OK, Name: config.SecretFileName, Detail: "present and git-ignored"})
	}
	if v, _ := local.Local.TokenVar(); v != "" && os.Getenv(v) == "" {
		out = append(out, Check{Level: Warn, Name: "provider token", Detail: "$" + v + " is not set", Hint: "export it, or put it in .env (git-ignored)"})
	}
	return out
}

// securityChecks says what the configured security level can do with this provider.
func securityChecks(c domain.Config, providerName string) []Check {
	level := c.SecurityLevel()
	if level == security.Off {
		return nil
	}
	switch providerName {
	case provider.GitHub:
		return []Check{{Level: OK, Name: "security", Detail: fmt.Sprintf("level %s: Dependabot and code scanning alerts", level),
			Hint: "needs GITHUB_TOKEN with the security_events scope; `gtide security` shows what it can read"}}
	case "":
		return []Check{{Level: Warn, Name: "security", Detail: fmt.Sprintf("level %s, but the provider is not known", level), Hint: "set `provider: github` in .tide.yml"}}
	}
	return []Check{{Level: Warn, Name: "security", Detail: fmt.Sprintf("level %s, but %s has no security alerts API Tide can read: nothing is checked", level, providerName)}}
}

// envFileChecks looks at the .env file at the root: the token Tide may read from
// it is a secret, and a .env that Git does not ignore is one `git add` away from
// the history.
func envFileChecks(ctx context.Context, repo *repository.Repository) []Check {
	info, err := os.Lstat(filepath.Join(repo.Root, ".env"))
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	if tracked, err := repo.IsTracked(ctx, ".env"); err == nil && tracked {
		return []Check{{Level: Fail, Name: ".env", Detail: "is tracked by Git: anything secret in it is exposed",
			Hint: "revoke the tokens in it, run `git rm --cached .env`, add .env to .gitignore and commit"}}
	}
	if ignored, err := repo.IsIgnored(ctx, ".env"); err == nil && !ignored {
		return []Check{{Level: Warn, Name: ".env", Detail: "is not in .gitignore: `gtide save` or `git add -A` would commit it",
			Hint: "add .env to .gitignore"}}
	}
	return []Check{{Level: OK, Name: ".env", Detail: "present and git-ignored"}}
}
