package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cellestias/git-tide/internal/testutil"
)

// fakeGitHub answers the alert endpoints, and counts what it was asked.
type fakeGitHub struct {
	*httptest.Server
	mu    sync.Mutex
	mode  string // "alerts", "high-only", "clean" or "denied"
	calls map[string]int
}

const (
	criticalAlert = `{"number":1,"state":"open","html_url":"https://github.com/acme/tool/security/dependabot/1",
	  "dependency":{"package":{"ecosystem":"npm","name":"lodash"},"manifest_path":"package.json"},
	  "security_advisory":{"summary":"Prototype pollution","severity":"critical"},
	  "security_vulnerability":{"severity":"critical","first_patched_version":{"identifier":"4.17.21"}}}`
	highAlert = `{"number":2,"state":"open","html_url":"u2",
	  "dependency":{"package":{"ecosystem":"go","name":"golang.org/x/net"},"manifest_path":"backend/go.mod"},
	  "security_advisory":{"summary":"HTTP/2 rapid reset","severity":"high"},
	  "security_vulnerability":{"severity":"high","first_patched_version":null}}`
)

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{mode: "alerts", calls: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		mode := f.mode
		f.calls[r.Method+" "+r.URL.Path]++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		reply := func(status int, body string) { w.WriteHeader(status); _, _ = fmt.Fprint(w, body) }
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pulls"):
			reply(201, `{"number":42,"html_url":"https://github.com/acme/tool/pull/42"}`)
		case strings.HasSuffix(r.URL.Path, "/dependabot/alerts"):
			switch {
			case r.Header.Get("Authorization") == "":
				reply(401, `{"message":"Requires authentication"}`)
			case mode == "denied":
				reply(403, `{"message":"Resource not accessible by personal access token"}`)
			case mode == "alerts":
				reply(200, "["+criticalAlert+","+highAlert+"]")
			case mode == "high-only":
				reply(200, "["+highAlert+"]")
			default:
				reply(200, "[]")
			}
		case strings.HasSuffix(r.URL.Path, "/code-scanning/alerts"):
			if mode == "denied" {
				reply(403, `{"message":"Resource not accessible by personal access token"}`)
				return
			}
			reply(404, `{"message":"no analysis found for the specified ref"}`)
		default:
			reply(404, `{"message":"Not Found"}`)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGitHub) set(mode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = mode
}

// asked is how many alert requests (both sources) reached the server.
func (f *fakeGitHub) asked() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for k, v := range f.calls {
		if strings.Contains(k, "/alerts") {
			n += v
		}
	}
	return n
}

func (f *fakeGitHub) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = map[string]int{}
}

func TestSecurityLevelsEndToEnd(t *testing.T) {
	gh := newFakeGitHub(t)
	env := []string{"TIDE_GITHUB_API_URL=" + gh.URL, "GITHUB_TOKEN=test-token", "GH_TOKEN="}
	bin := filepath.Join(binDir, "gtide")
	run := func(dir string, args ...string) (string, int) { return runEnv(t, dir, env, "", bin, args...) }

	dir, bare := testutil.NewRepoWithRemote(t)
	testutil.Run(t, dir, "remote", "set-url", "origin", "https://github.com/acme/tool.git")
	testutil.Run(t, dir, "config", "url."+bare+".insteadOf", "https://github.com/acme/tool.git")
	if out, code := gtide(t, dir, "init"); code != 0 { // installs the hooks too
		t.Fatalf("init: %s", out)
	}
	// The level is edited between scenarios: keep .tide.yml out of every commit and every `save`.
	exclude := filepath.Join(dir, ".git", "info", "exclude")
	if f, err := os.OpenFile(exclude, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err != nil {
		t.Fatal(err)
	} else {
		_, _ = f.WriteString("\n.tide.yml\n")
		_ = f.Close()
	}
	setLevel := func(level string) {
		t.Helper()
		path := filepath.Join(dir, ".tide.yml")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		for i, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "level:") {
				lines[i] = "  level: " + level
			}
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}
		gh.reset()
	}
	onRemote := func(branch string) bool {
		o, _ := git(t, bare, "branch", "--list", branch)
		return strings.Contains(o, branch)
	}
	branch := func(name string, files ...string) {
		t.Helper()
		git(t, dir, "checkout", "-q", "-b", name, "main")
		for _, f := range files {
			testutil.WriteFile(t, dir, f, "x\n")
		}
		git(t, dir, append([]string{"add", "--"}, files...)...) // never -A: .tide.yml must stay untracked
		if out, code := git(t, dir, "commit", "-q", "-m", "feat: "+name); code != 0 {
			t.Fatalf("commit: %s", out)
		}
	}

	t.Run("off looks nothing up", func(t *testing.T) {
		setLevel("off")
		branch("feature/off", "src/a.js")
		if out, code := run(dir, "push"); code != 0 {
			t.Fatalf("push: %d\n%s", code, out)
		}
		if gh.asked() != 0 {
			t.Errorf("level off asked GitHub %d times", gh.asked())
		}
	})

	t.Run("low warns at push and never blocks, and a commit stays silent and offline", func(t *testing.T) {
		setLevel("low")
		branch("feature/low", "src/a.js")
		testutil.WriteFile(t, dir, "more.txt", "m\n")
		if out, code := run(dir, "save", "-m", "feat: more"); code != 0 || strings.Contains(out, "open security alert") {
			t.Fatalf("save: %d\n%s", code, out)
		}
		if gh.asked() != 0 {
			t.Errorf("a commit at low must not touch the network (asked %d)", gh.asked())
		}
		out, code := run(dir, "push")
		if code != 0 {
			t.Fatalf("push: %d\n%s", code, out)
		}
		for _, want := range []string{"2 open security alerts on acme/tool (1 critical, 1 high)", "package.json", "backend/go.mod", "npm:lodash"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in:\n%s", want, out)
			}
		}
		if !onRemote("feature/low") {
			t.Error("not pushed")
		}
	})

	t.Run("medium: a commit warns, a push stops only for critical", func(t *testing.T) {
		setLevel("medium")
		branch("feature/medium", "src/a.js")
		testutil.WriteFile(t, dir, "more.txt", "m\n")
		out, code := run(dir, "save", "-m", "feat: more")
		if code != 0 || !strings.Contains(out, "package.json") || !strings.Contains(out, "Committed") {
			t.Fatalf("a commit warns and still happens: %d\n%s", code, out)
		}
		out, code = run(dir, "push")
		if code != 4 || !strings.Contains(out, "push blocked by security level medium: 1 open alert (1 critical)") || !strings.Contains(out, "package.json") {
			t.Fatalf("a critical alert stops the push: %d\n%s", code, out)
		}
		if onRemote("feature/medium") {
			t.Error("a blocked push reached the remote")
		}
		// with only a high alert open, medium lets the push go (and still lists it)
		gh.set("high-only")
		out, code = run(dir, "push")
		if code != 0 || !strings.Contains(out, "backend/go.mod") {
			t.Fatalf("high alerts do not stop medium: %d\n%s", code, out)
		}
		gh.set("alerts")
	})

	t.Run("high: every alert stops a push, and the push that fixes it goes through", func(t *testing.T) {
		setLevel("high")
		branch("feature/high", "src/a.js")
		out, code := run(dir, "push")
		if code != 4 || !strings.Contains(out, "security level high: 2 open alerts (1 critical, 1 high)") {
			t.Fatalf("blocked: %d\n%s", code, out)
		}
		if !strings.Contains(out, "package.json") || !strings.Contains(out, "is allowed so the fix can be sent") {
			t.Errorf("it must say how to get out:\n%s", out)
		}
		if onRemote("feature/high") {
			t.Error("a blocked push reached the remote")
		}

		// touching only one of the two affected files is not enough
		testutil.WriteFile(t, dir, "package.json", `{"dependencies":{"lodash":"4.17.21"}}`+"\n")
		git(t, dir, "add", "package.json")
		git(t, dir, "commit", "-q", "-m", "fix: bump lodash")
		out, code = run(dir, "push")
		if code != 4 || !strings.Contains(out, "1 open alert (1 high)") {
			t.Fatalf("the other alert still holds the push: %d\n%s", code, out)
		}

		// touching both is the fix
		testutil.WriteFile(t, dir, "backend/go.mod", "module backend\n")
		git(t, dir, "add", "backend/go.mod")
		git(t, dir, "commit", "-q", "-m", "fix: bump x/net")
		gh.reset()
		out, code = run(dir, "push")
		if code != 0 || !strings.Contains(out, "treated as a fix") {
			t.Fatalf("the fix must go through: %d\n%s", code, out)
		}
		if !onRemote("feature/high") {
			t.Error("the fix was not pushed")
		}
		if gh.asked() != 2 { // dependabot + code scanning, once: the hook must not ask again
			t.Errorf("GitHub was asked %d times, want 2 (one check, not one for the command and one for the hook)", gh.asked())
		}
	})

	t.Run("a plain git push is held back by the hook too", func(t *testing.T) {
		setLevel("high")
		branch("feature/raw", "src/a.js")
		gh.reset()
		out, code := runEnv(t, dir, env, "", "git", "push", "-u", "origin", "feature/raw")
		if code == 0 || !strings.Contains(out, "push blocked by security level high") {
			t.Fatalf("git push: %d\n%s", code, out)
		}
		if onRemote("feature/raw") {
			t.Error("the hook let the push through")
		}
		testutil.WriteFile(t, dir, "package.json", "{}\n")
		testutil.WriteFile(t, dir, "backend/go.mod", "module b\n")
		git(t, dir, "add", "package.json", "backend/go.mod")
		git(t, dir, "commit", "-q", "-m", "fix: bump")
		out, code = runEnv(t, dir, env, "", "git", "push", "-u", "origin", "feature/raw")
		if code != 0 {
			t.Fatalf("the fix through git push: %d\n%s", code, out)
		}
	})

	t.Run("when the alerts cannot be read: high holds, medium goes on", func(t *testing.T) {
		gh.set("denied")
		defer gh.set("alerts")
		setLevel("high")
		branch("feature/denied", "src/a.js")
		out, code := run(dir, "push")
		if code != 4 || !strings.Contains(out, "could not be") || !strings.Contains(out, "security_events") {
			t.Fatalf("high: %d\n%s", code, out)
		}
		setLevel("medium")
		out, code = run(dir, "push")
		if code != 0 || !strings.Contains(out, "could not check Dependabot alerts") {
			t.Fatalf("medium must not be held up: %d\n%s", code, out)
		}
	})

	t.Run("a pull request is held back before anything is pushed or opened", func(t *testing.T) {
		setLevel("high")
		branch("feature/pr", "src/a.js")
		gh.reset()
		out, code := run(dir, "pr", "--yes")
		if code != 4 || !strings.Contains(out, "push blocked by security level high") {
			t.Fatalf("pr: %d\n%s", code, out)
		}
		gh.mu.Lock()
		opened := gh.calls["POST /repos/acme/tool/pulls"]
		gh.mu.Unlock()
		if opened != 0 || onRemote("feature/pr") {
			t.Errorf("opened=%d onRemote=%v: nothing may happen when the check stops it", opened, onRemote("feature/pr"))
		}
		testutil.WriteFile(t, dir, "package.json", "{}\n")
		testutil.WriteFile(t, dir, "backend/go.mod", "module b\n")
		git(t, dir, "add", "package.json", "backend/go.mod")
		git(t, dir, "commit", "-q", "-m", "fix: bump")
		if out, code := run(dir, "pr", "--yes"); code != 0 || !strings.Contains(out, "pull/42") {
			t.Fatalf("pr with the fix: %d\n%s", code, out)
		}
	})

	t.Run("gtide security lists the alerts, as a table and as JSON, at any level", func(t *testing.T) {
		setLevel("off")
		out, code := run(dir, "security")
		if code != 0 || !strings.Contains(out, "2 open security alerts on acme/tool") || !strings.Contains(out, "backend/go.mod") || !strings.Contains(out, "CRITICAL") {
			t.Fatalf("security: %d\n%s", code, out)
		}
		for _, want := range []string{
			"checked         Dependabot: 2 open",
			"not checked     code scanning: not enabled on this repository",
			"level           off: nothing is checked",
			"push            security.level is off: nothing is blocked",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in:\n%s", want, out)
			}
		}
		setLevel("high")
		if out, _ := run(dir, "security"); !strings.Contains(out, "would be BLOCKED by 2 alerts (1 critical, 1 high); a push that changes backend/go.mod or package.json is allowed") {
			t.Errorf("at high the command says what a push would do:\n%s", out)
		}
		setLevel("off")
		raw, code := stdout(t, dir, "security", "--json") // no token: the stdout helper has none
		if code != 5 {
			t.Fatalf("without a token the command must fail (exit 5), got %d\n%s", code, raw)
		}
		cmd, code := runEnv(t, dir, env, "", bin, "security", "--json")
		if code != 0 {
			t.Fatalf("security --json: %d\n%s", code, cmd)
		}
		var doc struct {
			Repo   string
			Level  string
			Alerts []struct{ Severity, Path, Package string }
			Counts map[string]int
			Push   struct {
				WouldBlock bool `json:"would_block"`
				Blockers   int
			}
			Sources []struct {
				Source, Status string
				Open           int
			}
		}
		if err := json.Unmarshal([]byte(jsonPart(cmd)), &doc); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, cmd)
		}
		if doc.Repo != "acme/tool" || doc.Level != "off" || len(doc.Alerts) != 2 || doc.Alerts[0].Severity != "critical" || doc.Alerts[0].Path != "package.json" || doc.Counts["high"] != 1 {
			t.Errorf("doc = %+v", doc)
		}
		if doc.Push.WouldBlock || len(doc.Sources) != 2 || doc.Sources[0].Open != 2 || doc.Sources[1].Status != "not_enabled" {
			t.Errorf("at level off nothing blocks, and each source says how it went: %+v", doc)
		}
	})

	t.Run("doctor and config know the level", func(t *testing.T) {
		setLevel("high")
		out, code := run(dir, "doctor")
		if !strings.Contains(out, "level high: Dependabot and code scanning alerts") {
			t.Errorf("doctor (%d):\n%s", code, out)
		}
		setLevel("paranoid")
		if out, code := run(dir, "config", "validate"); code != 3 || !strings.Contains(out, "security.level") {
			t.Errorf("an unknown level must be refused (exit 3): %d\n%s", code, out)
		}
	})
}

// jsonPart returns the JSON object in combined output (stdout and stderr are interleaved there).
func jsonPart(s string) string {
	i := strings.Index(s, "{")
	j := strings.LastIndex(s, "}")
	if i < 0 || j < i {
		return s
	}
	return s[i : j+1]
}
