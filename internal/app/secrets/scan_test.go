package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/testutil"
)

// The fake secrets are built here, not written out, so this file does not trip the very
// scanner it tests when it is committed.
var (
	githubToken = "gh" + "p_" + strings.Repeat("a1B2", 9) // github_token
	awsKey      = "AK" + "IA" + "ABCDEFGHIJKLMNOP"        // aws_access_key_id
	slackToken  = "xo" + "xb-" + "1234567890-abcdefghij"  // slack_token
)

type out struct{ stdout, stderr bytes.Buffer }

func (o *out) printer() output.Printer { return output.Printer{Out: &o.stdout, Err: &o.stderr} }
func (o *out) all() string             { return o.stdout.String() + o.stderr.String() }

func setup(t *testing.T) (*repository.Repository, string) {
	t.Helper()
	dir := testutil.NewRepo(t)
	return repository.New(executor.Exec{}, dir), dir
}

func scan(t *testing.T, repo *repository.Repository, cfg domain.Config, opts Options) (*out, error) {
	t.Helper()
	var o out
	err := Scan(context.Background(), repo, cfg, opts, o.printer())
	return &o, err
}

type doc struct {
	Disabled bool
	Scanned  int
	Skipped  int
	Files    []struct {
		Path    string
		Line    int
		Rule    string
		Tracked bool
	}
	History []struct {
		Commit  string
		Path    string
		Line    int
		Rule    string
		InFiles bool `json:"in_files"`
	}
	Commits   int
	Truncated bool
}

func scanJSON(t *testing.T, repo *repository.Repository, cfg domain.Config, opts Options) doc {
	t.Helper()
	opts.JSON = true
	o, err := scan(t, repo, cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	var d doc
	if err := json.Unmarshal(o.stdout.Bytes(), &d); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, o.stdout.String())
	}
	return d
}

func TestScanFindsTheFilesAndNeverShowsTheValue(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "src/config.js", "const a = 1;\nconst b = 2;\nconst token = '"+githubToken+"';\n")
	testutil.Commit(t, dir, "feat: config")
	testutil.WriteFile(t, dir, "notes.txt", "key "+awsKey+"\n")                         // not committed, not ignored
	testutil.WriteFile(t, dir, ".gitignore", "secret.local\n")                          // ...
	testutil.WriteFile(t, dir, "secret.local", githubToken+"\n")                        // ignored: not scanned
	testutil.WriteFile(t, dir, "ok.txt", "x = '"+slackToken+"' // tide:allow-secret\n") // allowed on that line

	d := scanJSON(t, repo, domain.Default(), Options{})
	if len(d.Files) != 2 {
		t.Fatalf("findings = %+v", d.Files)
	}
	byPath := map[string]int{}
	for i, f := range d.Files {
		byPath[f.Path] = i
	}
	cfg := d.Files[byPath["src/config.js"]]
	if cfg.Line != 3 || cfg.Rule != "github_token" || !cfg.Tracked {
		t.Errorf("config.js = %+v", cfg)
	}
	notes := d.Files[byPath["notes.txt"]]
	if notes.Line != 1 || notes.Rule != "aws_access_key_id" || notes.Tracked {
		t.Errorf("notes.txt = %+v (not committed)", notes)
	}
	if d.Scanned < 3 {
		t.Errorf("scanned = %d", d.Scanned)
	}

	// no output form carries the value, or even a piece of it
	for _, opts := range []Options{{}, {JSON: true}, {History: true}, {History: true, JSON: true}} {
		o, err := scan(t, repo, domain.Default(), opts)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{githubToken, awsKey, slackToken, githubToken[:12], awsKey[:10]} {
			if strings.Contains(o.all(), secret) {
				t.Errorf("%+v: the output carries the value %q:\n%s", opts, secret[:6]+"…", o.all())
			}
		}
	}
}

func TestScanText(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "a.js", "x = '"+githubToken+"'\n")
	testutil.Commit(t, dir, "feat: a")
	testutil.WriteFile(t, dir, "b.txt", awsKey+"\n")
	o, err := scan(t, repo, domain.Default(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"! 2 likely secrets in the files (1 committed, 1 not committed)",
		"a.js:1", "github_token", "committed",
		"b.txt:1", "aws_access_key_id", "not committed, not ignored",
		"shown          the file, the line and the rule: never the value",
		"tide:allow-secret",
	} {
		if !strings.Contains(o.all(), want) {
			t.Errorf("missing %q in:\n%s", want, o.all())
		}
	}
	if o.stdout.Len() == 0 {
		t.Error("the table goes to stdout")
	}

	// a clean repository says so, and points at --history
	repo2, _ := setup(t)
	o, err = scan(t, repo2, domain.Default(), Options{})
	if err != nil || !strings.Contains(o.all(), "✓ No likely secrets in the 1 file scanned") || !strings.Contains(o.all(), "--history") {
		t.Errorf("err=%v\n%s", err, o.all())
	}
}

func TestScanSkipsWhatItCannotOrShouldNotRead(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "bin.dat", "\x00\x01"+githubToken+"\n") // binary: a NUL byte
	testutil.WriteFile(t, dir, "big.txt", strings.Repeat("x", maxFileSize+10)+githubToken+"\n")
	testutil.WriteFile(t, dir, "long.txt", strings.Repeat("y", maxLineBytes+10)+"\n"+githubToken+"\n") // a line too long to read
	testutil.WriteFile(t, dir, "real.txt", githubToken+"\n")
	if err := os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Skip(err)
	}
	d := scanJSON(t, repo, domain.Default(), Options{})
	if len(d.Files) != 1 || d.Files[0].Path != "real.txt" {
		t.Fatalf("only the regular, readable text file is scanned: %+v", d.Files)
	}
	if d.Skipped != 4 {
		t.Errorf("skipped = %d, want 4 (binary, too big, too long a line, symlink)", d.Skipped)
	}
}

func TestScanHistoryFindsWhatWasAddedAndRemovedLater(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "old.js", "x = '"+githubToken+"'\n")
	testutil.Commit(t, dir, "feat: add a key")
	oldCommit := testutil.Run(t, dir, "rev-parse", "HEAD")
	testutil.WriteFile(t, dir, "old.js", "x = process.env.TOKEN\n")
	testutil.Commit(t, dir, "fix: use the environment") // removed from the files
	testutil.WriteFile(t, dir, "keep.js", "y = '"+awsKey+"'\n")
	testutil.Commit(t, dir, "feat: another key") // still there
	// a commit on a branch nothing merged: a leak all the same
	testutil.Run(t, dir, "checkout", "-q", "-b", "side")
	testutil.WriteFile(t, dir, "side.js", "z = '"+slackToken+"'\n")
	testutil.Commit(t, dir, "feat: a key on a side branch")
	testutil.Run(t, dir, "checkout", "-q", "main")

	d := scanJSON(t, repo, domain.Default(), Options{History: true})
	if len(d.Files) != 1 || d.Files[0].Path != "keep.js" {
		t.Fatalf("in the files now: %+v", d.Files)
	}
	if len(d.History) != 3 || d.Commits < 4 {
		t.Fatalf("history = %+v (commits read: %d)", d.History, d.Commits)
	}
	got := map[string]bool{}
	for _, h := range d.History {
		got[h.Path] = h.InFiles
		if len(h.Commit) != 40 {
			t.Errorf("%s: commit %q", h.Path, h.Commit)
		}
	}
	if got["old.js"] || !got["keep.js"] {
		t.Errorf("in_files = %v: old.js was removed, keep.js is still there", got)
	}
	if _, ok := got["side.js"]; !ok {
		t.Errorf("a secret that only a side branch has must be found: %v", got)
	}
	var oldFinding string
	for _, h := range d.History {
		if h.Path == "old.js" {
			oldFinding = h.Commit
		}
	}
	if oldFinding != oldCommit {
		t.Errorf("old.js was added by %s, found at %s", oldCommit, oldFinding)
	}

	o, _ := scan(t, repo, domain.Default(), Options{History: true})
	for _, want := range []string{"only in the history", "still in the file", "rotate it first", "Tide never rewrites history"} {
		if !strings.Contains(o.all(), want) {
			t.Errorf("missing %q in:\n%s", want, o.all())
		}
	}
}

func TestScanHistoryLimit(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "a.js", "x = '"+githubToken+"'\n")
	testutil.Commit(t, dir, "feat: key")
	for _, f := range []string{"b", "c", "d"} {
		testutil.WriteFile(t, dir, f+".txt", f+"\n")
		testutil.Commit(t, dir, "feat: "+f)
	}
	d := scanJSON(t, repo, domain.Default(), Options{History: true, Limit: 2})
	if d.Commits != 2 || len(d.History) != 0 {
		t.Errorf("only the newest 2 commits are read, and the key is older: %+v", d)
	}
	d = scanJSON(t, repo, domain.Default(), Options{History: true})
	if len(d.History) != 1 {
		t.Errorf("by default it goes further back: %+v", d.History)
	}
}

func TestScanUsageErrors(t *testing.T) {
	repo, _ := setup(t)
	for name, opts := range map[string]Options{
		"--limit without --history": {Limit: 10},
		"a negative limit":          {History: true, Limit: -1},
		"a limit that is too big":   {History: true, Limit: MaxHistoryLimit + 1},
	} {
		if _, err := scan(t, repo, domain.Default(), opts); apperr.CodeOf(err) != apperr.CodeUsage {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestScanHonoursTheConfiguration(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, "a.txt", githubToken+"\n")
	cfg := domain.Default()
	cfg.Secrets.Enabled = false
	o, err := scan(t, repo, cfg, Options{})
	if err != nil || !strings.Contains(o.all(), "secret scanning is off") || strings.Contains(o.all(), "a.txt") {
		t.Errorf("a team that turned scanning off is not scanned: err=%v\n%s", err, o.all())
	}
	if d := scanJSON(t, repo, cfg, Options{}); !d.Disabled || d.Scanned != 0 || len(d.Files) != 0 {
		t.Errorf("disabled = %+v", d)
	}

	// a custom pattern of the project is used, like in the commit hook
	cfg = domain.Default()
	cfg.Secrets.CustomPatterns = []domain.CustomPattern{{Name: "internal_token", Pattern: `corp_[A-Za-z0-9]{12}`}}
	testutil.WriteFile(t, dir, "internal.txt", "t = corp_abcdefghij12\n")
	d := scanJSON(t, repo, cfg, Options{})
	found := map[string]bool{}
	for _, f := range d.Files {
		found[f.Rule] = true
	}
	if !found["internal_token"] || !found["github_token"] {
		t.Errorf("rules found = %v", found)
	}
}

func TestScanFailExitsWithAPolicyError(t *testing.T) {
	repo, dir := setup(t)
	if _, err := scan(t, repo, domain.Default(), Options{Fail: true}); err != nil {
		t.Fatalf("nothing found, nothing to fail: %v", err)
	}
	testutil.WriteFile(t, dir, "a.txt", githubToken+"\n")
	o, err := scan(t, repo, domain.Default(), Options{Fail: true})
	if apperr.CodeOf(err) != apperr.CodePolicy || !strings.Contains(err.Error(), "1 likely secret found") || !strings.Contains(apperr.HintOf(err), "rotate") {
		t.Errorf("err = %v hint=%q", err, apperr.HintOf(err))
	}
	if !strings.Contains(o.all(), "a.txt:1") {
		t.Errorf("the findings are still shown:\n%s", o.all())
	}
	// without --fail the same finding exits 0
	if _, err := scan(t, repo, domain.Default(), Options{}); err != nil {
		t.Errorf("without --fail: %v", err)
	}
}

func TestJSONListsAreNeverNull(t *testing.T) {
	repo, _ := setup(t)
	o, _ := scan(t, repo, domain.Default(), Options{JSON: true})
	for _, want := range []string{`"files": []`, `"history": []`, `"disabled": false`} {
		if !strings.Contains(o.stdout.String(), want) {
			t.Errorf("missing %q in:\n%s", want, o.stdout.String())
		}
	}
	if o.stderr.Len() != 0 {
		t.Errorf("stdout is the document, nothing else: stderr = %q", o.stderr.String())
	}
}

func TestAnEnvFileGetsTheAdviceThatFitsIt(t *testing.T) {
	repo, dir := setup(t)
	testutil.WriteFile(t, dir, ".env", "GITHUB_TOKEN="+githubToken+"\n")
	o, err := scan(t, repo, domain.Default(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(o.all(), "a .env file is meant to hold secrets: add it to .gitignore") {
		t.Errorf("an untracked .env is told to be ignored:\n%s", o.all())
	}
	if strings.Contains(o.all(), "use a variable instead") {
		t.Errorf("a .env must not be told to use a variable:\n%s", o.all())
	}

	// a secret in source code keeps the usual advice, and both can appear together
	testutil.WriteFile(t, dir, "config/.env.production", "K="+githubToken+"\n")
	testutil.WriteFile(t, dir, "app.js", "t = '"+awsKey+"'\n")
	o, _ = scan(t, repo, domain.Default(), Options{})
	if !strings.Contains(o.all(), "add it to .gitignore") || !strings.Contains(o.all(), "use a variable instead") {
		t.Errorf("both pieces of advice:\n%s", o.all())
	}

	// a .env that is committed is not "just add it to .gitignore": the secret is out, and the general advice applies
	testutil.Run(t, dir, "add", "-f", ".env")
	testutil.Commit(t, dir, "chore: oops")
	repo2, dir2 := setup(t)
	testutil.WriteFile(t, dir2, ".env", "K="+githubToken+"\n")
	testutil.Run(t, dir2, "add", "-f", ".env")
	testutil.Commit(t, dir2, "chore: oops")
	o, _ = scan(t, repo2, domain.Default(), Options{})
	if strings.Contains(o.all(), "add it to .gitignore") || !strings.Contains(o.all(), "use a variable instead") {
		t.Errorf("a committed .env gets the general advice:\n%s", o.all())
	}
	_ = repo
}
