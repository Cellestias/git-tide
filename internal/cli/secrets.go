package cli

import (
	"github.com/spf13/cobra"

	appsecrets "github.com/cellestias/git-tide/internal/app/secrets"
)

func (e *env) secretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Look for secrets in the files and the history",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	var opts appsecrets.Options
	scan := &cobra.Command{
		Use:   "scan",
		Short: "List the files that hold a likely secret, and with --history the commits that added one",
		Long: "Scan the files of the repository (the committed ones, and the new ones Git does not ignore) for likely secrets, with the same\n" +
			"detectors the commit hook uses: the built-in ones, the custom patterns of .tide.yml and the local ones of secret.tide.yml.\n" +
			"It shows the file, the line and the rule, never the value. A line that carries tide:allow-secret is left alone.\n\n" +
			"--history also looks at what the newest commits of every branch, remote branch and tag added, which finds a key that was committed\n" +
			"and removed later (it is still exposed: rotate it). --limit says how many commits (default 300). Tide never rewrites history.\n\n" +
			"Binary files, files over 1 MiB and symlinks are skipped. With secrets.enabled false in .tide.yml nothing is scanned.\n" +
			"It exits 0 even when it finds something, so a script can read the result; --fail makes it exit 4 instead, for CI.",
		Example: "  gtide secrets scan\n  gtide secrets scan --history\n  gtide secrets scan --history --limit 1000 --json\n  gtide secrets scan --fail",
		Args:    maxArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, cfg, err := e.requireConfig(cmd.Context())
			if err != nil {
				return err
			}
			return appsecrets.Scan(cmd.Context(), repo, cfg, opts, e.p)
		},
	}
	f := scan.Flags()
	f.BoolVar(&opts.History, "history", false, "also look at what the commits added")
	f.IntVar(&opts.Limit, "limit", 0, "with --history, how many commits to read (default 300, at most 5000)")
	f.BoolVar(&opts.JSON, "json", false, "print JSON on stdout")
	f.BoolVar(&opts.Fail, "fail", false, "exit with code 4 when something is found")
	cmd.AddCommand(scan)
	return cmd
}
