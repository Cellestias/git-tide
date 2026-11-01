package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cellestias/git-tide/internal/app/doctor"
	apphooks "github.com/cellestias/git-tide/internal/app/hooks"
	"github.com/cellestias/git-tide/internal/app/initrepo"
	"github.com/cellestias/git-tide/internal/app/review"
	"github.com/cellestias/git-tide/internal/app/update"
	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/config"
	"github.com/cellestias/git-tide/internal/github"
)

func (e *env) initCmd() *cobra.Command {
	var opts initrepo.Options
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create " + config.FileName + " and install Git hooks",
		Long: "Create " + config.FileName + " (never overwriting an existing one) and install Tide's Git hooks.\n" +
			"An existing hook is preserved as <hook>.pre-tide and still runs after Tide's checks.",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := e.openRepo(cmd.Context())
			if err != nil {
				return err
			}
			res, err := initrepo.Run(cmd.Context(), repo, opts)
			for _, c := range res.HookChanges {
				e.p.Success("hook %s", c.Describe())
			}
			if err != nil {
				return err
			}
			if res.ConfigCreated {
				e.p.Success("created %s (base branch: %s)", res.ConfigPath, res.Base)
			} else {
				e.p.Info("%s already exists; left unchanged", res.ConfigPath)
			}
			if res.HooksSkipped {
				e.p.Info("hooks not installed")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Base, "base", "", "default base branch (default: detected from the repository)")
	cmd.Flags().BoolVar(&opts.NoHooks, "no-hooks", false, "do not install Git hooks")
	return cmd
}

func (e *env) updateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update [branch]",
		Short: "Update the base branch and integrate it into the current branch",
		Long: "Fetch the base branch, fast-forward the local copy, and merge or rebase it into the current branch\n" +
			"(per update.strategy). The base comes from .tide.yml unless [branch] is given.\n" +
			"Stops without changes if uncommitted work or an unfinished merge/rebase is present.",
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			var opts update.Options
			if len(args) == 1 {
				opts.Target = args[0]
			}
			return update.Run(cmd.Context(), repo, cfg, opts, e.p)
		},
	}
}

func (e *env) reviewCmd() *cobra.Command {
	var opts review.Options
	cmd := &cobra.Command{
		Use:   "review <PR>",
		Short: "Check out a Pull Request on a local review branch",
		Long: "Fetch a GitHub Pull Request and check it out as " + "tide/review/pr-<number>.\n" +
			"<PR> is a number, #number, or a pull request URL. Set GITHUB_TOKEN for private repositories.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			client, err := github.NewClient()
			if err != nil {
				return apperr.Wrap(apperr.CodeConfig, "invalid GitHub client settings", "", err)
			}
			opts.Ref = args[0]
			return review.Run(cmd.Context(), repo, cfg, client, opts, e.p)
		},
	}
	cmd.Flags().BoolVar(&opts.Reset, "reset", false, "discard local commits on an existing review branch")
	return cmd
}

func (e *env) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose repository, configuration, Git, and hook setup",
		Args:  maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			failed := false
			for _, c := range doctor.Run(cmd.Context(), e.ex, e.dir) {
				switch c.Level {
				case doctor.OK:
					e.p.Success("%s: %s", c.Name, c.Detail)
				case doctor.Warn:
					e.p.Line("! %s: %s", c.Name, c.Detail)
				case doctor.Fail:
					failed = true
					e.p.Line("✗ %s: %s", c.Name, c.Detail)
				}
				if c.Hint != "" && c.Level != doctor.OK {
					e.p.Line("    hint: %s", c.Hint)
				}
			}
			if failed {
				return apperr.New(apperr.CodeGeneral, "doctor found problems", "")
			}
			return nil
		},
	}
}

func (e *env) configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect or validate Tide configuration",
		Args:  maxArgs(0),
		RunE:  func(cmd *cobra.Command, _ []string) error { return e.configShow(cmd) },
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration (file merged over defaults)",
		Args:  maxArgs(0),
		RunE:  func(cmd *cobra.Command, _ []string) error { return e.configShow(cmd) },
	}, &cobra.Command{
		Use:   "validate",
		Short: "Validate " + config.FileName,
		Args:  maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := e.openRepo(cmd.Context())
			if err != nil {
				return err
			}
			res, err := config.Load(repo.Root)
			if err != nil {
				return err
			}
			if !res.Found {
				return apperr.New(apperr.CodeConfig, config.FileName+" not found", "run `tide init` first")
			}
			e.p.Success("%s is valid", res.Path)
			return nil
		},
	})
	return cmd
}

func (e *env) configShow(cmd *cobra.Command) error {
	repo, err := e.openRepo(cmd.Context())
	if err != nil {
		return err
	}
	res, err := config.Load(repo.Root)
	if err != nil {
		return err
	}
	out, err := config.Marshal(res.Config)
	if err != nil {
		return err
	}
	src := res.Path
	if !res.Found {
		src = "defaults (" + config.FileName + " not found)"
	}
	e.p.Line("# source: %s", src)
	fmt.Fprint(e.p.Out, string(out))
	return nil
}

// hookCmd is invoked by the scripts that `tide init` installs.
func (e *env) hookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "hook",
		Short:  "Run a Git hook check (used by installed hooks)",
		Hidden: true,
		Args:   maxArgs(0),
	}
	// hookRun loads the config and skips silently when the repository has none:
	// an unconfigured repository has nothing to enforce.
	hookRun := func(fn func(cmd *cobra.Command, args []string, repo hookCtx) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			repo, err := e.openRepo(cmd.Context())
			if err != nil {
				return err
			}
			res, err := config.Load(repo.Root)
			if err != nil {
				return err
			}
			if !res.Found {
				return nil
			}
			return fn(cmd, args, hookCtx{repo: repo, cfg: res.Config})
		}
	}
	cmd.AddCommand(&cobra.Command{
		Use:  "pre-commit",
		Args: maxArgs(0),
		RunE: hookRun(func(cmd *cobra.Command, _ []string, h hookCtx) error {
			return apphooks.PreCommit(cmd.Context(), h.repo, h.cfg)
		}),
	}, &cobra.Command{
		Use:  "commit-msg <file>",
		Args: exactArgs(1),
		RunE: hookRun(func(_ *cobra.Command, args []string, h hookCtx) error {
			return apphooks.CommitMsg(h.cfg, args[0])
		}),
	}, &cobra.Command{
		Use:  "pre-push [remote] [url]",
		Args: maxArgs(2),
		RunE: hookRun(func(_ *cobra.Command, _ []string, h hookCtx) error {
			return apphooks.PrePush(h.cfg, e.in)
		}),
	})
	return cmd
}
