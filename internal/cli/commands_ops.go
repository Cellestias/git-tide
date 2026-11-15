package cli

import (
	"github.com/spf13/cobra"

	"github.com/cellestias/git-tide/internal/app/branches"
	"github.com/cellestias/git-tide/internal/app/pull"
	"github.com/cellestias/git-tide/internal/app/stash"
	"github.com/cellestias/git-tide/internal/app/status"
	"github.com/cellestias/git-tide/internal/app/tag"
	"github.com/cellestias/git-tide/internal/app/undo"
	"github.com/spf13/pflag"
)

// confirmUnless returns e.confirm, or nil when the user passed --yes.
func (e *env) confirmUnless(yes bool) func(string) (bool, error) {
	if yes {
		return nil
	}
	return e.confirm
}

func yesFlag(f *pflag.FlagSet, yes *bool, usage string) {
	f.BoolVarP(yes, "yes", "y", false, usage)
}

func (e *env) pullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Bring the remote copy of the current branch into it",
		Long: "Fetch the branch the current branch follows (its upstream, else the same name on the configured remote) and integrate it:\n" +
			"a fast-forward when possible, otherwise a merge or rebase per update.strategy. Stops before touching uncommitted work and\n" +
			"never stashes or resets. To bring the base branch in, use `gtide update`.",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			return pull.Run(cmd.Context(), repo, cfg, e.p)
		},
	}
}

func (e *env) statusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the branch, how far it is from its upstream and base, and what changed",
		Long: "Show the current branch, its distance from its upstream and from its base branch, any merge or rebase in progress, and the\n" +
			"changed files in groups: conflicts, staged, not staged and untracked. Local data only, so the distances are as of the last fetch.",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				return status.RunJSON(cmd.Context(), repo, cfg, e.p)
			}
			return status.Run(cmd.Context(), repo, cfg, e.p)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON on stdout")
	return cmd
}

func (e *env) diffCmd() *cobra.Command {
	var opts status.DiffOptions
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Show what changed: per file, or the full patch",
		Long: "Show the work tree against the index (what `gtide save` would add), or with --staged the index against the last commit\n" +
			"(what it would commit). A per-file summary by default; --patch prints the whole diff. Untracked files are in `gtide status`.",
		Example: "  gtide diff\n  gtide diff --staged\n  gtide diff -f src/auth/login.go --patch",
		Args:    maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, _, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			opts.Cwd = e.dir
			return status.Diff(cmd.Context(), repo, opts, e.p)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.Staged, "staged", false, "the index against the last commit")
	f.StringVarP(&opts.File, "file", "f", "", "only this `file` or directory")
	f.BoolVar(&opts.Patch, "patch", false, "print the full diff")
	f.BoolVar(&opts.JSON, "json", false, "print JSON on stdout (the files, and the patch with --patch)")
	return cmd
}

func (e *env) stashCmd() *cobra.Command {
	var (
		message   string
		untracked bool
	)
	run := func(cmd *cobra.Command, _ []string) error {
		repo, _, err := e.requireConfig(cmd.Context())
		if err != nil {
			return err
		}
		return stash.Push(cmd.Context(), repo, message, untracked, e.p)
	}
	cmd := &cobra.Command{
		Use:   "stash [push|list|pop|drop]",
		Short: "Put local changes aside and bring them back",
		Long: "Save the uncommitted changes of tracked files and leave the work tree clean, so `gtide update` and `gtide pull` can run.\n" +
			"Untracked files stay in place unless --untracked. Ignored files, such as secret.tide.yml, are never touched.\n" +
			"`stash pop` brings the newest stash back (or the one you name) and removes it; `stash list` shows them; `stash drop` deletes one.",
		Example: "  gtide stash -m \"half-done login form\"\n  gtide stash list\n  gtide stash pop",
		Args:    cobra.NoArgs,
		RunE:    run,
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "a note to find the stash by")
	cmd.Flags().BoolVarP(&untracked, "untracked", "u", false, "also stash untracked files")

	push := &cobra.Command{Use: "push", Short: "Stash the local changes (same as `gtide stash`)", Args: cobra.NoArgs, RunE: run}
	push.Flags().StringVarP(&message, "message", "m", "", "a note to find the stash by")
	push.Flags().BoolVarP(&untracked, "untracked", "u", false, "also stash untracked files")
	list := &cobra.Command{
		Use: "list", Short: "List the stashes", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, _, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			return stash.List(cmd.Context(), repo, e.p)
		},
	}
	pop := &cobra.Command{
		Use: "pop [stash]", Short: "Apply a stash and remove it", Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, _, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			ref, err := stash.ParseRef(first(args))
			if err != nil {
				return err
			}
			return stash.Pop(cmd.Context(), repo, ref, e.p)
		},
	}
	var yes bool
	drop := &cobra.Command{
		Use: "drop [stash]", Short: "Delete a stash for good", Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, _, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			ref, err := stash.ParseRef(first(args))
			if err != nil {
				return err
			}
			return stash.Drop(cmd.Context(), repo, ref, e.confirmUnless(yes), e.p)
		},
	}
	yesFlag(drop.Flags(), &yes, "do not ask for confirmation")
	cmd.AddCommand(push, list, pop, drop)
	return cmd
}

func first(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

func (e *env) tagCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "tag [create|push|delete]",
		Short: "List, create, publish and delete version tags",
		Long: "Without a subcommand, list the tags, newest version first.\n" +
			"`tag create <version>` makes an annotated tag, only on a protected branch, only on a commit that is already on the remote,\n" +
			"and never moves an existing tag. The name must be a version (v1.4.0, v2.0.0-rc.1). --push publishes it after asking:\n" +
			"a version tag can start a release build.",
		Example: "  gtide tag\n  gtide tag create v1.4.0 -m \"Release 1.4.0\" --push\n  gtide tag push v1.4.0\n  gtide tag delete v1.4.0 --remote",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, _, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			return tag.List(cmd.Context(), repo, limit, e.p)
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", tag.DefaultLimit, "maximum number of tags (0 for all)")

	var (
		copts tag.CreateOptions
		yes   bool
	)
	create := &cobra.Command{
		Use: "create <version>", Short: "Tag the current commit", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			copts.Confirm = e.confirmUnless(yes)
			return tag.Create(cmd.Context(), repo, cfg, args[0], copts, e.p)
		},
	}
	create.Flags().StringVarP(&copts.Message, "message", "m", "", "the tag message (default: the tag name)")
	create.Flags().BoolVar(&copts.Push, "push", false, "publish the tag to the remote after creating it")
	create.Flags().BoolVar(&copts.AnyBranch, "any-branch", false, "allow a branch that is not protected")
	yesFlag(create.Flags(), &yes, "do not ask before publishing")

	var pushYes bool
	push := &cobra.Command{
		Use: "push <version>", Short: "Publish an existing tag", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			return tag.Push(cmd.Context(), repo, cfg, args[0], e.confirmUnless(pushYes), e.p)
		},
	}
	yesFlag(push.Flags(), &pushYes, "do not ask for confirmation")

	var (
		remote bool
		delYes bool
	)
	del := &cobra.Command{
		Use: "delete <version>", Short: "Delete a tag locally, and with --remote on the remote", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			return tag.Delete(cmd.Context(), repo, cfg, args[0], remote, e.confirmUnless(delYes), e.p)
		},
	}
	del.Flags().BoolVar(&remote, "remote", false, "also delete the tag on the remote")
	yesFlag(del.Flags(), &delYes, "do not ask for confirmation")

	cmd.AddCommand(create, push, del)
	return cmd
}

func (e *env) undoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "undo",
		Short: "Take back the last commit and keep its changes staged",
		Long: "Move the branch to the parent of the last commit and keep that commit's changes staged, so nothing is lost and `gtide save`\n" +
			"can commit them again. Refused when the commit is already on the remote, is a merge, or is the first of the repository.",
		Args: maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, _, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			return undo.Undo(cmd.Context(), repo, e.p)
		},
	}
}

func (e *env) amendCmd() *cobra.Command {
	var opts undo.AmendOptions
	cmd := &cobra.Command{
		Use:   "amend",
		Short: "Fold staged changes into the last commit, and optionally reword it",
		Long: "Replace the last commit with the staged changes (and the files given with -f) and, with -m, a new message. The message and the\n" +
			"staged lines are checked like in `gtide save`. Refused when the commit is already on the remote, is a merge, or the branch is protected.",
		Example: "  gtide amend -f src/auth/login.go             # add a forgotten file\n  gtide amend -m \"fix(auth): reject empty token\"  # reword",
		Args:    maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			opts.Cwd = e.dir
			return undo.Amend(cmd.Context(), repo, cfg, opts, e.p)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.Message, "message", "m", "", "new commit message (the subject line)")
	f.StringVar(&opts.Body, "body", "", "new commit body")
	f.StringVar(&opts.Type, "type", "", "Conventional Commits type; builds \"type(scope): message\" from -m")
	f.StringVar(&opts.Scope, "scope", "", "scope for --type")
	f.StringArrayVarP(&opts.Files, "file", "f", nil, "stage this file or directory first (repeatable)")
	return cmd
}

// branchManageCmds are the subcommands of `gtide branch`.
func (e *env) branchManageCmds() []*cobra.Command {
	var (
		dopts branches.DeleteOptions
		yes   bool
	)
	del := &cobra.Command{
		Use:     "delete [branch...]",
		Aliases: []string{"rm"},
		Short:   "Delete branches that are merged (or any, with --force)",
		Long: "Delete local branches. A branch must be merged into its base unless --force; protected branches and the checked-out one are\n" +
			"never deleted. --merged adds every merged branch (the ones `gtide branch --merged` lists). --remote also deletes the remote copy.\n" +
			"It asks once, listing what will go, unless --yes.",
		Example: "  gtide branch delete feature/old\n  gtide branch delete --merged\n  gtide branch delete feature/old --remote --yes",
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			dopts.Confirm = e.confirmUnless(yes)
			return branches.Delete(cmd.Context(), repo, cfg, args, dopts, e.p)
		},
	}
	del.Flags().BoolVar(&dopts.Merged, "merged", false, "also delete every branch already merged into its base")
	del.Flags().BoolVar(&dopts.Remote, "remote", false, "also delete the branch on the remote")
	del.Flags().BoolVar(&dopts.Force, "force", false, "delete branches that are not merged: their commits are lost")
	yesFlag(del.Flags(), &yes, "do not ask for confirmation")

	rename := &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a local branch",
		Args:  exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			return branches.Rename(cmd.Context(), repo, cfg, args[0], args[1], e.p)
		},
	}
	return []*cobra.Command{del, rename}
}
