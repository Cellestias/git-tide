package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cellestias/git-tide/internal/app/branches"
	"github.com/cellestias/git-tide/internal/app/change"
	"github.com/cellestias/git-tide/internal/app/create"
	"github.com/cellestias/git-tide/internal/app/doctor"
	apphooks "github.com/cellestias/git-tide/internal/app/hooks"
	"github.com/cellestias/git-tide/internal/app/initrepo"
	"github.com/cellestias/git-tide/internal/app/pr"
	"github.com/cellestias/git-tide/internal/app/push"
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

func (e *env) changeCmd() *cobra.Command {
	var opts change.Options
	cmd := &cobra.Command{
		Use:     "change <branch>",
		Aliases: []string{"switch"},
		Short:   "Switch to another branch safely",
		Long: "Switch to a branch. <branch> may be a partial name when it is unambiguous (`tide change two-fa`),\n" +
			"or - for the previous branch. A branch that only exists on the remote is created locally with its upstream.\n" +
			"Refuses with uncommitted tracked changes unless --carry is given, and while a merge or rebase is in progress.\n" +
			"No network call is made: remote branches are the ones already fetched (see `tide branch --remote`).",
		Example: "  tide change feature/two-fa\n" +
			"  tide change two-fa     # unique partial match\n" +
			"  tide change -          # previous branch\n" +
			"  tide change main --carry",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			return change.Run(cmd.Context(), repo, cfg, args[0], opts, e.p)
		},
	}
	cmd.Flags().BoolVar(&opts.Carry, "carry", false, "let Git carry uncommitted changes to the other branch (it refuses on conflicts)")
	return cmd
}

func (e *env) branchCmd() *cobra.Command {
	var (
		opts   branches.Options
		stale  int
		sortBy string
	)
	cmd := &cobra.Command{
		Use:     "branch",
		Aliases: []string{"branches"},
		Short:   "List branches with their base, upstream, status and last activity",
		Long: "List local branches with the last update, last commit author, ahead/behind against the upstream and against\n" +
			"the base branch from .tide.yml, and whether they are protected or already merged.\n" +
			"Local data only by default; --remote and --github add network calls.\n" +
			"The author is the author of the last commit: Git does not record who created a branch.",
		Example: "  tide branch\n" +
			"  tide branch --remote --github     # include remote-only branches, GitHub protection and open PRs\n" +
			"  tide branch --mine --sort name\n" +
			"  tide branch --merged              # candidates for cleanup\n" +
			"  tide branch --stale 30",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			opts.StaleDays, opts.Sort = stale, sortBy
			var gh branches.GitHub
			if opts.GitHub {
				client, err := github.NewClient()
				if err != nil {
					return apperr.Wrap(apperr.CodeConfig, "invalid GitHub client settings", "", err)
				}
				gh = client
			}
			return branches.Run(cmd.Context(), repo, cfg, gh, opts, e.p)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.Remote, "remote", false, "also list branches that only exist on the remote (fetches first)")
	f.BoolVar(&opts.GitHub, "github", false, "add a GitHub column: branch protection and open pull request (GITHUB_TOKEN for private repos)")
	f.BoolVar(&opts.Mine, "mine", false, "only branches whose last author is your git user.email")
	f.IntVar(&stale, "stale", 0, "only branches with no commits for more than `days`")
	f.BoolVar(&opts.Merged, "merged", false, "only branches with nothing the base branch lacks")
	f.StringVar(&sortBy, "sort", branches.SortDate, "sort by date (newest first), name, or ahead (most commits ahead of base)")
	return cmd
}

func (e *env) createCmd() *cobra.Command {
	var opts create.Options
	cmd := &cobra.Command{
		Use:   "create <branch>",
		Short: "Create a branch from an up-to-date base",
		Long: "Create <branch> from the freshly fetched base branch (resolved from .tide.yml, or --from) and switch to it.\n" +
			"The new branch has no upstream unless you use --track or --push.",
		Example: "  tide create feature/two-fa                  # from the configured base, fetched first\n" +
			"  tide create feature/two-fa --from release/1  # from another branch, tag, or commit\n" +
			"  tide create feature/two-fa --local           # no network: use the local base\n" +
			"  tide create feature/two-fa --track           # check out a branch that exists on the remote\n" +
			"  tide create feature/two-fa --push            # also push and set the upstream",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			return create.Run(cmd.Context(), repo, cfg, args[0], opts, e.p)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.From, "from", "", "start point: a branch, tag, or commit (default: base from .tide.yml)")
	f.BoolVar(&opts.Local, "local", false, "start from the local branch; no network")
	f.BoolVar(&opts.Track, "track", false, "create from the remote branch of the same name and track it")
	f.BoolVar(&opts.Push, "push", false, "push the new branch and set its upstream")
	f.BoolVar(&opts.NoSwitch, "no-switch", false, "create the branch without switching to it")
	return cmd
}

func (e *env) pushCmd() *cobra.Command {
	var (
		opts push.Options
		yes  bool
	)
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push the current branch and set its upstream",
		Long: "Push the current branch to the configured remote under the same name and set it as upstream.\n" +
			"Protected branches are refused. --force uses --force-with-lease, so work you have not fetched is never overwritten.",
		Example: "  tide push           # publish the current branch\n" +
			"  tide push --force   # after rewriting history (rebase, amend); asks first",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			if opts.Force && !yes {
				opts.Confirm = e.confirm
			}
			return push.Run(cmd.Context(), repo, cfg, opts, e.p)
		},
	}
	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "overwrite the remote branch if it was rewritten (--force-with-lease)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation of --force")
	return cmd
}

func (e *env) prCmd() *cobra.Command {
	var (
		opts pr.Options
		yes  bool
	)
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Push the current branch and open a GitHub Pull Request",
		Long: "Push the current branch and open a Pull Request against its base branch (from .tide.yml, or --base).\n" +
			"The title defaults to the commit subject (one commit) or the branch name; the body lists the commits.\n" +
			"Requires GITHUB_TOKEN with pull-request write access. Asks for confirmation unless --yes is given.",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			client, err := github.NewClient()
			if err != nil {
				return apperr.Wrap(apperr.CodeConfig, "invalid GitHub client settings", "", err)
			}
			if client.Token == "" {
				return apperr.New(apperr.CodeConfig, "GITHUB_TOKEN is not set", "creating a pull request needs a token with pull-request write access")
			}
			if !yes {
				opts.Confirm = e.confirm
			}
			return pr.Run(cmd.Context(), repo, cfg, client, opts, e.p)
		},
	}
	cmd.Flags().StringVar(&opts.Base, "base", "", "base branch (default: resolved from .tide.yml)")
	cmd.Flags().StringVar(&opts.Title, "title", "", "pull request title (default: derived from commits)")
	cmd.Flags().StringVar(&opts.Body, "body", "", "pull request body (default: list of commits)")
	cmd.Flags().BoolVar(&opts.Draft, "draft", false, "open as a draft")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
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
