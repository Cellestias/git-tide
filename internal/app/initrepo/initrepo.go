// Package initrepo implements `tide init`.
package initrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/config"
	"github.com/cellestias/git-tide/internal/domain/branch"
	"github.com/cellestias/git-tide/internal/git/hooks"
	"github.com/cellestias/git-tide/internal/git/repository"
)

type Options struct {
	// Base overrides the detected default base branch.
	Base string
	// NoHooks skips hook installation.
	NoHooks bool
}

type Result struct {
	ConfigPath    string
	ConfigCreated bool
	Base          string
	HooksDir      string
	HookChanges   []hooks.Change
	HooksSkipped  bool
}

const configTemplate = `# Git Tide configuration. Docs: README.md
version: 1

# Remote used by "tide update" and "tide review".
remote: origin

branches:
  # Base branch for working branches that match no pattern in "base".
  default_base: %[1]s
  # Local hooks refuse direct commits and pushes on these branches.
  protected:
    - %[1]s
  # Optional: pattern -> base branch. "*" matches within one path segment,
  # "**" across segments; the most specific pattern wins.
  # base:
  #   feature/*: %[1]s
  #   release/*: main

update:
  strategy: merge # or: rebase

commits:
  convention: conventional
  require_scope: false

secrets:
  enabled: true
  # custom_patterns:
  #   - name: internal_token
  #     pattern: "corp_[A-Za-z0-9]{32}"

hooks:
  manage: true
`

// Run writes the configuration (never overwriting an existing one) and, when
// enabled, installs hooks.
func Run(ctx context.Context, repo *repository.Repository, opts Options) (Result, error) {
	res := Result{ConfigPath: filepath.Join(repo.Root, config.FileName)}

	loaded, err := config.Load(repo.Root)
	if err != nil {
		return res, err
	}

	if loaded.Found {
		res.Base = loaded.Config.Branches.DefaultBase
	} else {
		base, err := chooseBase(ctx, repo, opts.Base)
		if err != nil {
			return res, err
		}
		res.Base = base
		content := fmt.Sprintf(configTemplate, base)
		// O_EXCL: never clobber a file created between Load and here.
		f, err := os.OpenFile(res.ConfigPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // .tide.yml is a committed project file and must be readable by the team
		if err != nil {
			return res, apperr.Wrap(apperr.CodeGeneral, "cannot create "+res.ConfigPath, "", err)
		}
		_, werr := f.WriteString(content)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return res, apperr.Wrap(apperr.CodeGeneral, "cannot write "+res.ConfigPath, "", werr)
		}
		res.ConfigCreated = true
	}

	if opts.NoHooks || !loaded.Config.Hooks.Manage {
		res.HooksSkipped = true
		return res, nil
	}
	dir, err := repo.HooksDir(ctx)
	if err != nil {
		return res, apperr.Wrap(apperr.CodeExternal, "cannot locate hooks directory", "", err)
	}
	res.HooksDir = dir
	changes, err := hooks.Install(dir)
	res.HookChanges = changes
	if err != nil {
		return res, apperr.Wrap(apperr.CodeGeneral, "hook installation failed", "existing hooks were left in place", err)
	}
	return res, nil
}

func chooseBase(ctx context.Context, repo *repository.Repository, explicit string) (string, error) {
	if explicit != "" {
		if err := branch.ValidName(explicit); err != nil {
			return "", apperr.Usage("invalid --base: %v", err)
		}
		return explicit, nil
	}
	if b := repo.RemoteDefaultBranch(ctx, "origin"); b != "" && branch.ValidName(b) == nil {
		return b, nil
	}
	for _, candidate := range []string{"main", "master"} {
		ok, err := repo.LocalBranchExists(ctx, candidate)
		if err != nil && !errors.Is(err, context.Canceled) {
			continue
		}
		if ok {
			return candidate, nil
		}
	}
	return "main", nil
}
