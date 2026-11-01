package executor

import (
	"context"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	got := Redact("fatal: unable to access 'https://user:tok3n@github.com/o/r.git/'")
	if strings.Contains(got, "tok3n") || strings.Contains(got, "user") {
		t.Fatalf("credentials leaked: %s", got)
	}
	if !strings.Contains(got, "github.com/o/r.git") {
		t.Fatalf("over-redacted: %s", got)
	}
}

func TestRunSeparatesStreamsAndReportsExit(t *testing.T) {
	dir := t.TempDir()
	if _, err := (Exec{}).Run(context.Background(), Command{Args: []string{"init", "-q"}, Dir: dir}); err != nil {
		t.Fatal(err)
	}
	res, err := (Exec{}).Run(context.Background(), Command{Args: []string{"rev-parse", "--verify", "nope"}, Dir: dir})
	if err == nil {
		t.Fatal("expected error")
	}
	if ExitCode(err) <= 0 {
		t.Errorf("ExitCode = %d", ExitCode(err))
	}
	if res.Stdout != "" && res.Stderr == "" {
		t.Errorf("streams mixed: %+v", res)
	}
}

func TestRunCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (Exec{}).Run(ctx, Command{Args: []string{"--version"}, Dir: t.TempDir()})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}
