package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/output"
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

func TestEnvFileIsCheckedWhenThereIsOne(t *testing.T) {
	ctx := context.Background()
	dir := testutil.NewRepo(t)
	if _, ok := levels(Run(ctx, executor.Exec{}, dir))[".env"]; ok {
		t.Fatal("no .env, no check")
	}

	testutil.WriteFile(t, dir, ".env", "GITHUB_TOKEN=x\n")
	got := levels(Run(ctx, executor.Exec{}, dir))
	if got[".env"] != Warn {
		t.Errorf("a .env Git does not ignore must be a warning: %v", got)
	}

	testutil.WriteFile(t, dir, ".gitignore", ".env\n")
	if got := levels(Run(ctx, executor.Exec{}, dir)); got[".env"] != OK {
		t.Errorf("ignored: %v", got)
	}

	// tracked is a failure, even when ignored afterwards: the token is already in the history
	testutil.Run(t, dir, "add", "-f", ".env")
	testutil.Commit(t, dir, "chore: oops")
	var tracked Check
	for _, c := range Run(ctx, executor.Exec{}, dir) {
		if c.Name == ".env" {
			tracked = c
		}
	}
	if tracked.Level != Fail || tracked.Hint == "" {
		t.Errorf("tracked .env = %+v", tracked)
	}
}

func TestSecretScanningBeingOffIsAWarning(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.WriteFile(t, dir, ".tide.yml", "version: 1\nsecrets:\n  enabled: false\n")
	if got := levels(Run(context.Background(), executor.Exec{}, dir)); got["secret scanning"] != Warn {
		t.Errorf("levels = %v", got)
	}
	testutil.WriteFile(t, dir, ".tide.yml", "version: 1\n")
	if _, ok := levels(Run(context.Background(), executor.Exec{}, dir))["secret scanning"]; ok {
		t.Error("on by default: nothing to say")
	}
}

func TestCheckJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	checks := []Check{
		{Level: OK, Name: "git", Detail: "git version 2.40"},
		{Level: Warn, Name: ".env", Detail: "is not in .gitignore", Hint: "add it"},
		{Level: Fail, Name: "repository", Detail: "no repository"},
	}
	if err := WriteJSON(output.Printer{Out: &out, Err: &errOut}, checks); err != nil {
		t.Fatal(err)
	}
	var got struct {
		OK       bool
		Warnings int
		Failures int
		Checks   []struct{ Level, Name, Detail, Hint string }
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, &out)
	}
	if got.OK || got.Warnings != 1 || got.Failures != 1 || len(got.Checks) != 3 || got.Checks[1].Level != "warn" || got.Checks[1].Hint != "add it" || got.Checks[2].Level != "fail" {
		t.Errorf("got = %+v", got)
	}
	out.Reset()
	_ = WriteJSON(output.Printer{Out: &out, Err: &errOut}, checks[:1])
	if !strings.Contains(out.String(), `"ok": true`) || !strings.Contains(out.String(), `"hint": ""`) {
		t.Errorf("a clean run is ok, and a missing hint is an empty string:\n%s", &out)
	}
	_ = WriteJSON(output.Printer{Out: &out, Err: &errOut}, nil)
	if !strings.Contains(out.String(), `"checks": []`) {
		t.Errorf("no checks is [], never null:\n%s", &out)
	}
	if OK.String() != "ok" || Warn.String() != "warn" || Fail.String() != "fail" {
		t.Error("level names")
	}
}
