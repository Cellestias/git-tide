package cli

import (
	"context"
	"path/filepath"
	"regexp"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/dotenv"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/github"
	"github.com/cellestias/git-tide/internal/output"
)

// tokenKeys are the only keys read, in priority order.
var tokenKeys = []string{"GITHUB_TOKEN", "GH_TOKEN"}

// validToken keeps anything that is not a plausible token out of an HTTP header.
var validToken = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,255}$`)

// githubClient builds the GitHub client. The token comes from the environment
// (GITHUB_TOKEN, then GH_TOKEN) and, only when neither is set, from the .env
// file at the repository root, so each project can use its own organisation's token.
func (e *env) githubClient(ctx context.Context, repo *repository.Repository) (*github.Client, error) {
	client, err := github.NewClient()
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeConfig, "invalid GitHub client settings", "", err)
	}
	if client.Token == "" {
		client.Token = dotenvToken(ctx, repo, e.p)
	}
	return client, nil
}

// dotenvToken returns the token in <root>/.env, or "". A .env that Git tracks
// is never used: its token is already exposed. One that .gitignore does not
// cover is used with a warning. Warnings never include the token.
func dotenvToken(ctx context.Context, repo *repository.Repository, p output.Printer) string {
	values, err := dotenv.Read(filepath.Join(repo.Root, ".env"), tokenKeys...)
	if err != nil {
		p.Warn("ignoring .env: %v", err)
		return ""
	}
	token := ""
	for _, k := range tokenKeys {
		if values[k] != "" {
			token = values[k]
			break
		}
	}
	if token == "" {
		return ""
	}
	if !validToken.MatchString(token) {
		p.Warn("ignoring the token in .env: it contains characters a GitHub token never has")
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
