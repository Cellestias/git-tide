package doctor

import (
	"context"
	"testing"

	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/testutil"
)

func TestParseVersion(t *testing.T) {
	maj, min, ok := parseVersion("git version 2.39.2 (Apple Git-143)\n")
	if !ok || maj != 2 || min != 39 {
		t.Fatalf("%d.%d %v", maj, min, ok)
	}
	if _, _, ok := parseVersion("garbage"); ok {
		t.Error("garbage parsed")
	}
}

func levels(checks []Check) map[string]Level {
	m := map[string]Level{}
	for _, c := range checks {
		m[c.Name] = c.Level
	}
	return m
}

func TestOutsideRepository(t *testing.T) {
	got := levels(Run(context.Background(), executor.Exec{}, t.TempDir()))
	if got["git"] != OK || got["repository"] != Fail {
		t.Fatalf("levels = %v", got)
	}
}

func TestUnconfiguredRepository(t *testing.T) {
	dir := testutil.NewRepo(t)
	got := levels(Run(context.Background(), executor.Exec{}, dir))
	if got["config"] != Warn || got["hook pre-commit"] != Warn || got["remote"] != Warn {
		t.Fatalf("levels = %v", got)
	}
}

func TestInvalidConfigFails(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.WriteFile(t, dir, ".tide.yml", "version: 1\nupdate:\n  strategy: squash\n")
	got := levels(Run(context.Background(), executor.Exec{}, dir))
	if got["config"] != Fail {
		t.Fatalf("levels = %v", got)
	}
}
