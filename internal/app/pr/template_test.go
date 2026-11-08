package pr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/apperr"
)

func TestParseTemplate(t *testing.T) {
	cases := []struct{ name, in, title, body string }{
		{"title and body", "# feat: login\n\n## Summary\ntext\n", "feat: login", "## Summary\ntext"},
		{"title only", "# Only a title\n", "Only a title", ""},
		{"no heading", "Just a body\nsecond", "", "Just a body\nsecond"},
		{"level 2 is not a title", "## Summary\nx", "", "## Summary\nx"},
		{"hash without space", "#tag\nbody", "", "#tag\nbody"},
		{"leading blank lines and CRLF", "\r\n\r\n# T\r\nline1\r\nline2\r\n", "T", "line1\nline2"},
		{"BOM", "\ufeff# T\nb", "T", "b"},
		{"empty", "", "", ""},
	}
	for _, tc := range cases {
		got := ParseTemplate(tc.in)
		if got.Title != tc.title || got.Body != tc.body {
			t.Errorf("%s: got %+v, want title=%q body=%q", tc.name, got, tc.title, tc.body)
		}
	}
}

func TestRenderExpandsOnce(t *testing.T) {
	tpl := Template{Title: "{{branch}} → {{base}}", Body: "{{commit_count}} commit(s):\n{{commits}}\n{{unknown}}"}
	got := tpl.Render(Vars{Branch: "feature/x", Base: "main", Commits: []string{"feat: one", "fix: {{base}} two"}})
	if got.Title != "feature/x → main" {
		t.Errorf("title = %q", got.Title)
	}
	want := "2 commit(s):\n- feat: one\n- fix: {{base}} two\n{{unknown}}"
	if got.Body != want {
		t.Errorf("body = %q, want %q", got.Body, want)
	}
}

func TestRenderTitleStaysOneLine(t *testing.T) {
	got := Template{Title: "a {{commits}} b"}.Render(Vars{Commits: []string{"x", "y"}})
	if strings.Contains(got.Title, "\n") {
		t.Errorf("title spans lines: %q", got.Title)
	}
}

func TestLoadTemplate(t *testing.T) {
	dir := t.TempDir()
	if _, found, err := LoadTemplate(dir); found || err != nil {
		t.Fatalf("missing file: found=%v err=%v", found, err)
	}

	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, TemplateFile), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("# Title\nbody\n")
	tpl, found, err := LoadTemplate(dir)
	if !found || err != nil || tpl.Title != "Title" || tpl.Body != "body" {
		t.Fatalf("load = %+v, %v, %v", tpl, found, err)
	}

	write(strings.Repeat("x", maxTemplateSize+1))
	if _, _, err := LoadTemplate(dir); apperr.CodeOf(err) != apperr.CodeConfig {
		t.Errorf("oversized: %v", err)
	}

	// a symlink is refused, even to a harmless file
	os.Remove(filepath.Join(dir, TemplateFile))
	target := filepath.Join(dir, "elsewhere.md")
	if err := os.WriteFile(target, []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, TemplateFile)); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, _, err := LoadTemplate(dir); apperr.CodeOf(err) != apperr.CodeConfig {
		t.Errorf("symlink: %v", err)
	}

	// so is a directory
	os.Remove(filepath.Join(dir, TemplateFile))
	if err := os.Mkdir(filepath.Join(dir, TemplateFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadTemplate(dir); apperr.CodeOf(err) != apperr.CodeConfig {
		t.Errorf("directory: %v", err)
	}
}

func TestPreviewIsShortAndSafe(t *testing.T) {
	body := "line\x1b[31m1\n" + strings.Repeat("more\n", 20)
	p := Template{Title: "T\x1b", Body: body}.Preview()
	if strings.Contains(p, "\x1b") {
		t.Errorf("control characters in preview: %q", p)
	}
	if !strings.Contains(p, "title: T") || !strings.Contains(p, "more line(s)") {
		t.Errorf("preview = %q", p)
	}
}
