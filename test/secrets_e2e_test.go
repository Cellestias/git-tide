package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/testutil"
)

// The fake secrets are assembled here so this file does not trip the scanner it tests.
var (
	e2eGithubToken = "gh" + "p_" + strings.Repeat("a1B2", 9)
	e2eAWSKey      = "AK" + "IA" + "ABCDEFGHIJKLMNOP"
)

func TestSecretsScanEndToEnd(t *testing.T) {
	dir, _ := testutil.NewRepoWithRemote(t)
	if out, code := gtide(t, dir, "init", "--no-hooks"); code != 0 {
		t.Fatalf("init: %d\n%s", code, out)
	}
	testutil.Run(t, dir, "config", "user.email", "t@example.invalid")
	testutil.WriteFile(t, dir, "src/old.js", "token = '"+e2eGithubToken+"'\n")
	git(t, dir, "add", "src/old.js")
	git(t, dir, "commit", "-q", "-m", "feat: a key by mistake")
	testutil.WriteFile(t, dir, "src/old.js", "token = process.env.TOKEN\n")
	git(t, dir, "add", "src/old.js")
	git(t, dir, "commit", "-q", "-m", "fix: use the environment")
	testutil.WriteFile(t, dir, "notes.txt", "aws "+e2eAWSKey+"\n") // not committed

	type scanDoc struct {
		Scanned int
		Files   []struct {
			Path    string
			Line    int
			Rule    string
			Tracked bool
		}
		History []struct {
			Path    string
			Rule    string
			InFiles bool `json:"in_files"`
		}
		Commits int
	}
	read := func(args ...string) (scanDoc, string, int) {
		t.Helper()
		out, code := stdout(t, dir, args...)
		var d scanDoc
		if err := json.Unmarshal([]byte(out), &d); err != nil {
			t.Fatalf("gtide %s: not JSON: %v\n%s", strings.Join(args, " "), err, out)
		}
		return d, out, code
	}

	d, raw, code := read("secrets", "scan", "--json")
	if code != 0 || len(d.Files) != 1 || d.Files[0].Path != "notes.txt" || d.Files[0].Rule != "aws_access_key_id" || d.Files[0].Tracked || d.Files[0].Line != 1 {
		t.Fatalf("files: exit %d, %+v\n%s", code, d.Files, raw)
	}
	if len(d.History) != 0 {
		t.Errorf("history is only read when asked: %+v", d.History)
	}

	d, raw, code = read("secrets", "scan", "--history", "--json")
	if code != 0 || len(d.History) != 1 || d.History[0].Path != "src/old.js" || d.History[0].Rule != "github_token" || d.History[0].InFiles {
		t.Fatalf("history: exit %d, %+v\n%s", code, d.History, raw)
	}
	for _, secret := range []string{e2eGithubToken, e2eAWSKey} {
		if strings.Contains(raw, secret) || strings.Contains(raw, secret[:12]) {
			t.Errorf("the JSON carries the value:\n%s", raw)
		}
	}

	text, code := gtide(t, dir, "secrets", "scan", "--history")
	if code != 0 || !strings.Contains(text, "notes.txt:1") || !strings.Contains(text, "only in the history") || strings.Contains(text, e2eGithubToken) || strings.Contains(text, e2eAWSKey) {
		t.Fatalf("text: exit %d\n%s", code, text)
	}

	if out, code := gtide(t, dir, "secrets", "scan", "--fail"); code != 4 || !strings.Contains(out, "1 likely secret found") {
		t.Errorf("--fail: exit %d\n%s", code, out)
	}
	if out, code := gtide(t, dir, "secrets", "scan", "--limit", "5"); code != 2 {
		t.Errorf("--limit without --history is a usage error: exit %d\n%s", code, out)
	}
	// once the file is gone there is nothing to find in the files, and --fail agrees
	git(t, dir, "add", "notes.txt")
	git(t, dir, "rm", "-q", "-f", "notes.txt")
	if out, code := gtide(t, dir, "secrets", "scan", "--fail"); code != 0 {
		t.Errorf("clean: exit %d\n%s", code, out)
	}
}

func TestDoctorJSONEndToEnd(t *testing.T) {
	dir, _ := testutil.NewRepoWithRemote(t)
	if out, code := gtide(t, dir, "init", "--no-hooks"); code != 0 {
		t.Fatalf("init: %d\n%s", code, out)
	}
	type doctorDoc struct {
		OK       bool
		Warnings int
		Failures int
		Checks   []struct{ Level, Name, Detail, Hint string }
	}
	read := func() (doctorDoc, int) {
		t.Helper()
		out, code := stdout(t, dir, "doctor", "--json")
		var d doctorDoc
		if err := json.Unmarshal([]byte(out), &d); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		return d, code
	}
	find := func(d doctorDoc, name string) (level string, ok bool) {
		for _, c := range d.Checks {
			if c.Name == name {
				return c.Level, true
			}
		}
		return "", false
	}

	d, code := read()
	if len(d.Checks) < 5 {
		t.Fatalf("checks = %+v", d.Checks)
	}
	if _, ok := find(d, ".env"); ok {
		t.Error("no .env, no check")
	}
	if d.OK != (code == 0) {
		t.Errorf("ok=%v but exit %d: the JSON and the exit code must agree", d.OK, code)
	}

	// an .env Git does not ignore is a warning, in the JSON and not an exit code
	testutil.WriteFile(t, dir, ".env", "GITHUB_TOKEN=x\n")
	d, code = read()
	if level, _ := find(d, ".env"); level != "warn" || d.Warnings < 1 {
		t.Errorf(".env = %q warnings=%d", level, d.Warnings)
	}
	if code != 0 {
		t.Errorf("a warning does not fail doctor: exit %d", code)
	}

	// tracked: a failure, exit 1, and the JSON is still the whole document
	git(t, dir, "add", "-f", ".env")
	git(t, dir, "commit", "-q", "-m", "chore: oops")
	d, code = read()
	if level, _ := find(d, ".env"); level != "fail" || d.OK || d.Failures < 1 || code != 1 {
		t.Errorf(".env = %q ok=%v failures=%d exit=%d", level, d.OK, d.Failures, code)
	}
}
