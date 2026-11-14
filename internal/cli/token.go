package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/config"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/dotenv"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/gitlab"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
	"github.com/cellestias/git-tide/internal/providers"
)

// validToken keeps anything that is not a plausible token out of an HTTP header.
var validToken = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,255}$`)

// provider builds the hosting adapter for the repository: the one named in
// .tide.yml, or the one detected from the remote URL when it says "auto". The
// token comes from secret.tide.yml (`provider_token: ${VAR}`, resolved from the
// environment, then .env); failing that, from the provider's usual variable
// (GITHUB_TOKEN/GH_TOKEN, GITLAB_TOKEN) and .env, so each project can use its
// own organisation's token.
func (e *env) provider(ctx context.Context, repo *repository.Repository, cfg domain.Config) (provider.Provider, error) {
	resolved, configured, err := provider.RemoteURLs(ctx, repo, cfg.Remote)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeConfig, "remote "+cfg.Remote+" is not configured",
			"add it with `gtide remote add <url>` or change `remote` in .tide.yml", err)
	}
	name, err := providers.Resolve(cfg.Provider, configured, resolved)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeConfig, err.Error(),
			"set `provider: github` or `provider: gitlab` in .tide.yml", err)
	}
	host := gitlab.HostOf(configured)
	if host == "" {
		host = gitlab.HostOf(resolved)
	}

	token := e.localToken(ctx, repo)
	p, err := providers.New(name, host, token)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeConfig, "invalid "+name+" client settings", "", err)
	}
	if !p.HasToken() {
		if t := dotenvToken(ctx, repo, e.p, providers.TokenKeys(name)...); t != "" {
			return providers.New(name, host, t)
		}
	}
	return p, nil
}

// localToken resolves provider_token from secret.tide.yml: the referenced
// environment variable first, then the same key in .env. It returns "" when
// there is no file, no reference, or no value. Any problem becomes a warning
// and never includes the token.
func (e *env) localToken(ctx context.Context, repo *repository.Repository) string {
	local, err := config.LoadLocal(repo.Root)
	if err != nil {
		e.p.Warn("ignoring %s: %v", config.SecretFileName, err)
		return ""
	}
	name, _ := local.Local.TokenVar()
	if !local.Found || name == "" {
		return ""
	}
	if tracked, err := repo.IsTracked(ctx, config.SecretFileName); err == nil && tracked {
		e.p.Warn("%s is tracked by Git: add it to .gitignore and run `git rm --cached %s`", config.SecretFileName, config.SecretFileName)
	}
	if v := os.Getenv(name); v != "" {
		if !validToken.MatchString(v) {
			e.p.Warn("ignoring $%s: it contains characters a provider token never has", name)
			return ""
		}
		return v
	}
	return dotenvToken(ctx, repo, e.p, name)
}

// dotenvToken returns the first of keys found in <root>/.env, or "". A .env that
// Git tracks is never used: its token is already exposed. One that .gitignore
// does not cover is used with a warning. Warnings never include the token.
func dotenvToken(ctx context.Context, repo *repository.Repository, p output.Printer, keys ...string) string {
	values, err := dotenv.Read(filepath.Join(repo.Root, ".env"), keys...)
	if err != nil {
		p.Warn("ignoring .env: %v", err)
		return ""
	}
	token := ""
	for _, k := range keys {
		if values[k] != "" {
			token = values[k]
			break
		}
	}
	if token == "" {
		return ""
	}
	if !validToken.MatchString(token) {
		p.Warn("ignoring the token in .env: it contains characters a provider token never has")
		return ""
	}
	if tracked, err := repo.IsTracked(ctx, ".env"); err != nil || tracked {
		p.Warn(".env is tracked by Git, so its token is exposed: not using it. Revoke the token and run `git rm --cached .env`")
		return ""
	}
	if ignored, err := repo.IsIgnored(ctx, ".env"); err == nil && !ignored {
		p.Warn(".env is not in .gitignore; add it so the token cannot be committed by accident")
	}
	return token
}
