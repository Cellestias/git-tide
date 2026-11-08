// Package initrepo implements `gtide init`.
package initrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/config"
	"github.com/cellestias/git-tide/internal/domain/branch"
	"github.com/cellestias/git-tide/internal/git/hooks"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/provider"
)

type Options struct {
	// Base overrides the detected default base branch.
	Base string
	// NoHooks skips hook installation.
	NoHooks bool
	// Provider is "auto" (default), "github" or "gitlab".
	Provider string
}

type Result struct {
	ConfigPath    string
	ConfigCreated bool
	Provider      string
	Base          string
	// SecretPath is secret.tide.yml; SecretCreated is false when it already existed.
	SecretPath    string
	SecretCreated bool
	// IgnoreCreated / IgnoreUpdated report what happened to .gitignore; both false means
	// secret.tide.yml was already ignored.
	IgnoreCreated bool
	IgnoreUpdated bool
	// Notes are things the user should know, such as a flag that was not applied.
	Notes        []string
	HooksDir     string
	HookChanges  []hooks.Change
	HooksSkipped bool
}

const configTemplate = `# Git Tide configuration. Docs: README.md
version: 1

# Hosting provider: auto (detected from the remote URL), github or gitlab.
# Never put tokens in this file: they belong in secret.tide.yml.
provider: %[2]s

# Remote used by "gtide update" and "gtide review".
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

// secretTemplate is secret.tide.yml. The token is a reference, never a value.
// Detectors from the catalog are written commented out: enable one by deleting
// the leading "# " of its two lines.
const secretTemplate = `# Git Tide local settings. Never commit this file: "gtide init" adds it to .gitignore.
# provider_token must reference an environment variable (or a key in .env); never paste the token itself.
provider_token: ${%[1]s}

secrets:
  # Extra secret-scanning detectors, applied on top of the built-in ones (AWS access key IDs, GitHub tokens,
  # Slack tokens, Stripe live keys, Google API keys, private key headers).
  # Everything below is disabled. To enable a detector, delete the "# " at the start of its two lines.
  # Patterns are Go (RE2) regular expressions. For a false positive on one line, add tide:allow-secret to it.
  patterns:
%[2]s`

// Run writes .tide.yml and secret.tide.yml (never overwriting an existing one),
// makes sure secret.tide.yml is git-ignored and, when enabled, installs hooks.
func Run(ctx context.Context, repo *repository.Repository, opts Options) (Result, error) {
	res := Result{
		ConfigPath: filepath.Join(repo.Root, config.FileName),
		SecretPath: filepath.Join(repo.Root, config.SecretFileName),
	}

	chosen := opts.Provider
	switch chosen {
	case "":
		chosen = provider.Auto
	case provider.Auto, provider.GitHub, provider.GitLab:
	default:
		return res, apperr.Usage("invalid --provider %q: use %s, %s or %s", opts.Provider, provider.Auto, provider.GitHub, provider.GitLab)
	}

	loaded, err := config.Load(repo.Root)
	if err != nil {
		return res, err
	}

	if loaded.Found {
		res.Base = loaded.Config.Branches.DefaultBase
		res.Provider = loaded.Config.Provider
		if opts.Provider != "" && opts.Provider != res.Provider {
			res.Notes = append(res.Notes, fmt.Sprintf("--provider %s ignored: %s already sets provider: %s (edit it to change)", opts.Provider, config.FileName, res.Provider))
		}
	} else {
		base, err := chooseBase(ctx, repo, opts.Base)
		if err != nil {
			return res, err
		}
		res.Base, res.Provider = base, chosen
		//nolint:gosec // .tide.yml is a committed project file and must be readable by the team
		if err := createExclusive(res.ConfigPath, fmt.Sprintf(configTemplate, base, chosen), 0o644); err != nil {
			return res, err
		}
		res.ConfigCreated = true
	}

	created, err := ensureSecretFile(ctx, repo, res.SecretPath, res.Provider)
	if err != nil {
		return res, err
	}
	res.SecretCreated = created
	res.IgnoreCreated, res.IgnoreUpdated, err = ensureIgnored(filepath.Join(repo.Root, ".gitignore"), config.SecretFileName)
	if err != nil {
		return res, err
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

// createExclusive writes a new file and fails if it already exists (O_EXCL), so a
// file created between the existence check and here is never clobbered.
func createExclusive(path, content string, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) //nolint:gosec // callers pass fixed paths under the repository root
	if err != nil {
		return apperr.Wrap(apperr.CodeGeneral, "cannot create "+path, "", err)
	}
	_, werr := f.WriteString(content)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return apperr.Wrap(apperr.CodeGeneral, "cannot write "+path, "", werr)
	}
	return nil
}

// ensureSecretFile creates secret.tide.yml when missing and reports whether it did.
// The file is owner-only (0600) because it points at credentials.
func ensureSecretFile(ctx context.Context, repo *repository.Repository, path, providerName string) (bool, error) {
	if _, err := os.Lstat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, apperr.Wrap(apperr.CodeGeneral, "cannot inspect "+path, "", err)
	}
	if err := createExclusive(path, fmt.Sprintf(secretTemplate, tokenVar(ctx, repo, providerName), renderCatalog()), 0o600); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// tokenVar picks the environment variable the new secret.tide.yml references:
// from the provider, or for "auto" from the origin remote, defaulting to GITHUB_TOKEN.
func tokenVar(ctx context.Context, repo *repository.Repository, providerName string) string {
	name := providerName
	if name == provider.Auto {
		name = ""
		for _, get := range []func(context.Context, string) (string, error){repo.RemoteConfiguredURL, repo.RemoteURL} {
			if u, err := get(ctx, "origin"); err == nil {
				if name = provider.DetectName(u); name != "" {
					break
				}
			}
		}
	}
	if name == provider.GitLab {
		return "GITLAB_TOKEN"
	}
	return "GITHUB_TOKEN"
}

// ensureIgnored makes sure entry is a line of the .gitignore at path. It creates
// the file when missing, appends the entry otherwise, keeps every existing line,
// and never duplicates the rule (a leading "/" form counts as present).
func ensureIgnored(path, entry string) (created, updated bool, err error) {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := createExclusive(path, entry+"\n", 0o644); err != nil { //nolint:gosec // .gitignore is a committed project file
			return false, false, err
		}
		return true, false, nil
	case err != nil:
		return false, false, apperr.Wrap(apperr.CodeGeneral, "cannot inspect "+path, "", err)
	case !fi.Mode().IsRegular():
		return false, false, apperr.New(apperr.CodeGeneral, path+" is not a regular file", "add "+entry+" to your ignore rules by hand")
	}

	data, err := os.ReadFile(path) //nolint:gosec // fixed path under the repository root
	if err != nil {
		return false, false, apperr.Wrap(apperr.CodeGeneral, "cannot read "+path, "", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if l := strings.TrimSpace(line); l == entry || l == "/"+entry {
			return false, false, nil
		}
	}
	add := entry + "\n"
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		add = "\n" + add
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0) //nolint:gosec // fixed path under the repository root
	if err != nil {
		return false, false, apperr.Wrap(apperr.CodeGeneral, "cannot update "+path, "", err)
	}
	_, werr := f.WriteString(add)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return false, false, apperr.Wrap(apperr.CodeGeneral, "cannot update "+path, "", werr)
	}
	return false, true, nil
}
