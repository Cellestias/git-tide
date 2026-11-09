package history

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/testutil"
)

const paulo = "Paulo Silva <paulo@example.com>"

// setup returns a repo on feature/x: the initial commit and "feat: mine" by the
// configured user, then "fix: theirs" by Paulo, which touches rotation.py.
func setup(t *testing.T) (*repository.Repository, string) {
	t.Helper()
	dir := testutil.NewRepo(t)
	testutil.Run(t, dir, "checkout", "-q", "-b", "feature/x")
	testutil.WriteFile(t, dir, "mine.txt", "m\n")
	testutil.Commit(t, dir, "feat: mine")
	testutil.WriteFile(t, dir, "src/rotation.py", "1\n")
	testutil.Run(t, dir, "add", "-A")
	testutil.Run(t, dir, "commit", "-q", "--author="+paulo, "-m", "fix: theirs")
	return repository.New(executor.Exec{}, dir), dir
}

func run(repo *repository.Repository, dir string, opts Options) (string, error) {
	if opts.Cwd == "" {
		opts.Cwd = dir
	}
	var out, errOut bytes.Buffer
	err := Run(context.Background(), repo, domain.Default(), opts, output.Printer{Out: &out, Err: &errOut})
	return out.String(), err
}

func TestDefaultListsOnlyWhatTheBranchAddsToItsBase(t *testing.T) {
	repo, dir := setup(t)
	out, err := run(repo, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.Contains(lines[1], "fix: theirs") || !strings.Contains(lines[2], "feat: mine") || strings.Contains(out, "initial commit") {
		t.Fatalf("table:\n%s", out)
	}
	if !strings.Contains(lines[1], "Paulo Silva") {
		t.Errorf("author missing:\n%s", out)
	}
}

func TestAllShowsInheritedHistoryToo(t *testing.T) {
	repo, dir := setup(t)
	out, err := run(repo, dir, Options{All: true})
	if err != nil || !strings.Contains(out, "feat: initial commit") {
		t.Fatalf("--all: err=%v\n%s", err, out)
	}
}

func TestBaseBranchItselfShowsWholeHistory(t *testing.T) {
	repo, dir := setup(t)
	testutil.Run(t, dir, "checkout", "-q", "main")
	out, err := run(repo, dir, Options{})
	if err != nil || !strings.Contains(out, "feat: initial commit") {
		t.Fatalf("on the base: err=%v\n%s", err, out)
	}
}

func TestBaseFlagChangesWhatIsSubtracted(t *testing.T) {
	repo, dir := setup(t)
	testutil.Run(t, dir, "branch", "mid", "HEAD~1") // has "feat: mine"
	out, err := run(repo, dir, Options{Base: "mid"})
	if err != nil || strings.Contains(out, "feat: mine") || !strings.Contains(out, "fix: theirs") {
		t.Fatalf("--base mid: err=%v\n%s", err, out)
	}
	if _, err := run(repo, dir, Options{Base: "nope"}); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("unknown --base: err = %v", err)
	}
}

func TestMergesAreHiddenUnlessAsked(t *testing.T) {
	repo, dir := setup(t)
	testutil.Run(t, dir, "checkout", "-q", "-b", "side", "main")
	testutil.WriteFile(t, dir, "side.txt", "s\n")
	testutil.Commit(t, dir, "feat: side")
	testutil.Run(t, dir, "checkout", "-q", "feature/x")
	testutil.Run(t, dir, "merge", "-q", "--no-ff", "-m", "merge: side into x", "side")

	out, err := run(repo, dir, Options{})
	if err != nil || strings.Contains(out, "merge: side") || !strings.Contains(out, "feat: side") {
		t.Fatalf("default:\n%s", out)
	}
	out, err = run(repo, dir, Options{Merges: true})
	if err != nil || !strings.Contains(out, "merge: side") {
		t.Fatalf("--merges:\n%s", out)
	}
}

func TestSinceAndUntil(t *testing.T) {
	repo, dir := setup(t)
	out, err := run(repo, dir, Options{Since: "1d"})
	if err != nil || !strings.Contains(out, "fix: theirs") {
		t.Fatalf("--since 1d:\n%s", out)
	}
	out, err = run(repo, dir, Options{Until: "2w"})
	if err != nil || !strings.Contains(out, "No commits match") {
		t.Fatalf("--until 2w:\n%s", out)
	}
	if _, err := run(repo, dir, Options{Since: "--all"}); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("option-like --since: err = %v", err)
	}
}

func TestGitDate(t *testing.T) {
	for in, want := range map[string]string{"7d": "7 days ago", "1w": "1 week ago", "3m": "3 months ago", "12h": "12 hours ago", "2026-01-31": "2026-01-31", "": ""} {
		if got, err := gitDate("--since", in); err != nil || got != want {
			t.Errorf("gitDate(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestJSON(t *testing.T) {
	repo, dir := setup(t)
	out, err := run(repo, dir, Options{JSON: true, Member: "paulo"})
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(got) != 1 || got[0]["subject"] != "fix: theirs" || got[0]["email"] != "paulo@example.com" {
		t.Fatalf("json = %v", got)
	}
	files, _ := got[0]["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["path"] != "src/rotation.py" || files[0].(map[string]any)["added"] != float64(1) {
		t.Fatalf("files = %v", got[0]["files"])
	}
	// A commit without file changes still has "files": [] and never null.
	testutil.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "chore: empty")
	out, err = run(repo, dir, Options{JSON: true, Limit: 1})
	if err != nil || !strings.Contains(out, `"files": []`) {
		t.Fatalf("empty commit: err=%v\n%s", err, out)
	}
	out, err = run(repo, dir, Options{JSON: true, Member: "nobody"})
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("no match should be []: err=%v %q", err, out)
	}
}

func TestMeKeepsOnlyMyCommits(t *testing.T) {
	repo, dir := setup(t)
	out, err := run(repo, dir, Options{Me: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "theirs") || !strings.Contains(out, "feat: mine") {
		t.Fatalf("--me:\n%s", out)
	}
}

func TestMemberMatchesNameOrEmailIgnoringCase(t *testing.T) {
	repo, dir := setup(t)
	for _, m := range []string{"paulo", "PAULO@example.com", "silva"} {
		out, err := run(repo, dir, Options{Member: m})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "fix: theirs") || strings.Contains(out, "feat: mine") {
			t.Errorf("--member %q:\n%s", m, out)
		}
	}
}

func TestBranchReadsAnotherBranch(t *testing.T) {
	repo, dir := setup(t)
	testutil.Run(t, dir, "checkout", "-q", "main")
	out, err := run(repo, dir, Options{Branch: "feature/x", Member: "paulo"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "fix: theirs") {
		t.Fatalf("--branch:\n%s", out)
	}
}

func TestFileShowsCommitsThatTouchedItFollowingRenames(t *testing.T) {
	repo, dir := setup(t)
	testutil.Run(t, dir, "mv", "src/rotation.py", "src/rotation_keys.py")
	testutil.Commit(t, dir, "refactor: rename")
	// Relative to a subdirectory, as typed in a shell there.
	out, err := run(repo, dir, Options{File: "rotation_keys.py", Cwd: dir + "/src"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "refactor: rename") || !strings.Contains(out, "fix: theirs") || !strings.Contains(out, "Paulo Silva") || strings.Contains(out, "feat: mine") {
		t.Fatalf("-f:\n%s", out)
	}
}

func TestFileAcceptsADirectory(t *testing.T) {
	repo, dir := setup(t)
	out, err := run(repo, dir, Options{File: "src"})
	if err != nil || !strings.Contains(out, "fix: theirs") || strings.Contains(out, "feat: mine") {
		t.Fatalf("-f src:\n%s", out)
	}
}

func TestLimit(t *testing.T) {
	repo, dir := setup(t)
	out, err := run(repo, dir, Options{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Split(strings.TrimSpace(out), "\n")); n != 2 {
		t.Fatalf("rows = %d, want header + 1:\n%s", n, out)
	}
}

func TestRemoteReadsFetchedBranch(t *testing.T) {
	clone, bare := testutil.NewRepoWithRemote(t)
	testutil.Run(t, clone, "checkout", "-q", "-b", "feature/x")
	testutil.WriteFile(t, clone, "a.txt", "a\n")
	testutil.Commit(t, clone, "feat: pushed")
	testutil.Run(t, clone, "push", "-q", "-u", "origin", "feature/x")

	// A teammate pushes to the branch; the clone has not fetched it yet.
	other := t.TempDir()
	testutil.Run(t, other, "clone", "-q", "-b", "feature/x", bare, ".")
	testutil.WriteFile(t, other, "b.txt", "b\n")
	testutil.Run(t, other, "add", "-A")
	testutil.Run(t, other, "commit", "-q", "--author="+paulo, "-m", "feat: from remote")
	testutil.Run(t, other, "push", "-q")

	repo := repository.New(executor.Exec{}, clone)
	local, err := run(repo, clone, Options{})
	if err != nil || strings.Contains(local, "from remote") {
		t.Fatalf("local view: err=%v\n%s", err, local)
	}
	out, err := run(repo, clone, Options{Remote: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "feat: from remote") {
		t.Fatalf("--remote:\n%s", out)
	}
}

func TestErrors(t *testing.T) {
	repo, dir := setup(t)
	cases := map[string]struct {
		opts Options
		code int
	}{
		"me with member":  {Options{Me: true, Member: "x"}, apperr.CodeUsage},
		"negative limit":  {Options{Limit: -1}, apperr.CodeUsage},
		"unknown branch":  {Options{Branch: "nope"}, apperr.CodeUsage},
		"bad branch name": {Options{Branch: "-x"}, apperr.CodeUsage},
		"outside file":    {Options{File: "../x"}, apperr.CodeUsage},
		"no remote":       {Options{Remote: true}, apperr.CodeConfig},
	}
	for name, c := range cases {
		if _, err := run(repo, dir, c.opts); apperr.CodeOf(err) != c.code {
			t.Errorf("%s: err = %v, want code %d", name, err, c.code)
		}
	}
}

func TestMeNeedsAnEmail(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null") // the machine's own user.email must not leak in
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	repo, dir := setup(t)
	testutil.Run(t, dir, "config", "--unset", "user.email")
	if _, err := run(repo, dir, Options{Me: true}); apperr.CodeOf(err) != apperr.CodeConfig {
		t.Fatalf("err = %v, want config error", err)
	}
}

func TestShow(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "a.txt", "1\n2\n")
	testutil.Run(t, dir, "add", "-A")
	testutil.Run(t, dir, "commit", "-q", "--author="+paulo, "-m", "feat: detail", "-m", "the body text")

	var out, errOut bytes.Buffer
	if err := Show(context.Background(), repo, "HEAD", false, output.Printer{Out: &out, Err: &errOut}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Paulo Silva <paulo@example.com>", "feat: detail", "the body text", "a.txt", "+2 -0", "1 file changed, +2 -0"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := Show(context.Background(), repo, "HEAD", true, output.Printer{Out: &out, Err: &errOut}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Hash, Subject, Body string
		Files               []struct {
			Path  string
			Added int
		}
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || len(got.Hash) != 40 || got.Body != "the body text" || len(got.Files) != 1 || got.Files[0].Added != 2 {
		t.Fatalf("json: %v\n%s", err, out.String())
	}

	for _, bad := range []string{"nope", "--all", ""} {
		if err := Show(context.Background(), repo, bad, false, output.Printer{Out: &out, Err: &errOut}); apperr.CodeOf(err) != apperr.CodeUsage {
			t.Errorf("Show(%q): err = %v, want usage error", bad, err)
		}
	}
}

func TestNoMatchIsNotAnError(t *testing.T) {
	repo, dir := setup(t)
	out, err := run(repo, dir, Options{Member: "nobody"})
	if err != nil || !strings.Contains(out, "No commits match") {
		t.Fatalf("err=%v\n%s", err, out)
	}
}
