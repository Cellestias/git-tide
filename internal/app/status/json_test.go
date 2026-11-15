package status

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/testutil"
)

// jsonPrinter keeps stdout apart from stderr: only stdout may hold the JSON.
func jsonPrinter() (output.Printer, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return output.Printer{Out: &out, Err: &errOut}, &out, &errOut
}

type statusDoc struct {
	Branch   string `json:"branch"`
	Detached bool   `json:"detached"`
	Head     string `json:"head"`
	Upstream *struct {
		Name  string `json:"name"`
		Known bool   `json:"known"`
	} `json:"upstream"`
	Base *struct {
		Name   string `json:"name"`
		Ahead  int    `json:"ahead"`
		Behind int    `json:"behind"`
	} `json:"base"`
	Operation string `json:"operation"`
	Clean     bool   `json:"clean"`
	Files     []struct {
		Path      string `json:"path"`
		From      string `json:"from"`
		Index     string `json:"index"`
		Worktree  string `json:"worktree"`
		Staged    bool   `json:"staged"`
		Unstaged  bool   `json:"unstaged"`
		Untracked bool   `json:"untracked"`
		Conflict  bool   `json:"conflict"`
	} `json:"files"`
}

func readStatus(t *testing.T, out *bytes.Buffer) statusDoc {
	t.Helper()
	var d statusDoc
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	return d
}

func TestStatusJSON(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "staged.txt", "s\n")
	testutil.Run(t, dir, "add", "staged.txt")
	testutil.WriteFile(t, dir, "README.md", "changed\n")
	testutil.WriteFile(t, dir, "new.txt", "n\n")
	p, out, _ := jsonPrinter()
	if err := RunJSON(context.Background(), repo, domain.Default(), p); err != nil {
		t.Fatal(err)
	}
	d := readStatus(t, out)
	if d.Branch != "feature/x" || d.Detached || d.Clean || d.Operation != "" || d.Upstream != nil {
		t.Fatalf("header = %+v", d)
	}
	if d.Base == nil || d.Base.Name != "main" || d.Base.Ahead != 0 || d.Base.Behind != 0 {
		t.Fatalf("base = %+v", d.Base)
	}
	byPath := map[string]int{}
	for i, f := range d.Files {
		byPath[f.Path] = i
	}
	if len(d.Files) != 3 {
		t.Fatalf("files = %+v", d.Files)
	}
	if f := d.Files[byPath["staged.txt"]]; !f.Staged || f.Unstaged || f.Index != "A" || f.Worktree != " " {
		t.Errorf("staged.txt = %+v", f)
	}
	if f := d.Files[byPath["README.md"]]; f.Staged || !f.Unstaged || f.Worktree != "M" {
		t.Errorf("README.md = %+v", f)
	}
	if f := d.Files[byPath["new.txt"]]; !f.Untracked || f.Staged || f.Unstaged {
		t.Errorf("new.txt = %+v", f)
	}
}

func TestStatusJSONCleanHasEmptyFilesAndNoNoise(t *testing.T) {
	repo, _ := setup(t)
	p, out, errOut := jsonPrinter()
	if err := RunJSON(context.Background(), repo, domain.Default(), p); err != nil {
		t.Fatal(err)
	}
	if !readStatus(t, out).Clean || !strings.Contains(out.String(), `"files": []`) {
		t.Fatalf("clean:\n%s", out)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr: %s", errOut)
	}
}

func TestStatusJSONUpstreamRenameDetachedAndConflict(t *testing.T) {
	repo, dir := setup(t)
	testutil.Run(t, dir, "mv", "README.md", "READ.md")
	p, out, _ := jsonPrinter()
	if err := RunJSON(context.Background(), repo, domain.Default(), p); err != nil {
		t.Fatal(err)
	}
	d := readStatus(t, out)
	if len(d.Files) != 1 || d.Files[0].Path != "READ.md" || d.Files[0].From != "README.md" {
		t.Fatalf("rename = %+v", d.Files)
	}

	testutil.Run(t, dir, "checkout", "-q", "--detach")
	p, out, _ = jsonPrinter()
	if err := RunJSON(context.Background(), repo, domain.Default(), p); err != nil {
		t.Fatal(err)
	}
	if d := readStatus(t, out); !d.Detached || d.Branch != "" || d.Head == "" || d.Upstream != nil || d.Base != nil {
		t.Fatalf("detached = %+v", d)
	}

	testutil.Run(t, dir, "checkout", "-q", "-f", "feature/x")
	testutil.Run(t, dir, "checkout", "-q", "main")
	testutil.WriteFile(t, dir, "c.txt", "main\n")
	testutil.Commit(t, dir, "feat: main side")
	testutil.Run(t, dir, "checkout", "-q", "feature/x")
	testutil.WriteFile(t, dir, "c.txt", "branch\n")
	testutil.Commit(t, dir, "feat: branch side")
	_, _ = testutil.TryRun(dir, "merge", "main")
	p, out, _ = jsonPrinter()
	if err := RunJSON(context.Background(), repo, domain.Default(), p); err != nil {
		t.Fatal(err)
	}
	d = readStatus(t, out)
	if d.Operation != "merge" || len(d.Files) != 1 || !d.Files[0].Conflict || d.Files[0].Staged || d.Files[0].Unstaged {
		t.Fatalf("conflict = %+v", d)
	}
}

func TestDiffJSON(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "README.md", "hello\nmore\n")
	testutil.WriteFile(t, dir, "other.txt", "o\n")
	testutil.Run(t, dir, "add", "other.txt")

	type doc struct {
		Scope string `json:"scope"`
		Files []struct {
			Path  string `json:"path"`
			Added int    `json:"added"`
		} `json:"files"`
		Patch *string `json:"patch"`
	}
	read := func(opts DiffOptions) doc {
		t.Helper()
		opts.JSON, opts.Cwd = true, dir
		p, out, errOut := jsonPrinter()
		if err := Diff(context.Background(), repo, opts, p); err != nil {
			t.Fatal(err)
		}
		if errOut.Len() != 0 {
			t.Errorf("stderr: %s", errOut)
		}
		var d doc
		if err := json.Unmarshal(out.Bytes(), &d); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		return d
	}
	if d := read(DiffOptions{}); d.Scope != "worktree" || len(d.Files) != 1 || d.Files[0].Path != "README.md" || d.Files[0].Added != 1 || d.Patch != nil {
		t.Errorf("work tree = %+v", d)
	}
	if d := read(DiffOptions{Staged: true}); d.Scope != "staged" || len(d.Files) != 1 || d.Files[0].Path != "other.txt" {
		t.Errorf("staged = %+v", d)
	}
	if d := read(DiffOptions{File: "README.md", Patch: true}); d.Patch == nil || !strings.Contains(*d.Patch, "+more") {
		t.Errorf("patch = %+v", d)
	}
	// No changes is an empty list, not a message that breaks the JSON.
	testutil.Run(t, dir, "checkout", "--", "README.md")
	if d := read(DiffOptions{}); len(d.Files) != 0 {
		t.Errorf("clean = %+v", d)
	}
}
