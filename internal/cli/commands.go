package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/cellestias/git-tide/internal/app/branches"
	"github.com/cellestias/git-tide/internal/app/change"
	appclone "github.com/cellestias/git-tide/internal/app/clone"
	appcommit "github.com/cellestias/git-tide/internal/app/commit"
	"github.com/cellestias/git-tide/internal/app/create"
	"github.com/cellestias/git-tide/internal/app/doctor"
	"github.com/cellestias/git-tide/internal/app/history"
	apphooks "github.com/cellestias/git-tide/internal/app/hooks"
	"github.com/cellestias/git-tide/internal/app/initrepo"
	"github.com/cellestias/git-tide/internal/app/pr"
	"github.com/cellestias/git-tide/internal/app/push"
	remoteapp "github.com/cellestias/git-tide/internal/app/remote"
	"github.com/cellestias/git-tide/internal/app/resolve"
	"github.com/cellestias/git-tide/internal/app/review"
	"github.com/cellestias/git-tide/internal/app/update"
	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/config"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/provider"
)

func (e *env) initCmd() *cobra.Command {
	var (
		opts initrepo.Options
		yes  bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create " + config.FileName + ", " + config.SecretFileName + " and install Git hooks",
		Long: "Create " + config.FileName + " (shared settings) and " + config.SecretFileName + " (local credentials reference and\n" +
			"secret patterns), add " + config.SecretFileName + " to .gitignore, and install Tide's Git hooks. Existing files are never overwritten.\n" +
			"An existing hook is preserved as <hook>.pre-tide and still runs after Tide's checks.\n" +
			"Outside a Git repository it offers to create one first (git init), with --base as its first branch (default main).\n" +
			"--remote <url> registers the remote (origin, or `remote` from " + config.FileName + "); the provider is then detected from it.\n" +
			"--provider chooses github, gitlab, or auto (default: detect from the remote).",
		Example: "  gtide init                                        # in an existing repository\n" +
			"  gtide init --remote git@github.com:me/app.git    # also set the remote\n" +
			"  mkdir app && cd app && gtide init --yes --remote https://gitlab.com/me/app.git   # a new project",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := repository.Open(cmd.Context(), e.ex, e.dir)
			switch {
			case errors.Is(err, repository.ErrNotRepository):
				if repo, err = e.createRepo(cmd.Context(), opts.Base, yes); err != nil {
					return err
				}
			case err != nil:
				return apperr.Wrap(apperr.CodeExternal, "cannot run git", "make sure Git is installed and on PATH", err)
			}
			res, err := initrepo.Run(cmd.Context(), repo, opts)
			e.reportInit(res)
			return err
		},
	}
	cmd.Flags().StringVar(&opts.Base, "base", "", "default base branch (default: detected from the repository)")
	cmd.Flags().StringVar(&opts.Provider, "provider", "", "hosting provider: auto, github or gitlab (default: auto)")
	cmd.Flags().StringVar(&opts.Remote, "remote", "", "register this `url` as the remote")
	cmd.Flags().BoolVar(&opts.NoHooks, "no-hooks", false, "do not install Git hooks")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "create the Git repository without asking when there is none")
	return cmd
}

// createRepo runs `git init` in the current folder, after confirmation. The
// first branch is name, or main.
func (e *env) createRepo(ctx context.Context, name string, yes bool) (*repository.Repository, error) {
	if name == "" {
		name = "main"
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.Clean(e.dir) == filepath.Clean(home) || filepath.Dir(e.dir) == e.dir {
		return nil, apperr.New(apperr.CodeUnsafeRepo, "refusing to create a Git repository in "+e.dir, "go into a project folder first")
	}
	if !yes {
		ok, err := e.confirm(fmt.Sprintf("No Git repository in %s. Create one with branch %q? [y/N] ", e.dir, name))
		if err != nil {
			return nil, apperr.Wrap(apperr.CodeGeneral, "cannot read confirmation", "use --yes to skip the prompt", err)
		}
		if !ok {
			return nil, apperr.New(apperr.CodeUnsafeRepo, "not inside a Git repository; nothing was created",
				"run `git init`, or `gtide init --yes` to create one here")
		}
	}
	repo, err := repository.Init(ctx, e.ex, e.dir, name)
	if err != nil {
		if errors.As(err, new(*apperr.Error)) {
			return nil, err
		}
		return nil, apperr.Wrap(apperr.CodeExternal, "cannot create the Git repository", "", err)
	}
	e.p.Success("created a Git repository in %s (branch %s)", repo.Root, name)
	return repo, nil
}

// reportInit prints what init (or clone) did.
func (e *env) reportInit(res initrepo.Result) {
	for _, c := range res.HookChanges {
		e.p.Success("hook %s", c.Describe())
	}
	if res.RemoteURL != "" {
		if res.RemoteAdded {
			e.p.Success("added remote %s: %s", res.RemoteName, res.RemoteURL)
		} else {
			e.p.Info("remote %s already points to %s", res.RemoteName, res.RemoteURL)
		}
	}
	if res.ConfigPath == "" {
		return
	}
	if res.ConfigCreated {
		e.p.Success("created %s (provider: %s, base branch: %s)", res.ConfigPath, res.Provider, res.Base)
	} else if res.Base != "" {
		e.p.Info("preserved %s (already exists)", res.ConfigPath)
	}
	for _, n := range res.Notes {
		e.p.Warn("%s", n)
	}
	if res.SecretCreated {
		e.p.Success("created %s (set the token variable it references)", res.SecretPath)
	} else if res.Base != "" {
		e.p.Info("preserved %s (already exists)", res.SecretPath)
	}
	switch {
	case res.IgnoreCreated:
		e.p.Success("created .gitignore with %s", config.SecretFileName)
	case res.IgnoreUpdated:
		e.p.Success("updated .gitignore: added %s", config.SecretFileName)
	case res.Base != "":
		e.p.Info("preserved .gitignore (%s already ignored)", config.SecretFileName)
	}
	if res.HooksSkipped {
		e.p.Info("hooks not installed")
	}
}

func (e *env) cloneCmd() *cobra.Command {
	var opts initrepo.Options
	cmd := &cobra.Command{
		Use:   "clone <url> [folder]",
		Short: "Clone a repository and set it up for Tide",
		Long: "git clone, then `gtide init` in the copy: " + config.FileName + ", " + config.SecretFileName + " and the Git hooks.\n" +
			"The folder defaults to the repository name. An existing, non-empty folder is refused.\n" +
			"A " + config.FileName + " already committed in the repository is kept as it is.",
		Example: "  gtide clone git@github.com:me/app.git\n  gtide clone https://gitlab.com/group/app.git my-app",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := ""
			if len(args) == 2 {
				target = args[1]
			}
			e.p.Step("Cloning %s", executor.Redact(args[0]))
			res, err := appclone.Run(cmd.Context(), e.ex, e.dir, args[0], target, opts)
			if res.Dir != "" {
				e.p.Success("cloned into %s", res.Dir)
			}
			e.reportInit(res.Init)
			if err == nil {
				e.p.Info("next: cd %s", res.Dir)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&opts.Base, "base", "", "default base branch (default: detected from the repository)")
	cmd.Flags().StringVar(&opts.Provider, "provider", "", "hosting provider: auto, github or gitlab (default: auto)")
	cmd.Flags().BoolVar(&opts.NoHooks, "no-hooks", false, "do not install Git hooks")
	return cmd
}

func (e *env) remoteCmd() *cobra.Command {
	name := ""
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "remote [add|set-url|show]",
		Short: "Add, change or show the remote",
		Long: "Manage the remote Tide publishes to: the one named by `remote` in " + config.FileName + ", or origin. The URL is checked first:\n" +
			"https, ssh and scp-like addresses are accepted; a password or token inside the URL, and remote helpers like ext::, are refused.\n" +
			"Without a subcommand it shows the remotes.",
		Example: "  gtide remote add git@github.com:me/app.git\n  gtide remote set-url https://github.com/me/app.git\n  gtide remote",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return e.remoteShow(cmd.Context(), asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the remotes as JSON on stdout")
	cmd.PersistentFlags().StringVar(&name, "name", "", "remote name (default: `remote` from "+config.FileName+", else origin)")
	add := &cobra.Command{
		Use:   "add <url>",
		Short: "Register the remote",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, n, err := e.remoteTarget(cmd.Context(), name)
			if err != nil {
				return err
			}
			return remoteapp.Add(cmd.Context(), repo, n, args[0], e.p)
		},
	}
	set := &cobra.Command{
		Use:   "set-url <url>",
		Short: "Point the remote at another URL",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, n, err := e.remoteTarget(cmd.Context(), name)
			if err != nil {
				return err
			}
			return remoteapp.SetURL(cmd.Context(), repo, n, args[0], e.p)
		},
	}
	var showJSON bool
	show := &cobra.Command{
		Use:   "show",
		Short: "List the remotes and their provider",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return e.remoteShow(cmd.Context(), showJSON)
		},
	}
	show.Flags().BoolVar(&showJSON, "json", false, "print the remotes as JSON on stdout")
	cmd.AddCommand(add, set, show)
	return cmd
}

// remoteTarget opens the repository and picks the remote name: --name, else the
// one in .tide.yml, else origin. It works before `gtide init`.
func (e *env) remoteTarget(ctx context.Context, name string) (*repository.Repository, string, error) {
	repo, err := e.openRepo(ctx)
	if err != nil {
		return nil, "", err
	}
	if name != "" {
		return repo, name, nil
	}
	res, err := config.Load(repo.Root)
	if err != nil {
		return nil, "", err
	}
	if res.Found && res.Config.Remote != "" {
		return repo, res.Config.Remote, nil
	}
	return repo, remoteapp.DefaultName, nil
}

func (e *env) remoteShow(ctx context.Context, asJSON bool) error {
	repo, name, err := e.remoteTarget(ctx, "")
	if err != nil {
		return err
	}
	return remoteapp.Show(ctx, repo, name, asJSON, e.p)
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
	var (
		opts review.Options
		yes  bool
	)
	cmd := &cobra.Command{
		Use:   "review <PR>",
		Short: "Check out a Pull Request / Merge Request on a local review branch",
		Long: "Fetch a Pull Request (GitHub) or Merge Request (GitLab) and check it out as " + "tide/review/pr-<number>.\n" +
			"<PR> is a number, #number (!number on GitLab), or a request URL. Set the provider token (GITHUB_TOKEN or GITLAB_TOKEN,\n" +
			"or provider_token in secret.tide.yml) for private repositories.\n" +
			"It works for requests from forks too, prints the request's metadata and a summary of its changes against the base branch,\n" +
			"and with --diff the whole patch. --reset moves an existing review branch back to the request and asks first when that\n" +
			"would discard local commits that are not part of it.",
		Example: "  gtide review 42\n  gtide review 42 --diff\n  gtide review 42 --reset",
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			client, err := e.provider(cmd.Context(), repo, cfg)
			if err != nil {
				return err
			}
			opts.Ref = args[0]
			if !yes {
				opts.Confirm = e.confirm
			}
			return review.Run(cmd.Context(), repo, cfg, client, opts, e.p)
		},
	}
	cmd.Flags().BoolVar(&opts.Reset, "reset", false, "discard local commits on an existing review branch (asks first)")
	cmd.Flags().BoolVar(&opts.Diff, "diff", false, "print the full patch against the base branch")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation of --reset")
	return cmd
}

func (e *env) changeCmd() *cobra.Command {
	var opts change.Options
	cmd := &cobra.Command{
		Use:     "change <branch>",
		Aliases: []string{"switch"},
		Short:   "Switch to another branch safely",
		Long: "Switch to a branch. <branch> may be a partial name when it is unambiguous (`gtide change two-fa`),\n" +
			"or - for the previous branch. A branch that only exists on the remote is created locally with its upstream.\n" +
			"Refuses with uncommitted tracked changes unless --carry is given, and while a merge or rebase is in progress.\n" +
			"No network call is made: remote branches are the ones already fetched (see `gtide branch --remote`).",
		Example: "  gtide change feature/two-fa\n" +
			"  gtide change two-fa     # unique partial match\n" +
			"  gtide change -          # previous branch\n" +
			"  gtide change main --carry",
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
		opts         branches.Options
		stale        int
		sortBy       string
		legacyHosted bool
	)
	cmd := &cobra.Command{
		Use:     "branch",
		Aliases: []string{"branches"},
		Short:   "List branches with their base, upstream, status and last activity",
		Long: "List local branches with the last update, last commit author, ahead/behind against the upstream and against\n" +
			"the base branch from .tide.yml, and whether they are protected or already merged.\n" +
			"Local data only by default; --remote and --hosted add network calls.\n" +
			"The author is the author of the last commit: Git does not record who created a branch.",
		Example: "  gtide branch\n" +
			"  gtide branch --remote --hosted     # include remote-only branches, provider protection and open PRs/MRs\n" +
			"  gtide branch --mine --sort name\n" +
			"  gtide branch --merged              # candidates for cleanup\n" +
			"  gtide branch --stale 30",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			opts.StaleDays, opts.Sort = stale, sortBy
			opts.Provider = opts.Provider || legacyHosted
			var gh provider.Provider
			if opts.Provider {
				client, err := e.provider(cmd.Context(), repo, cfg)
				if err != nil {
					return err
				}
				gh = client
			}
			return branches.Run(cmd.Context(), repo, cfg, gh, opts, e.p)
		},
	}
	cmd.AddCommand(e.branchManageCmds()...)
	f := cmd.Flags()
	f.BoolVar(&opts.Remote, "remote", false, "also list branches that only exist on the remote (fetches first)")
	f.BoolVar(&opts.Provider, "hosted", false, "add a GITHUB/GITLAB column: branch protection and open pull/merge request (provider token for private repos)")
	f.BoolVar(&legacyHosted, "github", false, "alias of --hosted")
	_ = f.MarkHidden("github")
	f.BoolVar(&opts.Mine, "mine", false, "only branches whose last author is your git user.email")
	f.IntVar(&stale, "stale", 0, "only branches with no commits for more than `days`")
	f.BoolVar(&opts.Merged, "merged", false, "only branches with nothing the base branch lacks")
	f.BoolVar(&opts.JSON, "json", false, "print JSON on stdout")
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
		Example: "  gtide create feature/two-fa                  # from the configured base, fetched first\n" +
			"  gtide create feature/two-fa --from release/1  # from another branch, tag, or commit\n" +
			"  gtide create feature/two-fa --local           # no network: use the local base\n" +
			"  gtide create feature/two-fa --track           # check out a branch that exists on the remote\n" +
			"  gtide create feature/two-fa --push            # also push and set the upstream",
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
		Example: "  gtide push           # publish the current branch\n" +
			"  gtide push --force   # after rewriting history (rebase, amend); asks first",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			if opts.Force && !yes {
				opts.Confirm = e.confirm
			}
			opts.Security = e.securityCheck(repo, cfg)
			return push.Run(cmd.Context(), repo, cfg, opts, e.p)
		},
	}
	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "overwrite the remote branch if it was rewritten (--force-with-lease)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation of --force")
	return cmd
}

func (e *env) commitCmd() *cobra.Command {
	var opts appcommit.Options
	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Commit what is staged, checking Tide's rules first",
		Long: "Commit the staged changes. Tide checks the branch is not protected, the message follows the Conventional Commits rules,\n" +
			"and the staged lines hold no likely secrets, then runs git commit (your hooks still run).\n" +
			"Use `gtide save` to stage specific files and commit them in one step.",
		Example: "  gtide commit -m \"feat(auth): add login form\"\n" +
			"  gtide commit --type feat --scope auth -m \"add login form\"\n" +
			"  gtide commit -a -m \"fix: handle empty token\"   # also stage tracked changes",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			opts.Cwd = e.dir
			return e.commitThenAlerts(cmd.Context(), repo, cfg, opts)
		},
	}
	messageFlags(cmd, &opts)
	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "stage modifications and deletions of tracked files first")
	return cmd
}

func (e *env) historyCmd() *cobra.Command {
	var (
		opts    history.Options
		compare bool
	)
	cmd := &cobra.Command{
		Use:   "history [<from>..<to>] | --compare <from> <to>",
		Short: "List the commits of a branch, by author or by file",
		Long: "List commits, newest first: hash, date, author and message. Without flags it shows the current branch.\n" +
			"--me keeps the commits made with your git user.email; --member keeps those whose author name or email contains the text.\n" +
			"--branch reads another branch (local, or remote-tracking when it only exists on the remote); --remote reads the remote copy\n" +
			"of the branch and fetches it first. -f shows the commits that touched a file, following renames, with who made each one.\n" +
			"A branch shows only what it adds to its base (from .tide.yml, or --base); --all shows the whole history, and so does -f.\n" +
			"Merge commits are hidden unless --merges. --since and --until take 7d, 2w, 3m, 12h or a date (2026-01-31). --json prints JSON.\n" +
			"--patch adds what each commit changed (with -f, only that file) to the list and to --json.\n" +
			"--all-branches reads every local and remote-tracking branch and tag at once. --graph (with --json) adds each commit's\n" +
			"parents and the branches and tags on it, newest first with parents after their children, to draw a commit graph.\n" +
			"--search keeps the commits whose message contains the text; --type and --scope keep the Conventional Commits of that type\n" +
			"(feat, fix...) or scope (auth...). A range <from>..<to> (branches, tags or hashes) lists what <to> has that <from> does not,\n" +
			"as for release notes. --summary counts the commits per author instead of listing them (it ignores --limit).\n" +
			"`gtide history show <commit>` shows one commit with the files it changed.\n" +
			"--compare <from> <to> shows how far <from> is ahead of and behind <to>, and whether each commit <from> has would\n" +
			"cherry-pick cleanly onto <to> (tried oldest first, each on top of the ones that applied; nothing is changed). It needs\n" +
			"Git 2.40 or later and exits with code 6 when a commit conflicts.\n" +
			"To create a commit, use `gtide commit` or `gtide save`.",
		Example: "  gtide history --me\n" +
			"  gtide history --branch feat/remember-login --member paulo\n" +
			"  gtide history --remote\n" +
			"  gtide history -f src/utils/rotation_keys.py\n" +
			"  gtide history -f src/utils/rotation_keys.py --patch   # with the changes of each commit\n" +
			"  gtide history --all-branches --merges --graph --json  # the data a commit graph is drawn from\n" +
			"  gtide history --me --since 7d       # what you did this week\n" +
			"  gtide history --all --merges        # the full history, merges included\n" +
			"  gtide history --type fix --scope auth --since 2w\n" +
			"  gtide history --search \"login\"\n" +
			"  gtide history v1.3.1..v1.3.2        # what a release added\n" +
			"  gtide history --summary --since 1m  # who did how much\n" +
			"  gtide history show 1a2b3c4\n" +
			"  gtide history --compare feat/sign-in feat/staging",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case compare && opts.Patch:
				return apperr.Usage("--patch does not apply to --compare")
			case compare && len(args) != 2:
				return apperr.Usage("--compare needs two branches: gtide history --compare <from> <to>")
			case !compare && len(args) > 1:
				return apperr.Usage("unexpected argument %q; see `gtide history --help`", args[1])
			case !compare && len(args) == 1:
				opts.Range = args[0]
			}
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			opts.Cwd = e.dir
			if compare {
				return history.Compare(cmd.Context(), repo, cfg, args[0], args[1], opts, e.p)
			}
			return history.Run(cmd.Context(), repo, cfg, opts, e.p)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.Me, "me", false, "only commits made with your git user.email")
	f.StringVarP(&opts.Member, "member", "M", "", "only commits whose author name or email contains `text`")
	f.StringVarP(&opts.Branch, "branch", "b", "", "branch to read (default: the current branch)")
	f.BoolVar(&opts.Remote, "remote", false, "read the remote copy of the branch (fetches it first)")
	f.StringVarP(&opts.File, "file", "f", "", "only commits that touched this `file`")
	f.IntVarP(&opts.Limit, "limit", "n", history.DefaultLimit, "maximum number of commits (0 for all)")
	f.BoolVar(&opts.All, "all", false, "the whole history, not only what the branch adds to its base")
	f.StringVar(&opts.Base, "base", "", "base branch to subtract (default: resolved from .tide.yml)")
	f.BoolVar(&opts.Merges, "merges", false, "include merge commits")
	f.StringVar(&opts.Since, "since", "", "only commits after `when` (7d, 2w, 3m, 12h or a date)")
	f.StringVar(&opts.Until, "until", "", "only commits before `when`")
	f.StringVarP(&opts.Search, "search", "s", "", "only commits whose message contains `text`")
	f.StringVar(&opts.Type, "type", "", "only Conventional Commits of this `type` (feat, fix...)")
	f.StringVar(&opts.Scope, "scope", "", "only Conventional Commits of this `scope`")
	f.BoolVar(&opts.Summary, "summary", false, "count the commits per author instead of listing them")
	f.BoolVar(&opts.JSON, "json", false, "print JSON on stdout")
	f.BoolVar(&opts.AllBranches, "all-branches", false, "read every local and remote-tracking branch and tag, not just one branch")
	f.BoolVar(&opts.Graph, "graph", false, "with --json: add each commit's full hash, parents and the branches and tags on it")
	f.BoolVar(&opts.Patch, "patch", false, "show what each commit changed (only the file's lines with -f)")
	f.BoolVar(&compare, "compare", false, "compare two branches: `gtide history --compare <from> <to>`")
	cmd.AddCommand(e.historyShowCmd())
	return cmd
}

func (e *env) historyShowCmd() *cobra.Command {
	var asJSON, patch bool
	cmd := &cobra.Command{
		Use:     "show <commit>",
		Short:   "Show one commit: author, message and the files it changed",
		Example: "  gtide history show 1a2b3c4\n  gtide history show 1a2b3c4 --patch\n  gtide history show 1a2b3c4 --json",
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, _, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			return history.Show(cmd.Context(), repo, args[0], asJSON, patch, e.p)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON on stdout")
	cmd.Flags().BoolVar(&patch, "patch", false, "also print what the commit changed (in --json too)")
	return cmd
}

func (e *env) saveCmd() *cobra.Command {
	var opts appcommit.Options
	cmd := &cobra.Command{
		Use:   "save",
		Short: "Stage changes and commit them in one step",
		Long: "Stage and commit in one step. With -f, only the files you name are staged (like `git add <files>`) and Tide refuses\n" +
			"if other files are already staged. Without -f, every change is staged (like `git add --all`: modified, deleted and\n" +
			"new files that .gitignore does not exclude) and the staged list is shown. If a check fails, the index is restored\n" +
			"and nothing stays staged.",
		Example: "  gtide save -m \"feat: add login form\"                  # everything that changed\n" +
			"  gtide save -m \"feat(auth): add login form\" -f src/auth/login_form.tsx\n" +
			"  gtide save --type fix -m \"handle empty token\" -f api/token.go -f api/token_test.go\n" +
			"  gtide save -m \"docs: update readme\" -f docs/   # a directory stages everything under it",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			opts.Everything = len(opts.Files) == 0
			opts.Cwd = e.dir
			return e.commitThenAlerts(cmd.Context(), repo, cfg, opts)
		},
	}
	messageFlags(cmd, &opts)
	cmd.Flags().StringArrayVarP(&opts.Files, "file", "f", nil, "stage only this file or directory (repeatable); default: every change")
	return cmd
}

func messageFlags(cmd *cobra.Command, opts *appcommit.Options) {
	f := cmd.Flags()
	f.StringVarP(&opts.Message, "message", "m", "", "commit message (the subject line)")
	f.StringVar(&opts.Body, "body", "", "commit body, after a blank line")
	f.StringVar(&opts.Type, "type", "", "Conventional Commits type; builds \"type(scope): message\" from -m")
	f.StringVar(&opts.Scope, "scope", "", "scope for --type")
}

func (e *env) continueCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "continue",
		Short: "Finish a merge, rebase, cherry-pick or revert after resolving conflicts",
		Long: "Continue the operation that stopped on conflicts, whichever it is. Every conflict must be resolved and staged first\n" +
			"(`git add <file>`). Tide runs its pre-commit checks (leftover conflict markers, secrets) before continuing, and the\n" +
			"message Git prepared is used as it is. A rebase can stop again on the next commit; run `gtide continue` again then.",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			return resolve.Continue(cmd.Context(), repo, cfg, e.p)
		},
	}
}

func (e *env) abortCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "abort",
		Short: "Give up a merge, rebase, cherry-pick or revert and go back to before it",
		Long: "Abort the operation that stopped on conflicts and restore the branch to how it was before it started.\n" +
			"Conflict resolutions made so far are discarded, so Tide asks first unless --yes is given.",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, _, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			var opts resolve.Options
			if !yes {
				opts.Confirm = e.confirm
			}
			return resolve.Abort(cmd.Context(), repo, opts, e.p)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

func (e *env) prCmd() *cobra.Command {
	var (
		opts pr.Options
		yes  bool
	)
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Push the current branch and open a Pull Request / Merge Request",
		Long: "Push the current branch and open a Pull Request (GitHub) or Merge Request (GitLab) against its base branch\n" +
			"(from .tide.yml, or --base). The provider comes from `provider` in .tide.yml, detected from the remote when \"auto\".\n" +
			"In a terminal it asks for the title and the body; an empty answer keeps the default: the commit subject (one commit)\n" +
			"or the branch name for the title, the list of commits for the body. If description.pr.md exists at the repository root\n" +
			"it asks instead whether to build the request from that file (first line `# Title`, the rest is the body; placeholders\n" +
			"{{commits}}, {{branch}}, {{base}} and {{commit_count}}). --title and --body skip their prompts, and --yes or a\n" +
			"non-interactive stdin asks nothing and uses the file when present, else the defaults.\n" +
			"Requires the provider token (GITHUB_TOKEN / GITLAB_TOKEN, or provider_token in secret.tide.yml; .env at the repository root\n" +
			"also works) with write access. Asks for confirmation unless --yes is given.\n\n" +
			"Subcommands: `pr list`, `pr view <id>` and `pr status` read from the provider; `gtide review <id>` checks a request out.",
		Example: "  gtide pr --draft\n  gtide pr --base develop --title \"Add authentication\"\n  gtide pr list --state merged --author me\n  gtide pr view 42\n  gtide pr status",
		Args:    maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			client, err := e.provider(cmd.Context(), repo, cfg)
			if err != nil {
				return err
			}
			if !client.HasToken() {
				return apperr.New(apperr.CodeConfig, client.TokenEnv()+" is not set",
					"export "+client.TokenEnv()+" (and reference it as provider_token: ${"+client.TokenEnv()+"} in secret.tide.yml), or put it in .env at the repository root; it needs "+client.Noun()+" write access")
			}
			if !yes {
				opts.Confirm = e.confirm
				if e.interactive() {
					opts.Ask = e.ask
				}
			}
			opts.Security = e.securityCheck(repo, cfg)
			return pr.Run(cmd.Context(), repo, cfg, client, opts, e.p)
		},
	}
	cmd.Flags().StringVar(&opts.Base, "base", "", "base branch (default: resolved from .tide.yml)")
	cmd.Flags().StringVar(&opts.Title, "title", "", "request title (default: derived from commits)")
	cmd.Flags().StringVar(&opts.Body, "body", "", "request body (default: list of commits)")
	cmd.Flags().BoolVar(&opts.Draft, "draft", false, "open as a draft")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	cmd.AddCommand(e.prListCmd(), e.prViewCmd(), e.prStatusCmd())
	return cmd
}

func (e *env) prListCmd() *cobra.Command {
	var opts pr.ListOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Pull Requests / Merge Requests of the repository",
		Long: "List the Pull Requests (GitHub) or Merge Requests (GitLab) of the repository, newest first: number, title, author,\n" +
			"state, source branch and target branch. Filter with --state (open, closed, merged or all), --author and --assignee;\n" +
			"`me` means the owner of the provider token. Public repositories need no token.",
		Example: "  gtide pr list\n  gtide pr list --state closed --limit 50\n  gtide pr list --author me --assignee alice",
		Args:    maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			client, err := e.provider(cmd.Context(), repo, cfg)
			if err != nil {
				return err
			}
			return pr.List(cmd.Context(), repo, cfg, client, opts, e.p)
		},
	}
	cmd.Flags().StringVar(&opts.State, "state", "open", "open, closed, merged or all")
	cmd.Flags().StringVar(&opts.Author, "author", "", "only requests by this login (`me` for yourself)")
	cmd.Flags().StringVar(&opts.Assignee, "assignee", "", "only requests assigned to this login (`me` for yourself)")
	cmd.Flags().IntVar(&opts.Limit, "limit", pr.DefaultListLimit, fmt.Sprintf("maximum number of requests (1-%d)", pr.MaxListLimit))
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "print the requests as a JSON array on stdout")
	return cmd
}

func (e *env) prViewCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "view <id>",
		Short: "Show the details of a Pull Request / Merge Request",
		Long: "Show a request: title, description, author, assignees, state, branches, dates, review status, changed files,\n" +
			"commits and URL. <id> is a number, #number (!number on GitLab), or a request URL.",
		Example: "  gtide pr view 42",
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			client, err := e.provider(cmd.Context(), repo, cfg)
			if err != nil {
				return err
			}
			return pr.View(cmd.Context(), repo, cfg, client, args[0], asJSON, e.p)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the request, its review, files and commits as JSON on stdout")
	return cmd
}

func (e *env) prStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the Pull Request / Merge Request of the current branch",
		Long: "Show the request opened from the current branch, if any: title, state, review status, branches, outstanding\n" +
			"review requests and URL.",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			client, err := e.provider(cmd.Context(), repo, cfg)
			if err != nil {
				return err
			}
			return pr.Status(cmd.Context(), repo, cfg, client, asJSON, e.p)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the request of the branch (or null) as JSON on stdout")
	return cmd
}

func (e *env) doctorCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose repository, configuration, Git, and hook setup",
		Long: "Check the Git version, the repository, .tide.yml, the remote and provider, secret.tide.yml and .env (ignored, not tracked),\n" +
			"the security level, and the installed hooks. It changes nothing. Exit code 1 when a check fails; warnings do not fail it.\n" +
			"--json prints every check as JSON on stdout, with the same exit code.",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := doctor.Run(cmd.Context(), e.ex, e.dir)
			failed := false
			for _, c := range checks {
				if c.Level == doctor.Fail {
					failed = true
				}
			}
			if asJSON {
				if err := doctor.WriteJSON(e.p, checks); err != nil {
					return err
				}
			} else {
				for _, c := range checks {
					switch c.Level {
					case doctor.OK:
						e.p.Success("%s: %s", c.Name, c.Detail)
					case doctor.Warn:
						e.p.Line("%s %s: %s", e.p.Yellow("!"), c.Name, c.Detail)
					case doctor.Fail:
						e.p.Line("%s %s: %s", e.p.Red("✗"), c.Name, c.Detail)
					}
					if c.Hint != "" && c.Level != doctor.OK {
						e.p.Line("    %s", e.p.Dim("hint: "+c.Hint))
					}
				}
			}
			if failed {
				return apperr.New(apperr.CodeGeneral, "doctor found problems", "")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the checks as JSON on stdout")
	return cmd
}

func (e *env) configCmd() *cobra.Command {
	var showJSON, validateJSON bool
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect or validate Tide configuration",
		Args:  maxArgs(0),
		RunE:  func(cmd *cobra.Command, _ []string) error { return e.configShow(cmd, showJSON) },
	}
	cmd.Flags().BoolVar(&showJSON, "json", false, "print the effective configuration as JSON on stdout")
	show := &cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration (file merged over defaults)",
		Args:  maxArgs(0),
		RunE:  func(cmd *cobra.Command, _ []string) error { return e.configShow(cmd, showJSON) },
	}
	show.Flags().BoolVar(&showJSON, "json", false, "print the effective configuration as JSON on stdout")
	validate := &cobra.Command{
		Use:   "validate",
		Short: "Validate " + config.FileName,
		Long: "Check " + config.FileName + " and secret.tide.yml: they must parse, have no unknown key and pass every rule. Exit code 3 when they do not.\n" +
			"--json prints {valid, path, errors} on stdout, with the same exit code.",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := e.openRepo(cmd.Context())
			if err != nil {
				return err
			}
			res, err := config.Load(repo.Root)
			if validateJSON {
				if jerr := e.p.JSON(config.Validate(res, err)); jerr != nil {
					return apperr.Wrap(apperr.CodeGeneral, "cannot write JSON", "", jerr)
				}
			}
			if err != nil {
				return err
			}
			if !res.Found {
				return apperr.New(apperr.CodeConfig, config.FileName+" not found", "run `gtide init` first")
			}
			if !validateJSON {
				e.p.Success("%s is valid", res.Path)
			}
			return nil
		},
	}
	validate.Flags().BoolVar(&validateJSON, "json", false, "print the result as JSON on stdout")
	cmd.AddCommand(show, validate)
	return cmd
}

func (e *env) configShow(cmd *cobra.Command, asJSON bool) error {
	repo, err := e.openRepo(cmd.Context())
	if err != nil {
		return err
	}
	res, err := config.Load(repo.Root)
	if err != nil {
		return err
	}
	if asJSON {
		if err := e.p.JSON(config.View(res)); err != nil {
			return apperr.Wrap(apperr.CodeGeneral, "cannot write JSON", "", err)
		}
		return nil
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
	_, _ = fmt.Fprint(e.p.Out, string(out))
	return nil
}

// hookCmd is invoked by the scripts that `gtide init` installs.
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
		RunE: hookRun(func(cmd *cobra.Command, args []string, h hookCtx) error {
			remote := ""
			if len(args) > 0 {
				remote = args[0]
			}
			return apphooks.PrePush(cmd.Context(), h.repo, h.cfg, remote, e.in, e.securityGate(h.repo, h.cfg))
		}),
	})
	return cmd
}
