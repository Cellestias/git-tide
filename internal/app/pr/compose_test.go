package pr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/testutil"
)

// scripted answers prompts in order and records what was asked.
type scripted struct {
	answers []string
	asked   []string
}

func (s *scripted) ask(prompt string) (string, error) {
	s.asked = append(s.asked, prompt)
	if len(s.asked) > len(s.answers) {
		return "", nil
	}
	return s.answers[len(s.asked)-1], nil
}

func yes(string) (bool, error) { return true, nil }

func writeTemplate(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, TemplateFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const tpl = "# feat: {{branch}} into {{base}}\n\n## Summary\n{{commits}}\n\n- [ ] tests\n"

func TestTemplateUsedWithoutPromptsWhenNonInteractive(t *testing.T) {
	repo, dir, _ := setup(t)
	writeTemplate(t, dir, tpl)
	c := &fakeCreator{}
	if _, _, err := run(repo, c, Options{}); err != nil { // no Ask, no Confirm: --yes or a script
		t.Fatal(err)
	}
	if c.got.Title != "feat: feature/login-page into main" {
		t.Errorf("title = %q", c.got.Title)
	}
	if want := "## Summary\n- feat: add login page\n\n- [ ] tests"; c.got.Body != want {
		t.Errorf("body = %q, want %q", c.got.Body, want)
	}
}

func TestTemplateAcceptedAtThePrompt(t *testing.T) {
	repo, dir, _ := setup(t)
	writeTemplate(t, dir, tpl)
	c := &fakeCreator{}
	s := &scripted{}
	var question string
	confirm := func(q string) (bool, error) {
		if strings.Contains(q, TemplateFile) {
			question = q
			return true, nil
		}
		return true, nil // the final push confirmation
	}
	out, _, err := run(repo, c, Options{Ask: s.ask, Confirm: confirm})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.asked) != 0 {
		t.Errorf("free-text prompts shown although the template was accepted: %v", s.asked)
	}
	if !strings.Contains(question, "description.pr.md") || !strings.Contains(out, "Found description.pr.md") || !strings.Contains(out, "feat: feature/login-page into main") {
		t.Errorf("no preview/question:\nq=%q\n%s", question, out)
	}
	if c.got.Title != "feat: feature/login-page into main" {
		t.Errorf("title = %q", c.got.Title)
	}
}

func TestTemplateDeclinedFallsBackToPrompts(t *testing.T) {
	repo, dir, _ := setup(t)
	writeTemplate(t, dir, tpl)
	c := &fakeCreator{}
	s := &scripted{answers: []string{"My own title", ""}}
	confirm := func(q string) (bool, error) { return !strings.Contains(q, TemplateFile), nil }
	if _, _, err := run(repo, c, Options{Ask: s.ask, Confirm: confirm}); err != nil {
		t.Fatal(err)
	}
	if len(s.asked) != 2 || !strings.HasPrefix(s.asked[0], "Title") || !strings.HasPrefix(s.asked[1], "Body") {
		t.Fatalf("prompts = %v", s.asked)
	}
	if c.got.Title != "My own title" {
		t.Errorf("title = %q", c.got.Title)
	}
	if c.got.Body != "- feat: add login page\n" {
		t.Errorf("empty body answer should give the commit list, got %q", c.got.Body)
	}
}

func TestPromptsWithoutTemplate(t *testing.T) {
	repo, _, _ := setup(t)

	// Enter, Enter: the defaults
	c := &fakeCreator{}
	s := &scripted{answers: []string{"", ""}}
	if _, _, err := run(repo, c, Options{Ask: s.ask, Confirm: yes}); err != nil {
		t.Fatal(err)
	}
	if c.got.Title != "feat: add login page" || c.got.Body != "- feat: add login page\n" {
		t.Errorf("defaults: %+v", c.got)
	}
	if !strings.Contains(s.asked[0], "feat: add login page") {
		t.Errorf("the default title is not shown in the prompt: %q", s.asked[0])
	}

	// typed values, with control characters stripped
	c = &fakeCreator{}
	s = &scripted{answers: []string{"  Add\x1b[31m   login ", "Closes #12"}}
	if _, _, err := run(repo, c, Options{Ask: s.ask, Confirm: yes}); err != nil {
		t.Fatal(err)
	}
	if c.got.Title != "Add[31m login" {
		t.Errorf("title = %q", c.got.Title)
	}
	if strings.Contains(c.got.Title, "\x1b") {
		t.Errorf("control character kept: %q", c.got.Title)
	}
	if c.got.Body != "Closes #12" {
		t.Errorf("body = %q", c.got.Body)
	}
}

func TestFlagsSkipTheirPromptAndBeatTheTemplate(t *testing.T) {
	repo, dir, _ := setup(t)

	// no template: --title skips only the title prompt
	c := &fakeCreator{}
	s := &scripted{answers: []string{"typed body"}}
	if _, _, err := run(repo, c, Options{Title: "Flag title", Ask: s.ask, Confirm: yes}); err != nil {
		t.Fatal(err)
	}
	if len(s.asked) != 1 || !strings.HasPrefix(s.asked[0], "Body") || c.got.Title != "Flag title" || c.got.Body != "typed body" {
		t.Errorf("asked=%v got=%+v", s.asked, c.got)
	}

	// both flags: neither the file nor the prompts are consulted
	writeTemplate(t, dir, tpl)
	c = &fakeCreator{}
	s = &scripted{}
	confirm := func(q string) (bool, error) {
		if strings.Contains(q, TemplateFile) {
			t.Error("asked about the template although both flags were given")
		}
		return true, nil
	}
	if _, _, err := run(repo, c, Options{Title: "T", Body: "B", Ask: s.ask, Confirm: confirm}); err != nil {
		t.Fatal(err)
	}
	if len(s.asked) != 0 || c.got.Title != "T" || c.got.Body != "B" {
		t.Errorf("asked=%v got=%+v", s.asked, c.got)
	}

	// template accepted, --title still wins for its field
	c = &fakeCreator{}
	if _, _, err := run(repo, c, Options{Title: "Flag title", Ask: (&scripted{}).ask, Confirm: yes}); err != nil {
		t.Fatal(err)
	}
	if c.got.Title != "Flag title" || !strings.Contains(c.got.Body, "## Summary") {
		t.Errorf("got %+v", c.got)
	}
}

func TestTemplateWithoutHeadingKeepsDefaultTitle(t *testing.T) {
	repo, dir, _ := setup(t)
	writeTemplate(t, dir, "Only a body, {{commit_count}} commit\n")
	c := &fakeCreator{}
	if _, _, err := run(repo, c, Options{}); err != nil {
		t.Fatal(err)
	}
	if c.got.Title != "feat: add login page" || c.got.Body != "Only a body, 1 commit" {
		t.Errorf("got %+v", c.got)
	}
}

func TestUnsafeTemplateStopsBeforeAnythingIsPushed(t *testing.T) {
	repo, dir, bare := setup(t)
	target := filepath.Join(t.TempDir(), "real.md")
	if err := os.WriteFile(target, []byte("# secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, TemplateFile)); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	c := &fakeCreator{}
	_, _, err := run(repo, c, Options{})
	if apperr.CodeOf(err) != apperr.CodeConfig || c.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, c.calls)
	}
	if out := testutil.Run(t, bare, "branch", "--list", "feature/login-page"); strings.TrimSpace(out) != "" {
		t.Error("branch pushed despite the unusable template")
	}
}
