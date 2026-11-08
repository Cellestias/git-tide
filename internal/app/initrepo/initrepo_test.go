package initrepo

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/config"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/hooks"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/testutil"
)

func open(t *testing.T, dir string) *repository.Repository {
	t.Helper()
	repo, err := repository.Open(context.Background(), executor.Exec{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestInitCreatesValidConfigAndHooks(t *testing.T) {
	dir := testutil.NewRepo(t)
	res, err := Run(context.Background(), open(t, dir), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.ConfigCreated || res.Base != "main" {
		t.Fatalf("res = %+v", res)
	}
	loaded, err := config.Load(dir)
	if err != nil || !loaded.Found {
		t.Fatalf("generated config invalid: %v", err)
	}
	if len(res.HookChanges) != len(hooks.Managed) {
		t.Errorf("hooks = %+v", res.HookChanges)
	}
}

func TestInitDoesNotOverwriteConfig(t *testing.T) {
	dir := testutil.NewRepo(t)
	custom := "version: 1\nbranches:\n  default_base: stage\n  protected: [stage]\n"
	testutil.WriteFile(t, dir, config.FileName, custom)
	res, err := Run(context.Background(), open(t, dir), Options{Base: "other"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, config.FileName))
	if res.ConfigCreated || string(got) != custom {
		t.Fatalf("config overwritten: created=%v content=%q", res.ConfigCreated, got)
	}
}

func TestInitBaseSelectionAndNoHooks(t *testing.T) {
	dir := testutil.NewRepo(t)
	res, err := Run(context.Background(), open(t, dir), Options{Base: "stage", NoHooks: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Base != "stage" || !res.HooksSkipped || len(res.HookChanges) != 0 {
		t.Fatalf("res = %+v", res)
	}
	if _, err := Run(context.Background(), open(t, testutil.NewRepo(t)), Options{Base: "-bad"}); err == nil {
		t.Error("invalid base accepted")
	}
}

func TestInitDetectsRemoteDefaultBranch(t *testing.T) {
	dir, _ := testutil.NewRepoWithRemote(t)
	testutil.Run(t, dir, "remote", "set-head", "origin", "main")
	res, err := Run(context.Background(), open(t, dir), Options{NoHooks: true})
	if err != nil || res.Base != "main" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInitProviderFlagAndSecretFile(t *testing.T) {
	for _, tc := range []struct{ flag, wantVar string }{
		{"", "GITHUB_TOKEN"}, {"github", "GITHUB_TOKEN"}, {"gitlab", "GITLAB_TOKEN"},
	} {
		dir := testutil.NewRepo(t)
		res, err := Run(context.Background(), open(t, dir), Options{Provider: tc.flag, NoHooks: true})
		if err != nil {
			t.Fatal(err)
		}
		want := tc.flag
		if want == "" {
			want = "auto"
		}
		loaded, err := config.Load(dir)
		if err != nil || loaded.Config.Provider != want || res.Provider != want {
			t.Errorf("flag %q: provider = %q (res %q), err %v", tc.flag, loaded.Config.Provider, res.Provider, err)
		}
		if !res.SecretCreated || !strings.Contains(read(t, res.SecretPath), "provider_token: ${"+tc.wantVar+"}") {
			t.Errorf("flag %q: secret file = %q", tc.flag, read(t, res.SecretPath))
		}
		if v, ok := loaded.Local.Local.TokenVar(); !ok || v != tc.wantVar {
			t.Errorf("flag %q: TokenVar = %q, %v", tc.flag, v, ok)
		}
		if fi, _ := os.Stat(res.SecretPath); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
			t.Errorf("secret file mode = %v", fi.Mode().Perm())
		}
	}
}

func TestInitAutoPicksTokenVarFromRemote(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.Run(t, dir, "remote", "add", "origin", "git@gitlab.com:group/proj.git")
	res, err := Run(context.Background(), open(t, dir), Options{NoHooks: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, res.SecretPath), "${GITLAB_TOKEN}") {
		t.Errorf("secret file = %q", read(t, res.SecretPath))
	}
}

func TestInitRejectsUnknownProvider(t *testing.T) {
	dir := testutil.NewRepo(t)
	if _, err := Run(context.Background(), open(t, dir), Options{Provider: "bitbucket"}); err == nil {
		t.Fatal("expected a usage error")
	}
	if _, err := os.Stat(filepath.Join(dir, config.FileName)); err == nil {
		t.Error("nothing may be written on a usage error")
	}
}

func TestInitGitignoreCreatedAppendedAndNeverDuplicated(t *testing.T) {
	// created
	dir := testutil.NewRepo(t)
	res, err := Run(context.Background(), open(t, dir), Options{NoHooks: true})
	if err != nil || !res.IgnoreCreated || res.IgnoreUpdated {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if got := read(t, filepath.Join(dir, ".gitignore")); got != "secret.tide.yml\n" {
		t.Errorf(".gitignore = %q", got)
	}
	// idempotent
	res, err = Run(context.Background(), open(t, dir), Options{NoHooks: true})
	if err != nil || res.ConfigCreated || res.SecretCreated || res.IgnoreCreated || res.IgnoreUpdated {
		t.Fatalf("second run changed something: %+v, %v", res, err)
	}
	if got := read(t, filepath.Join(dir, ".gitignore")); got != "secret.tide.yml\n" {
		t.Errorf("duplicated: %q", got)
	}

	// appended, existing entries kept, missing trailing newline handled
	dir = testutil.NewRepo(t)
	testutil.WriteFile(t, dir, ".gitignore", "node_modules/\n.env")
	res, err = Run(context.Background(), open(t, dir), Options{NoHooks: true})
	if err != nil || !res.IgnoreUpdated {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if got := read(t, filepath.Join(dir, ".gitignore")); got != "node_modules/\n.env\nsecret.tide.yml\n" {
		t.Errorf(".gitignore = %q", got)
	}

	// already ignored in the rooted form: untouched
	dir = testutil.NewRepo(t)
	testutil.WriteFile(t, dir, ".gitignore", "/secret.tide.yml\n")
	res, err = Run(context.Background(), open(t, dir), Options{NoHooks: true})
	if err != nil || res.IgnoreUpdated || res.IgnoreCreated {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if got := read(t, filepath.Join(dir, ".gitignore")); got != "/secret.tide.yml\n" {
		t.Errorf(".gitignore = %q", got)
	}
}

func TestInitNeverOverwritesExistingFiles(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.WriteFile(t, dir, config.FileName, "version: 1\nprovider: github\n")
	testutil.WriteFile(t, dir, config.SecretFileName, "provider_token: ${MINE}\n")
	res, err := Run(context.Background(), open(t, dir), Options{Provider: "gitlab", NoHooks: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.ConfigCreated || res.SecretCreated || res.Provider != "github" || len(res.Notes) != 1 {
		t.Errorf("res = %+v", res)
	}
	if read(t, filepath.Join(dir, config.FileName)) != "version: 1\nprovider: github\n" ||
		read(t, filepath.Join(dir, config.SecretFileName)) != "provider_token: ${MINE}\n" {
		t.Error("an existing file was modified")
	}
}

// enableAll turns the commented catalog items of a generated secret.tide.yml into
// real list items, the way a user would by deleting the leading "# ".
func enableAll(content string) string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "    # - name:") || strings.HasPrefix(line, "    #   pattern:") {
			line = "    " + strings.TrimPrefix(line, "    # ")
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func TestGeneratedSecretFileStartsWithNoActivePatterns(t *testing.T) {
	dir := testutil.NewRepo(t)
	if _, err := Run(context.Background(), open(t, dir), Options{NoHooks: true}); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadLocal(dir)
	if err != nil || !loaded.Found {
		t.Fatalf("generated file invalid: %v", err)
	}
	if n := len(loaded.Local.Secrets.Patterns); n != 0 {
		t.Errorf("%d patterns active by default, want 0", n)
	}
	if catalogSize() < 100 {
		t.Errorf("catalog has only %d detectors", catalogSize())
	}
}

func TestEveryCatalogPatternCanBeEnabledAndMatchesItsSample(t *testing.T) {
	dir := testutil.NewRepo(t)
	res, err := Run(context.Background(), open(t, dir), Options{NoHooks: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(res.SecretPath, []byte(enableAll(read(t, res.SecretPath))), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadLocal(dir)
	if err != nil {
		t.Fatalf("file with every detector enabled is invalid: %v", err)
	}
	pats := loaded.Local.Secrets.Patterns
	if len(pats) != catalogSize() {
		t.Fatalf("loaded %d patterns, catalog has %d", len(pats), catalogSize())
	}

	samples := map[string]string{}
	for _, g := range secretCatalog {
		for _, e := range g.Entries {
			samples[e.Name] = e.Sample
		}
	}
	if len(samples) != catalogSize() {
		t.Fatalf("duplicate names in the catalog: %d unique of %d", len(samples), catalogSize())
	}
	for _, p := range pats {
		sample, ok := samples[p.Name]
		if !ok {
			t.Errorf("%q loaded but not in the catalog", p.Name)
			continue
		}
		re, err := regexp.Compile(p.Pattern)
		if err != nil {
			t.Errorf("%s: %v", p.Name, err)
			continue
		}
		if !re.MatchString(sample) {
			t.Errorf("%s: pattern %q does not match its own sample %q", p.Name, p.Pattern, sample)
		}
	}
}

func TestCatalogDoesNotFlagOrdinaryCode(t *testing.T) {
	benign := []string{
		"func main() { fmt.Println(\"hello world\") }",
		"const maxRetries = 3 // retry the request",
		"https://example.com/docs/getting-started",
		"git clone https://github.com/acme/tool.git",
		"password := os.Getenv(\"DB_PASSWORD\")",
		"commit 4dc985a docs(readme): document providers",
		"version: 1.2.3",
		"sha256: " + strings.Repeat("0123456789abcdef", 4),
	}
	for _, g := range secretCatalog {
		for _, e := range g.Entries {
			re := regexp.MustCompile(e.Pattern)
			for _, line := range benign {
				if re.MatchString(line) {
					t.Errorf("%s flags ordinary text %q", e.Name, line)
				}
			}
		}
	}
}
