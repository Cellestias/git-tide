// Package executor runs the Git binary with an argument list, never a shell.
package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Command is one Git invocation. Args excludes the "git" executable itself.
type Command struct {
	Args []string
	Dir  string
}

type Result struct {
	Stdout string
	Stderr string
}

type Executor interface {
	Run(ctx context.Context, command Command) (Result, error)
}

// ExitError is returned when Git runs but exits non-zero.
type ExitError struct {
	Args     []string
	ExitCode int
	Stderr   string
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		return fmt.Sprintf("git %s: exit status %d", RedactArgs(e.Args), e.ExitCode)
	}
	return fmt.Sprintf("git %s: %s", RedactArgs(e.Args), Redact(msg))
}

// Exec is the production Executor backed by os/exec.
type Exec struct {
	// Binary defaults to "git".
	Binary string
}

func (x Exec) Run(ctx context.Context, c Command) (Result, error) {
	bin := x.Binary
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, c.Args...)
	cmd.Dir = c.Dir
	// Keep output parseable regardless of the user's locale; never block
	// background commands on an optional index lock.
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0")

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return res, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, fmt.Errorf("git %s: %w", RedactArgs(c.Args), ctxErr)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return res, &ExitError{Args: c.Args, ExitCode: ee.ExitCode(), Stderr: res.Stderr}
	}
	return res, fmt.Errorf("run git: %w", err)
}

var userinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/@\s]+@`)

// Redact hides credentials embedded in URLs.
func Redact(s string) string { return userinfo.ReplaceAllString(s, "${1}***@") }

// RedactArgs joins args for display with credentials hidden.
func RedactArgs(args []string) string { return Redact(strings.Join(args, " ")) }

// ExitCode returns the Git exit code carried by err, or -1.
func ExitCode(err error) int {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode
	}
	return -1
}
