package cli

import (
	"github.com/spf13/cobra"

	"github.com/cellestias/git-tide/internal/app/fetch"
	"github.com/cellestias/git-tide/internal/app/pick"
	"github.com/cellestias/git-tide/internal/app/restore"
	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/output"
)

// minArgs is cobra.MinimumNArgs with an exit-code-2 error.
func minArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.MinimumNArgs(n)(cmd, args); err != nil {
			return apperr.Usage("%v", err)
		}
		return nil
	}
}

func (e *env) pickCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "pick <commit|from..to>...",
		Short: "Copy commits of another branch onto the current one",
		Long: "Cherry-pick the given commits, oldest first, each as a commit of its own that keeps the author and the message.\n" +
			"A commit is a hash, a branch or a tag; <from>..<to> stands for the commits of <to> that <from> lacks.\n" +
			"Before anything changes, every message is checked against the Conventional Commits rules and every commit against the\n" +
			"secret scan, since Git runs no commit hook for a cherry-pick. A commit the branch already has (under any hash) is skipped.\n" +
			"Refused on a protected branch, with uncommitted changes, and for a merge commit. On conflicts, resolve them,\n" +
			"`git add` the files and run `gtide continue`, or give up with `gtide abort`.",
		Example: "  gtide pick 1a2b3c4\n  gtide pick origin/staging~3..origin/staging",
		Args:    minArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			res := pick.Result{Done: []pick.Commit{}, Skipped: []pick.Skipped{}}
			return e.act(ctx, repo, asJSON, "pick", func(p output.Printer) error { return pick.Pick(ctx, repo, cfg, args, &res, p) }, func() any {
				return map[string]any{"picked": res.Done, "skipped": res.Skipped}
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON on stdout (the log goes to stderr)")
	return cmd
}

func (e *env) revertCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "revert <commit|from..to>...",
		Short: "Add a commit that takes back earlier commits, published or not",
		Long: "Make a new commit that undoes each given commit (newest first for a range). Unlike `gtide undo` it works on commits that are\n" +
			"already on the remote, because it adds to the history and does not rewrite it. The commits must be in the current branch.\n" +
			"Refused on a protected branch, with uncommitted changes, and for a merge commit. On conflicts, resolve them,\n" +
			"`git add` the files and run `gtide continue`, or give up with `gtide abort`.",
		Example: "  gtide revert 1a2b3c4\n  gtide revert HEAD~2..HEAD",
		Args:    minArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			res := pick.Result{Done: []pick.Commit{}, Skipped: []pick.Skipped{}}
			return e.act(ctx, repo, asJSON, "revert", func(p output.Printer) error { return pick.Revert(ctx, repo, cfg, args, &res, p) }, func() any {
				return map[string]any{"reverted": res.Done}
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON on stdout (the log goes to stderr)")
	return cmd
}

func (e *env) restoreCmd() *cobra.Command {
	var (
		opts   restore.Options
		yes    bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "restore --from <ref> -f <file>...",
		Short: "Bring files from another branch, tag or commit, without merging anything else",
		Long: "Replace the given files with their version in --from and stage them, so `gtide save` commits them. Nothing else of that\n" +
			"branch comes along. A folder takes every file under it; a file the other side lacks is not deleted here.\n" +
			"The lines brought go through the conflict-marker and secret checks of `gtide save`. Local changes in those files are\n" +
			"overwritten, so Tide asks first unless --yes is given. Refused on a protected branch.",
		Example: "  gtide restore --from origin/staging -f src/teams -f docs/teams.md",
		Args:    maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			opts.Cwd = e.dir
			opts.Confirm = e.confirmUnless(yes)
			ctx := cmd.Context()
			return e.act(ctx, repo, asJSON, "restore", func(p output.Printer) error { return restore.Run(ctx, repo, cfg, opts, p) }, func() any {
				staged, _ := repo.ChangeStat(ctx, true, "")
				return map[string]any{"from": opts.From, "staged": toChanges(staged)}
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.From, "from", "", "the branch, tag or commit to take the files from")
	f.StringArrayVarP(&opts.Files, "file", "f", nil, "file or folder to bring (repeatable)")
	yesFlag(f, &yes, "do not ask before overwriting local changes")
	f.BoolVar(&asJSON, "json", false, "print the result as JSON on stdout (the log goes to stderr)")
	return cmd
}

func (e *env) fetchCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "fetch [remote]",
		Short: "Refresh the remote-tracking branches and change nothing else",
		Long: "Fetch every branch of the remote (the one in .tide.yml unless [remote] is given) and drop the remote-tracking branches\n" +
			"whose branch is gone. The work tree, the index and the local branches are not touched, so it is safe with uncommitted work.\n" +
			"To bring the new commits into the current branch, use `gtide pull`.",
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			remote := ""
			if len(args) == 1 {
				remote = args[0]
			}
			ctx := cmd.Context()
			var st fetch.Status
			return e.act(ctx, repo, asJSON, "fetch", func(p output.Printer) error { return fetch.Run(ctx, repo, cfg, remote, &st, p) }, func() any {
				return st
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON on stdout (the log goes to stderr)")
	return cmd
}
