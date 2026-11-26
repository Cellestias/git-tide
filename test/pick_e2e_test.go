package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/testutil"
)

// pickRepo is a repository with a remote, a source branch holding two commits
// (a.txt, b.txt) and a working branch feature/dst cut from main and checked out.
func pickRepo(t *testing.T) (dir, bare string) {
	t.Helper()
	dir, bare = configured(t)
	git(t, dir, "checkout", "-q", "-b", "feature/src")
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	testutil.Commit(t, dir, "feat: add a")
	testutil.WriteFile(t, dir, "b.txt", "b\n")
	testutil.Commit(t, dir, "feat: add b")
	git(t, dir, "checkout", "-q", "main")
	git(t, dir, "checkout", "-q", "-b", "feature/dst")
	return dir, bare
}

// configured is dryRepo with the files of `gtide init` committed on main, so
// every branch cut from it keeps its .tide.yml.
func configured(t *testing.T) (dir, bare string) {
	t.Helper()
	dir, bare = dryRepo(t)
	testutil.Commit(t, dir, "chore: add the tide config")
	return dir, bare
}

func headOf(t *testing.T, dir string) string { return testutil.Run(t, dir, "rev-parse", "HEAD") }

func TestPickCopiesCommitsAndSkipsWhatIsThere(t *testing.T) {
	dir, _ := pickRepo(t)

	out, code := gtide(t, dir, "pick", "main..feature/src")
	if code != 0 || !strings.Contains(out, "Picked 2 commits") {
		t.Fatalf("pick: %d\n%s", code, out)
	}
	if got := testutil.Run(t, dir, "log", "--format=%s", "-2"); got != "feat: add b\nfeat: add a" {
		t.Errorf("subjects = %q", got)
	}
	for _, f := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s was not brought: %v", f, err)
		}
	}

	// the same commits again: they are here under other hashes, so nothing is done
	before := headOf(t, dir)
	out, code = gtide(t, dir, "pick", "main..feature/src")
	if code != 0 || !strings.Contains(out, "Nothing to pick") || headOf(t, dir) != before {
		t.Fatalf("second pick: %d\n%s", code, out)
	}
}

func TestPickJSON(t *testing.T) {
	dir, _ := pickRepo(t)
	stdout, stderr, code := both(t, dir, nil, "pick", "feature/src", "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	var doc struct {
		OK      bool   `json:"ok"`
		Action  string `json:"action"`
		Details struct {
			Picked  []struct{ Commit, Subject string } `json:"picked"`
			Skipped []any                              `json:"skipped"`
		} `json:"details"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if !doc.OK || doc.Action != "pick" || len(doc.Details.Picked) != 1 || doc.Details.Picked[0].Subject != "feat: add b" || doc.Details.Skipped == nil {
		t.Errorf("doc = %+v", doc)
	}
}

func TestPickRefusals(t *testing.T) {
	dir, _ := pickRepo(t)
	git(t, dir, "checkout", "-q", "feature/src")
	testutil.WriteFile(t, dir, "c.txt", "c\n")
	testutil.Commit(t, dir, "added c without a type")
	fake := "AKIA" + strings.Repeat("B", 16)
	testutil.WriteFile(t, dir, "creds.txt", "id="+fake+"\n")
	testutil.Commit(t, dir, "feat: add creds")
	git(t, dir, "checkout", "-q", "feature/dst")
	before := headOf(t, dir)

	if out, code := gtide(t, dir, "pick", "feature/src~1"); code != 4 || !strings.Contains(out, "Conventional Commits") {
		t.Errorf("a bad message: %d\n%s", code, out)
	}
	out, code := gtide(t, dir, "pick", "feature/src")
	if code != 4 || !strings.Contains(out, "creds.txt:1 (aws_access_key_id)") || strings.Contains(out, fake) {
		t.Errorf("a secret: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "pick", "no-such-branch"); code != 2 {
		t.Errorf("an unknown commit: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "pick", "--bad"); code != 2 {
		t.Errorf("an option: %d\n%s", code, out)
	}
	if headOf(t, dir) != before {
		t.Error("a refused pick moved HEAD")
	}

	testutil.WriteFile(t, dir, "README.md", "changed\n")
	if out, code := gtide(t, dir, "pick", "feature/src~2"); code != 6 || !strings.Contains(out, "uncommitted changes") {
		t.Errorf("a dirty tree: %d\n%s", code, out)
	}
	git(t, dir, "checkout", "--", "README.md")

	git(t, dir, "checkout", "-q", "main")
	if out, code := gtide(t, dir, "pick", "feature/src~2"); code != 4 || !strings.Contains(out, "protected") {
		t.Errorf("a protected branch: %d\n%s", code, out)
	}
}

func TestPickRefusesAMergeCommit(t *testing.T) {
	dir, _ := pickRepo(t)
	git(t, dir, "merge", "--no-ff", "-m", "feat: merge src", "feature/src")
	git(t, dir, "checkout", "-q", "-b", "feature/other", "main")
	if out, code := gtide(t, dir, "pick", "feature/dst"); code != 6 || !strings.Contains(out, "merge commit") {
		t.Errorf("a merge commit: %d\n%s", code, out)
	}
}

func TestPickConflictContinueAndAbort(t *testing.T) {
	dir, _ := configured(t)
	git(t, dir, "checkout", "-q", "-b", "feature/src")
	testutil.WriteFile(t, dir, "f.txt", "from src\n")
	testutil.Commit(t, dir, "feat: src f")
	git(t, dir, "checkout", "-q", "main")
	git(t, dir, "checkout", "-q", "-b", "feature/dst")
	testutil.WriteFile(t, dir, "f.txt", "from dst\n")
	testutil.Commit(t, dir, "feat: dst f")
	before := headOf(t, dir)

	out, code := gtide(t, dir, "pick", "feature/src")
	if code != 1 || !strings.Contains(out, "f.txt") || !strings.Contains(out, "gtide continue") {
		t.Fatalf("pick with a conflict: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "abort", "--yes"); code != 0 || headOf(t, dir) != before {
		t.Fatalf("abort: %d\n%s", code, out)
	}

	if _, code := gtide(t, dir, "pick", "feature/src"); code != 1 {
		t.Fatal("the second pick should conflict too")
	}
	testutil.WriteFile(t, dir, "f.txt", "resolved\n")
	git(t, dir, "add", "f.txt")
	if out, code := gtide(t, dir, "continue"); code != 0 || !strings.Contains(out, "Finished the cherry-pick") {
		t.Fatalf("continue: %d\n%s", code, out)
	}
	if got := testutil.Run(t, dir, "log", "-1", "--format=%s"); got != "feat: src f" {
		t.Errorf("subject = %q", got)
	}
}

func TestPickDryRunChangesNothing(t *testing.T) {
	dir, _ := pickRepo(t)
	before := headOf(t, dir)
	out, code := gtide(t, dir, "pick", "main..feature/src", "--dry-run")
	if code != 0 || !strings.Contains(out, "cherry-pick") || headOf(t, dir) != before {
		t.Fatalf("dry run: %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err == nil {
		t.Error("a dry run brought a.txt")
	}
}

func TestRevertWorksOnAPublishedCommit(t *testing.T) {
	dir, _ := pickRepo(t)
	if out, code := gtide(t, dir, "pick", "feature/src"); code != 0 {
		t.Fatalf("pick: %d\n%s", code, out)
	}
	git(t, dir, "push", "-q", "-u", "origin", "feature/dst")

	if out, code := gtide(t, dir, "undo"); code != 4 {
		t.Fatalf("undo of a published commit: %d\n%s", code, out)
	}
	out, code := gtide(t, dir, "revert", "HEAD")
	if code != 0 || !strings.Contains(out, "Reverted 1 commit") {
		t.Fatalf("revert: %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); err == nil {
		t.Error("b.txt is still there")
	}
	if got := testutil.Run(t, dir, "log", "-1", "--format=%s"); !strings.HasPrefix(got, "Revert ") {
		t.Errorf("subject = %q", got)
	}
	if out, code := gtide(t, dir, "push"); code != 0 {
		t.Fatalf("the revert should push without force: %d\n%s", code, out)
	}
}

func TestRevertRefusals(t *testing.T) {
	dir, _ := pickRepo(t)
	if out, code := gtide(t, dir, "revert", "feature/src"); code != 2 || !strings.Contains(out, "is not in feature/dst") {
		t.Errorf("a commit of another branch: %d\n%s", code, out)
	}
	git(t, dir, "checkout", "-q", "main")
	if out, code := gtide(t, dir, "revert", "HEAD"); code != 4 {
		t.Errorf("a protected branch: %d\n%s", code, out)
	}
}

func TestRestoreBringsFilesAndStagesThem(t *testing.T) {
	dir, _ := pickRepo(t)
	testutil.WriteFile(t, dir, "mine.txt", "mine\n")

	out, code := gtide(t, dir, "restore", "--from", "feature/src", "-f", "a.txt", "-f", "b.txt")
	if code != 0 || !strings.Contains(out, "staged") {
		t.Fatalf("restore: %d\n%s", code, out)
	}
	if got := read(t, filepath.Join(dir, "a.txt")); got != "a\n" {
		t.Errorf("a.txt = %q", got)
	}
	if got := testutil.Run(t, dir, "diff", "--cached", "--name-only"); got != "a.txt\nb.txt" {
		t.Errorf("staged = %q", got)
	}
	if got := testutil.Run(t, dir, "status", "--porcelain", "mine.txt"); got != "?? mine.txt" {
		t.Errorf("a file that was not asked for changed: %q", got)
	}
	if out, code := gtide(t, dir, "save", "-m", "feat: bring a and b"); code != 0 {
		t.Fatalf("save: %d\n%s", code, out)
	}
}

func TestRestoreAsksBeforeOverwriting(t *testing.T) {
	dir, _ := pickRepo(t)
	git(t, dir, "checkout", "-q", "feature/src")
	testutil.WriteFile(t, dir, "a.txt", "a changed on src\n")
	testutil.Commit(t, dir, "feat: change a")
	git(t, dir, "checkout", "-q", "feature/dst")
	testutil.WriteFile(t, dir, "a.txt", "local work\n")
	bin := filepath.Join(binDir, "gtide")

	if out, code := runEnv(t, dir, nil, "n\n", bin, "restore", "--from", "feature/src", "-f", "a.txt"); code != 1 || !strings.Contains(out, "aborted") {
		t.Fatalf("answer no: %d\n%s", code, out)
	}
	if got := read(t, filepath.Join(dir, "a.txt")); got != "local work\n" {
		t.Fatalf("a.txt = %q; it must stay as it was", got)
	}
	if out, code := gtide(t, dir, "restore", "--from", "feature/src", "-f", "a.txt", "--yes"); code != 0 {
		t.Fatalf("--yes: %d\n%s", code, out)
	}
	if got := read(t, filepath.Join(dir, "a.txt")); got != "a changed on src\n" {
		t.Errorf("a.txt = %q", got)
	}
}

func TestRestoreRefusals(t *testing.T) {
	dir, _ := pickRepo(t)
	git(t, dir, "checkout", "-q", "feature/src")
	fake := "AKIA" + strings.Repeat("B", 16)
	testutil.WriteFile(t, dir, "creds.txt", "id="+fake+"\n")
	testutil.Commit(t, dir, "feat: add creds")
	git(t, dir, "checkout", "-q", "feature/dst")

	out, code := gtide(t, dir, "restore", "--from", "feature/src", "-f", "creds.txt")
	if code != 4 || !strings.Contains(out, "creds.txt:1 (aws_access_key_id)") || strings.Contains(out, fake) {
		t.Errorf("a secret: %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "creds.txt")); err == nil {
		t.Error("the file with the secret was written")
	}
	if got := testutil.Run(t, dir, "diff", "--cached", "--name-only"); got != "" {
		t.Errorf("something stayed staged: %q", got)
	}
	if out, code := gtide(t, dir, "restore", "--from", "feature/src", "-f", "nope.txt"); code != 2 || !strings.Contains(out, "has no nope.txt") {
		t.Errorf("a missing file: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "restore", "-f", "a.txt"); code != 2 {
		t.Errorf("no --from: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "restore", "--from", "feature/src"); code != 2 {
		t.Errorf("no file: %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "restore", "--from", "feature/src", "-f", "../outside"); code != 2 {
		t.Errorf("a path outside: %d\n%s", code, out)
	}
	git(t, dir, "checkout", "-q", "main")
	if out, code := gtide(t, dir, "restore", "--from", "feature/src", "-f", "a.txt"); code != 4 {
		t.Errorf("a protected branch: %d\n%s", code, out)
	}
}

func TestFetchRefreshesOnlyTheRemoteBranches(t *testing.T) {
	dir, bare := configured(t)
	git(t, dir, "checkout", "-q", "-b", "feature/x")
	git(t, dir, "push", "-q", "-u", "origin", "feature/x")

	other := t.TempDir()
	testutil.Run(t, other, "clone", "-q", bare, ".")
	testutil.Run(t, other, "config", "user.name", "O")
	testutil.Run(t, other, "config", "user.email", "o@example.invalid")
	testutil.Run(t, other, "checkout", "-q", "feature/x")
	testutil.WriteFile(t, other, "new.txt", "new\n")
	testutil.Commit(t, other, "feat: new")
	testutil.Run(t, other, "push", "-q", "origin", "feature/x")

	testutil.WriteFile(t, dir, "wip.txt", "work in progress\n")
	before := headOf(t, dir)
	stdout, stderr, code := both(t, dir, nil, "fetch", "--json")
	if code != 0 {
		t.Fatalf("fetch: %d\n%s\n%s", code, stdout, stderr)
	}
	var doc struct {
		Action  string `json:"action"`
		Details struct {
			Remote   string `json:"remote"`
			Upstream string `json:"upstream"`
			Behind   int    `json:"behind"`
		} `json:"details"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if doc.Action != "fetch" || doc.Details.Remote != "origin" || doc.Details.Upstream != "origin/feature/x" || doc.Details.Behind != 1 {
		t.Errorf("doc = %+v", doc)
	}
	if headOf(t, dir) != before {
		t.Error("fetch moved HEAD")
	}
	if got := testutil.Run(t, dir, "status", "--porcelain"); got != "?? wip.txt" {
		t.Errorf("work tree = %q", got)
	}
	if out, code := gtide(t, dir, "fetch", "nowhere"); code != 3 {
		t.Errorf("an unknown remote: %d\n%s", code, out)
	}
}
