// Package cli defines the tide commands. It parses input, calls one use case,
// and renders the result; it holds no Git workflow logic.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/config"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

// Version is set at build time: -ldflags "-X github.com/cellestias/git-tide/internal/cli.Version=v0.1.0".
var Version = "dev"

type env struct {
	ex  executor.Executor
	in  io.Reader
	p   output.Printer
	dir string
}

// Execute runs the CLI and returns the process exit code.
func Execute(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(errOut, "✗ cannot determine working directory:", err)
		return apperr.CodeGeneral
	}
	e := &env{ex: executor.Exec{}, in: in, p: output.Printer{Out: out, Err: errOut}, dir: dir}
	return e.run(ctx, args, out, errOut)
}

func (e *env) run(ctx context.Context, args []string, out, errOut io.Writer) int {
	root := e.rootCmd()
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(errOut)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return apperr.CodeOK
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		// cobra's own errors (unknown command or flag) are usage errors
		err = apperr.Usage("%v", err)
	}
	e.p.Fail("%s", err)
	if h := apperr.HintOf(err); h != "" {
		fmt.Fprintf(errOut, "  hint: %s\n", h)
	}
	return apperr.CodeOf(err)
}

func (e *env) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "tide",
		Short:         "Git Tide — move with the flow",
		Long:          "Git Tide sits on top of Git and makes common workflows safer, faster, and more consistent.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE:          func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return apperr.Usage("%v", err) })
	root.AddCommand(e.initCmd(), e.createCmd(), e.pushCmd(), e.updateCmd(), e.reviewCmd(), e.prCmd(), e.doctorCmd(), e.configCmd(), e.hookCmd())
	return root
}

// exactArgs is cobra.ExactArgs with an exit-code-2 error.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(n)(cmd, args); err != nil {
			return apperr.Usage("%v", err)
		}
		return nil
	}
}

func maxArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.MaximumNArgs(n)(cmd, args); err != nil {
			return apperr.Usage("%v", err)
		}
		return nil
	}
}

// openRepo finds the repository containing the working directory.
func (e *env) openRepo(ctx context.Context) (*repository.Repository, error) {
	repo, err := repository.Open(ctx, e.ex, e.dir)
	switch {
	case errors.Is(err, repository.ErrNotRepository):
		return nil, apperr.New(apperr.CodeUnsafeRepo, "not inside a Git repository", "run tide from within a repository")
	case err != nil:
		return nil, apperr.Wrap(apperr.CodeExternal, "cannot run git", "make sure Git is installed and on PATH", err)
	}
	return repo, nil
}

// requireConfig opens the repository and loads a configuration that must exist.
func (e *env) requireConfig(ctx context.Context) (*repository.Repository, domain.Config, error) {
	repo, err := e.openRepo(ctx)
	if err != nil {
		return nil, domain.Config{}, err
	}
	res, err := config.Load(repo.Root)
	if err != nil {
		return nil, domain.Config{}, err
	}
	if !res.Found {
		return nil, domain.Config{}, apperr.New(apperr.CodeConfig, config.FileName+" not found", "run `tide init` first")
	}
	return repo, res.Config, nil
}

type hookCtx struct {
	repo *repository.Repository
	cfg  domain.Config
}

// confirm asks a yes/no question on the error stream and reads the answer from
// stdin. Anything but "y"/"yes" (including EOF) is a no.
func (e *env) confirm(prompt string) (bool, error) {
	fmt.Fprint(e.p.Err, prompt)
	line, err := bufio.NewReader(e.in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}
