package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/testutil"
)

// stdout runs gtide and returns only its stdout and exit code. Anything a
// program reading --json must not see (notes, warnings) has to stay on stderr.
func stdout(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), filepath.Join(binDir, "gtide"), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.invalid",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), code
}

// TestJSONOutputEndToEnd is the contract a program (the VS Code extension, an
// AI agent) relies on: every --json command exits 0 and puts valid JSON, and
// nothing else, on stdout; an empty result is [] or an object, never prose.
func TestJSONOutputEndToEnd(t *testing.T) {
	dir, _ := testutil.NewRepoWithRemote(t)
	if out, code := gtide(t, dir, "init", "--no-hooks"); code != 0 {
		t.Fatalf("init: %d\n%s", code, out)
	}
	testutil.Run(t, dir, "config", "user.email", "t@example.invalid")
	if out, code := gtide(t, dir, "create", "feature/x"); code != 0 {
		t.Fatalf("create: %d\n%s", code, out)
	}
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	if out, code := gtide(t, dir, "save", "-m", "feat: a"); code != 0 {
		t.Fatalf("save: %d\n%s", code, out)
	}
	testutil.WriteFile(t, dir, "a.txt", "a\nmore\n")

	type check struct {
		args []string
		kind byte // '{' object or '[' array
	}
	for _, c := range []check{
		{[]string{"status", "--json"}, '{'},
		{[]string{"diff", "--json"}, '{'},
		{[]string{"diff", "--json", "--patch", "-f", "a.txt"}, '{'},
		{[]string{"diff", "--json", "--staged"}, '{'},
		{[]string{"branch", "--json"}, '['},
		{[]string{"history", "--json"}, '['},
		{[]string{"history", "--json", "-f", "a.txt", "--patch"}, '['},
		{[]string{"history", "--json", "--summary"}, '['},
		{[]string{"history", "--json", "--all-branches", "--graph", "--merges"}, '['},
		{[]string{"history", "show", "HEAD", "--json"}, '{'},
		{[]string{"history", "--json", "--compare", "feature/x", "main"}, '{'},
		{[]string{"tag", "--json"}, '['},
		{[]string{"stash", "list", "--json"}, '['},
		{[]string{"secrets", "scan", "--json"}, '{'},
		{[]string{"secrets", "scan", "--history", "--json"}, '{'},
		{[]string{"doctor", "--json"}, '{'},
	} {
		name := strings.Join(c.args, " ")
		out, code := stdout(t, dir, c.args...)
		if code != 0 || !json.Valid([]byte(out)) || !strings.HasPrefix(strings.TrimSpace(out), string(c.kind)) {
			t.Errorf("gtide %s: exit %d, stdout is not a JSON %c:\n%s", name, code, c.kind, out)
		}
	}

	// The graph data names the parents and the refs of every commit, and is opt-in.
	out, _ := stdout(t, dir, "history", "--json", "--all-branches", "--graph", "--merges")
	var graph []struct {
		FullHash string   `json:"full_hash"`
		Parents  []string `json:"parents"`
		Refs     []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"refs"`
	}
	if err := json.Unmarshal([]byte(out), &graph); err != nil || len(graph) < 2 || len(graph[0].FullHash) != 40 {
		t.Fatalf("graph = %+v (err %v)", graph, err)
	}
	if len(graph[0].Parents) != 1 || len(graph[0].Refs) == 0 || graph[0].Refs[0].Name != "feature/x" {
		t.Errorf("newest commit = %+v", graph[0])
	}
	if plain, _ := stdout(t, dir, "history", "--json", "--all-branches"); strings.Contains(plain, "parents") {
		t.Errorf("--graph data leaked into the plain JSON:\n%s", plain)
	}
	if out, code := stdout(t, dir, "history", "--graph"); code != 2 {
		t.Errorf("--graph without --json: exit %d, want 2\n%s", code, out)
	}

	// The file's history carries the change of each commit.
	out, _ = stdout(t, dir, "history", "--json", "-f", "a.txt", "--patch")
	var got []struct {
		Subject string `json:"subject"`
		Patch   string `json:"patch"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 1 || got[0].Subject != "feat: a" || !strings.Contains(got[0].Patch, "+a") {
		t.Errorf("file history = %+v (err %v)", got, err)
	}
}
