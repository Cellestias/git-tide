package history

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
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
	if err := Show(context.Background(), repo, "HEAD", false, false, output.Printer{Out: &out, Err: &errOut}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Paulo Silva <paulo@example.com>", "feat: detail", "the body text", "a.txt", "+2 -0", "1 file changed, +2 -0"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := Show(context.Background(), repo, "HEAD", true, false, output.Printer{Out: &out, Err: &errOut}); err != nil {
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
		if err := Show(context.Background(), repo, bad, false, false, output.Printer{Out: &out, Err: &errOut}); apperr.CodeOf(err) != apperr.CodeUsage {
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

func TestSearchMatchesTheMessage(t *testing.T) {
	repo, dir := setup(t)
	out, err := run(repo, dir, Options{Search: "THEIRS"}) // case-insensitive
	if err != nil || !strings.Contains(out, "fix: theirs") || strings.Contains(out, "feat: mine") {
		t.Fatalf("--search:\n%s", out)
	}
	if out, err = run(repo, dir, Options{Search: "fix."}); err != nil || !strings.Contains(out, "No commits match") {
		t.Fatalf("the text is literal, not a regex:\n%s", out) // "fix." must not match "fix:"
	}
}

func TestTypeAndScopeReadTheConventionalPrefix(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "a.txt", "a\n")
	testutil.Commit(t, dir, "fix(auth)!: reject expired tokens")
	testutil.WriteFile(t, dir, "b.txt", "b\n")
	testutil.Commit(t, dir, "feat(auth): add login")
	testutil.WriteFile(t, dir, "c.txt", "c\n")
	testutil.Commit(t, dir, "chore: bump deps\n\nfix: only in the body")

	out, err := run(repo, dir, Options{Type: "fix"})
	if err != nil || !strings.Contains(out, "reject expired") || !strings.Contains(out, "fix: theirs") ||
		strings.Contains(out, "add login") || strings.Contains(out, "bump deps") {
		t.Fatalf("--type fix:\n%s", out)
	}
	out, err = run(repo, dir, Options{Type: "FIX", Scope: "auth"})
	if err != nil || strings.Count(out, "\n") != 2 || !strings.Contains(out, "reject expired") {
		t.Fatalf("--type fix --scope auth:\n%s", out)
	}
	if out, err = run(repo, dir, Options{Scope: "auth", Limit: 1}); err != nil || !strings.Contains(out, "add login") || strings.Contains(out, "reject expired") {
		t.Fatalf("--limit applies after the filter:\n%s", out)
	}
	if _, err := run(repo, dir, Options{Type: "fe at"}); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("bad --type: err = %v", err)
	}
}

func TestRangeListsWhatTheEndHasAndTheStartDoesNot(t *testing.T) {
	repo, dir := setup(t)
	testutil.Run(t, dir, "tag", "v1", "HEAD~1")
	testutil.Run(t, dir, "tag", "v2", "HEAD")
	out, err := run(repo, dir, Options{Range: "v1..v2"})
	if err != nil || !strings.Contains(out, "fix: theirs") || strings.Contains(out, "feat: mine") {
		t.Fatalf("v1..v2:\n%s", out)
	}
	for _, bad := range []string{"v1", "v1..", "..v2", "-x..v2", "v1...v2", "v1..nope"} {
		if _, err := run(repo, dir, Options{Range: bad}); apperr.CodeOf(err) != apperr.CodeUsage {
			t.Errorf("range %q: err = %v", bad, err)
		}
	}
	if _, err := run(repo, dir, Options{Range: "v1..v2", Branch: "main"}); apperr.CodeOf(err) != apperr.CodeUsage {
		t.Errorf("range with --branch: err = %v", err)
	}
}

func TestSummaryCountsPerAuthor(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "x.txt", "x\n")
	testutil.Run(t, dir, "add", "-A")
	testutil.Run(t, dir, "commit", "-q", "--author="+paulo, "-m", "fix: more")

	out, err := run(repo, dir, Options{Summary: true, Limit: 1}) // --limit is ignored
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.Contains(lines[1], "Paulo Silva") || !strings.HasPrefix(strings.TrimSpace(lines[1]), "2") || !strings.Contains(lines[2], "Git Tide Test") {
		t.Fatalf("--summary:\n%s", out)
	}
	out, err = run(repo, dir, Options{Summary: true, JSON: true})
	var got []map[string]any
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || len(got) != 2 || got[0]["commits"] != float64(2) || got[0]["email"] != "paulo@example.com" {
		t.Fatalf("--summary --json: err=%v\n%s", err, out)
	}
}

func TestCompareRejectsTheNewFilters(t *testing.T) {
	for _, o := range []Options{{Search: "x"}, {Type: "feat"}, {Scope: "a"}, {Summary: true}, {AllBranches: true}, {Graph: true}, {Patch: true}} {
		if len(incompatible(o)) != 1 {
			t.Errorf("%+v is not rejected by --compare", o)
		}
	}
}

func TestPatchShowsWhatEachCommitChangedOnTheFile(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "src/rotation.py", "1\n2\n")
	testutil.Commit(t, dir, "feat: second line")
	out, err := run(repo, dir, Options{File: "src/rotation.py", Patch: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"feat: second line", "fix: theirs", "+2", "+1", "diff --git a/src/rotation.py"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "mine.txt") {
		t.Errorf("another file leaked into the file's patch:\n%s", out)
	}
}

func TestPatchFollowsARenameAndStaysOnTheFile(t *testing.T) {
	repo, dir := setup(t)
	testutil.Run(t, dir, "mv", "src/rotation.py", "src/rotation_keys.py")
	testutil.Commit(t, dir, "refactor: rename")
	out, err := run(repo, dir, Options{File: "src/rotation_keys.py", Patch: true, JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Subject string  `json:"subject"`
		Patch   *string `json:"patch"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(got) != 2 || got[0].Subject != "refactor: rename" || got[1].Subject != "fix: theirs" {
		t.Fatalf("commits = %+v", got)
	}
	if got[1].Patch == nil || !strings.Contains(*got[1].Patch, "+1") || !strings.Contains(*got[1].Patch, "src/rotation.py") {
		t.Fatalf("patch before the rename = %v", got[1].Patch)
	}
}

func TestPatchKeepsTabsAndRecordSeparatorsInsideTheDiff(t *testing.T) {
	repo, dir := setup(t)
	// A tab-separated line looks like a numstat line; \x1e is the usual record separator.
	testutil.WriteFile(t, dir, "data.tsv", "1\t2\t3\n\x1efake record\n")
	testutil.Commit(t, dir, "feat: data")
	out, err := run(repo, dir, Options{File: "data.tsv", Patch: true, JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Files []struct{ Path string } `json:"files"`
		Patch string                  `json:"patch"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(got) != 1 || len(got[0].Files) != 1 || got[0].Files[0].Path != "data.tsv" || !strings.Contains(got[0].Patch, "+1\t2\t3") {
		t.Fatalf("got = %+v", got)
	}
}

func TestPatchIsCutWhenHuge(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "big.txt", strings.Repeat("0123456789abcdef\n", 30000))
	testutil.Commit(t, dir, "feat: big")
	out, err := run(repo, dir, Options{File: "big.txt", Patch: true, JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Patch     string `json:"patch"`
		Truncated bool   `json:"patch_truncated"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 1 {
		t.Fatalf("err=%v out=%d bytes", err, len(out))
	}
	if !got[0].Truncated || len(got[0].Patch) > repository.MaxPatchBytes || !strings.HasSuffix(got[0].Patch, "\n") {
		t.Errorf("truncated=%v len=%d", got[0].Truncated, len(got[0].Patch))
	}
}

func TestPatchRejectsSummary(t *testing.T) {
	repo, dir := setup(t)
	_, err := run(repo, dir, Options{Patch: true, Summary: true})
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeUsage {
		t.Fatalf("err = %v", err)
	}
}

func TestJSONHasNoPatchFieldWithoutTheFlag(t *testing.T) {
	repo, dir := setup(t)
	out, err := run(repo, dir, Options{JSON: true})
	if err != nil || strings.Contains(out, `"patch"`) {
		t.Fatalf("err=%v\n%s", err, out)
	}
}

// graphSetup: main <- feature/x (2 commits) merged with side, a tag, a
// remote-tracking ref and another branch that nothing merged.
func graphSetup(t *testing.T) (*repository.Repository, string) {
	t.Helper()
	repo, dir := setup(t)
	testutil.Run(t, dir, "checkout", "-q", "-b", "side", "main")
	testutil.WriteFile(t, dir, "side.txt", "s\n")
	testutil.Commit(t, dir, "feat: side")
	testutil.Run(t, dir, "checkout", "-q", "-b", "elsewhere", "main")
	testutil.WriteFile(t, dir, "else.txt", "e\n")
	testutil.Commit(t, dir, "feat: elsewhere")
	testutil.Run(t, dir, "checkout", "-q", "feature/x")
	testutil.Run(t, dir, "merge", "-q", "--no-ff", "-m", "merge: side into x", "side")
	testutil.Run(t, dir, "tag", "v1.0.0")
	testutil.Run(t, dir, "update-ref", "refs/remotes/origin/main", "main")
	testutil.Run(t, dir, "update-ref", "refs/remotes/origin/HEAD", "main")
	return repo, dir
}

type graphCommit struct {
	Hash     string   `json:"hash"`
	FullHash string   `json:"full_hash"`
	Subject  string   `json:"subject"`
	Parents  []string `json:"parents"`
	Refs     []struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Current bool   `json:"current"`
	} `json:"refs"`
}

func readGraph(t *testing.T, out string) []graphCommit {
	t.Helper()
	var got []graphCommit
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	return got
}

func TestGraphGivesParentsRefsAndAParentsLastOrder(t *testing.T) {
	repo, dir := graphSetup(t)
	out, err := run(repo, dir, Options{JSON: true, Graph: true, AllBranches: true, Merges: true, Limit: 0})
	if err != nil {
		t.Fatal(err)
	}
	got := readGraph(t, out)
	bySubject := map[string]graphCommit{}
	position := map[string]int{}
	for i, c := range got {
		bySubject[c.Subject] = c
		position[c.FullHash] = i
		if len(c.FullHash) != 40 || !strings.HasPrefix(c.FullHash, c.Hash) {
			t.Errorf("%s: full hash %q does not extend %q", c.Subject, c.FullHash, c.Hash)
		}
	}
	for _, want := range []string{"feat: initial commit", "feat: mine", "fix: theirs", "feat: side", "feat: elsewhere", "merge: side into x"} {
		if _, ok := bySubject[want]; !ok {
			t.Fatalf("%q missing from %d commits", want, len(got))
		}
	}
	// A parent always comes after its children, which is what a lane layout needs.
	for _, c := range got {
		for _, p := range c.Parents {
			if pos, ok := position[p]; !ok || pos <= position[c.FullHash] {
				t.Errorf("%s: parent %s is at %d, child at %d", c.Subject, p, pos, position[c.FullHash])
			}
		}
	}
	if m := bySubject["merge: side into x"]; len(m.Parents) != 2 {
		t.Errorf("merge parents = %v", m.Parents)
	}
	if root := bySubject["feat: initial commit"]; root.Parents == nil || len(root.Parents) != 0 {
		t.Errorf("root parents = %#v, want []", root.Parents)
	}
	if !strings.Contains(out, `"parents": []`) {
		t.Errorf("an empty parents list must print as [], not null:\n%s", out)
	}

	merge := bySubject["merge: side into x"]
	if len(merge.Refs) != 2 || merge.Refs[0].Name != "feature/x" || merge.Refs[0].Kind != "branch" || !merge.Refs[0].Current || merge.Refs[1].Name != "v1.0.0" || merge.Refs[1].Kind != "tag" {
		t.Errorf("merge refs = %+v", merge.Refs)
	}
	var main []string
	for _, r := range bySubject["feat: initial commit"].Refs {
		main = append(main, r.Kind+":"+r.Name)
	}
	sort.Strings(main)
	if strings.Join(main, " ") != "branch:main remote:origin/main" {
		t.Errorf("main refs = %v (origin/HEAD must be left out)", main)
	}
	if e := bySubject["feat: elsewhere"]; len(e.Refs) != 1 || e.Refs[0].Name != "elsewhere" {
		t.Errorf("elsewhere refs = %+v", e.Refs)
	}
}

func TestAllBranchesReadsWhatNoCurrentBranchHas(t *testing.T) {
	repo, dir := graphSetup(t)
	out, err := run(repo, dir, Options{})
	if err != nil || strings.Contains(out, "feat: elsewhere") {
		t.Fatalf("one branch must not show another's commits: err=%v\n%s", err, out)
	}
	out, err = run(repo, dir, Options{AllBranches: true})
	if err != nil || !strings.Contains(out, "feat: elsewhere") || !strings.Contains(out, "feat: initial commit") {
		t.Fatalf("--all-branches: err=%v\n%s", err, out)
	}
}

func TestGraphDetachedHeadAndRefsDoNotLeakWithoutTheFlag(t *testing.T) {
	repo, dir := graphSetup(t)
	testutil.Run(t, dir, "checkout", "-q", "--detach", "feature/x")
	testutil.Run(t, dir, "branch", "-D", "elsewhere")
	out, err := run(repo, dir, Options{JSON: true, Graph: true, AllBranches: true, Merges: true})
	if err != nil {
		t.Fatal(err)
	}
	var head []string
	for _, c := range readGraph(t, out) {
		for _, r := range c.Refs {
			if r.Kind == "head" {
				head = append(head, c.Subject)
			}
		}
	}
	if len(head) != 1 || head[0] != "merge: side into x" {
		t.Errorf("detached HEAD seen at %v", head)
	}
	out, err = run(repo, dir, Options{JSON: true, AllBranches: true, Merges: true})
	if err != nil || strings.Contains(out, `"parents"`) || strings.Contains(out, `"full_hash"`) || strings.Contains(out, `"refs"`) {
		t.Fatalf("without --graph the JSON must stay as it was: err=%v\n%s", err, out)
	}
}

func TestGraphAndAllBranchesUsageErrors(t *testing.T) {
	repo, dir := graphSetup(t)
	for name, o := range map[string]Options{
		"--graph needs --json":      {Graph: true},
		"--graph with --summary":    {Graph: true, JSON: true, Summary: true},
		"--all-branches + --branch": {AllBranches: true, Branch: "side"},
		"--all-branches + --remote": {AllBranches: true, Remote: true},
		"--all-branches + --base":   {AllBranches: true, Base: "main"},
		"--all-branches + range":    {AllBranches: true, Range: "main..side"},
	} {
		_, err := run(repo, dir, o)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeUsage {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestGraphKeepsTheFiltersWorking(t *testing.T) {
	repo, dir := graphSetup(t)
	out, err := run(repo, dir, Options{JSON: true, Graph: true, AllBranches: true, Member: "paulo"})
	if err != nil {
		t.Fatal(err)
	}
	got := readGraph(t, out)
	if len(got) != 1 || got[0].Subject != "fix: theirs" || len(got[0].Parents) != 1 {
		t.Fatalf("filtered graph = %+v", got)
	}
}

func TestShowPatch(t *testing.T) {
	repo, dir := setup(t)
	show := func(rev string, asJSON bool) string {
		t.Helper()
		var out, errOut bytes.Buffer
		if err := Show(context.Background(), repo, rev, asJSON, true, output.Printer{Out: &out, Err: &errOut}); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	out := show("HEAD", false)
	for _, want := range []string{"fix: theirs", "src/rotation.py", "diff --git a/src/rotation.py", "+1"} {
		if !strings.Contains(out, want) {
			t.Errorf("text missing %q:\n%s", want, out)
		}
	}
	var got struct {
		Subject string  `json:"subject"`
		Patch   *string `json:"patch"`
		Files   []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(show("HEAD", true)), &got); err != nil || got.Patch == nil || !strings.Contains(*got.Patch, "+1") || len(got.Files) != 1 {
		t.Fatalf("json: err=%v got=%+v", err, got)
	}
	// Without the flag the JSON has no patch field, as before.
	var out2, errOut bytes.Buffer
	if err := Show(context.Background(), repo, "HEAD", true, false, output.Printer{Out: &out2, Err: &errOut}); err != nil || strings.Contains(out2.String(), `"patch"`) {
		t.Fatalf("no flag: err=%v\n%s", err, &out2)
	}
	// A tab in the file and a merge commit do not confuse it.
	testutil.WriteFile(t, dir, "d.tsv", "1\t2\t3\n")
	testutil.Commit(t, dir, "feat: tabs")
	if err := json.Unmarshal([]byte(show("HEAD", true)), &got); err != nil || len(got.Files) != 1 || got.Files[0].Path != "d.tsv" || got.Patch == nil || !strings.Contains(*got.Patch, "+1\t2\t3") {
		t.Fatalf("tabs: err=%v got=%+v", err, got)
	}
}
