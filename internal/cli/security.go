package cli

import (
	"context"

	"github.com/spf13/cobra"

	appcommit "github.com/cellestias/git-tide/internal/app/commit"
	appsecurity "github.com/cellestias/git-tide/internal/app/security"
	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	sec "github.com/cellestias/git-tide/internal/domain/security"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/provider"
)

// securityGate builds the check for the repository's security level. The
// provider and the hosted repository are only worked out when a stage asks.
func (e *env) securityGate(repo *repository.Repository, cfg domain.Config) appsecurity.Gate {
	return appsecurity.NewGate(cfg.SecurityLevel(), e.securityTarget(repo, cfg), e.p)
}

// securityCheck is the gate as push and pr use it: always at the push stage.
func (e *env) securityCheck(repo *repository.Repository, cfg domain.Config) func(context.Context, []string) error {
	if cfg.SecurityLevel() == sec.Off {
		return nil
	}
	gate := e.securityGate(repo, cfg)
	return func(ctx context.Context, touched []string) error { return gate(ctx, sec.StagePush, touched) }
}

// securityTarget finds the provider (with its token) and the repository on it.
func (e *env) securityTarget(repo *repository.Repository, cfg domain.Config) appsecurity.Resolver {
	return func(ctx context.Context) (provider.Provider, provider.Repo, error) {
		p, err := e.provider(ctx, repo, cfg)
		if err != nil {
			return nil, provider.Repo{}, err
		}
		hosted, err := provider.RepoFromRemote(ctx, repo, p, cfg.Remote)
		if err != nil {
			return nil, provider.Repo{}, err
		}
		return p, hosted, nil
	}
}

// commitThenAlerts makes the commit, then, at levels medium and high, lists the
// open security alerts. The alerts never undo or fail a commit that was made.
func (e *env) commitThenAlerts(ctx context.Context, repo *repository.Repository, cfg domain.Config, opts appcommit.Options) error {
	if err := appcommit.Run(ctx, repo, cfg, opts, e.p); err != nil {
		return err
	}
	if cfg.SecurityLevel() != sec.Off {
		_ = e.securityGate(repo, cfg)(ctx, sec.StageCommit, nil)
	}
	return nil
}

func (e *env) securityCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "security",
		Short: "List the open security alerts of the repository",
		Long: "List the open Dependabot (vulnerable dependencies) and code scanning alerts of the repository on GitHub, most severe first,\n" +
			"with the file each one is about. It works whatever `security.level` is: the level decides when Tide warns and what it blocks,\n" +
			"this command only looks. It needs a token that can read the alerts (GITHUB_TOKEN with the security_events scope, or access to\n" +
			"the repository), and it fails when none of the sources could be read.\n\n" +
			"security.level in .tide.yml:\n" +
			"  off     look nothing up (the default)\n" +
			"  low     warn at push, never block\n" +
			"  medium  warn at commit and at push; block a push while a critical alert is open\n" +
			"  high    warn at commit; block a push while any alert is open, or cannot be checked\n" +
			"A push that changes the file an alert is about (such as package.json) is always allowed, so the fix can be sent.",
		Example: "  gtide security\n  gtide security --json",
		Args:    maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			p, hosted, err := e.securityTarget(repo, cfg)(cmd.Context())
			if err != nil {
				return apperr.Wrap(apperr.CodeConfig, "cannot identify the hosted repository", "check the remote and the `provider` setting in .tide.yml", err)
			}
			return appsecurity.List(cmd.Context(), p, hosted, cfg.SecurityLevel(), asJSON, e.p)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON on stdout")
	return cmd
}
