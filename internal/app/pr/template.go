package pr

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cellestias/git-tide/internal/apperr"
)

// TemplateFile is the pull request template at the repository root.
const TemplateFile = "description.pr.md"

const maxTemplateSize = 64 << 10

// Template is the parsed description.pr.md: a first line "# Title" and the
// rest as the body. Either part may be empty.
type Template struct {
	Title string
	Body  string
}

// Vars are the values placeholders expand to.
type Vars struct {
	Branch  string
	Base    string
	Commits []string // commit subjects
}

// LoadTemplate reads description.pr.md from root. found is false when the file
// does not exist. Anything but a regular file (a symlink, a directory) or a
// file over 64 KiB is refused: the file is committed content, not trusted input.
func LoadTemplate(root string) (t Template, found bool, err error) {
	path := filepath.Join(root, TemplateFile)
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return Template{}, false, nil
	}
	if err != nil {
		return Template{}, false, apperr.Wrap(apperr.CodeConfig, "cannot read "+TemplateFile, "", err)
	}
	if !fi.Mode().IsRegular() {
		return Template{}, false, apperr.New(apperr.CodeConfig, TemplateFile+" must be a regular file, not a symlink or directory",
			"replace it with a plain file or remove it")
	}
	if fi.Size() > maxTemplateSize {
		return Template{}, false, apperr.New(apperr.CodeConfig, fmt.Sprintf("%s is larger than %d KiB", TemplateFile, maxTemplateSize>>10), "")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // fixed file name under the repository root
	if err != nil {
		return Template{}, false, apperr.Wrap(apperr.CodeConfig, "cannot read "+TemplateFile, "", err)
	}
	return ParseTemplate(string(raw)), true, nil
}

// ParseTemplate splits text into title and body. The title is the first line
// when it is a level-1 heading ("# ..."); otherwise the whole text is the body.
func ParseTemplate(text string) Template {
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	text = strings.TrimLeft(text, "\n")
	first, rest, _ := strings.Cut(text, "\n")
	if strings.HasPrefix(first, "# ") {
		return Template{Title: strings.TrimSpace(first[2:]), Body: strings.Trim(rest, "\n")}
	}
	return Template{Body: strings.Trim(text, "\n")}
}

// Render expands {{commits}}, {{branch}}, {{base}} and {{commit_count}} in one
// pass, so text that came from a commit subject is never expanded again.
// Unknown placeholders are left as written.
func (t Template) Render(v Vars) Template {
	r := strings.NewReplacer(
		"{{commits}}", strings.TrimRight(DefaultBody(v.Commits), "\n"),
		"{{branch}}", v.Branch,
		"{{base}}", v.Base,
		"{{commit_count}}", strconv.Itoa(len(v.Commits)),
	)
	return Template{Title: truncate(strings.TrimSpace(strings.ReplaceAll(r.Replace(t.Title), "\n", " "))), Body: r.Replace(t.Body)}
}

// Preview is a short, terminal-safe excerpt of the template for a prompt.
func (t Template) Preview() string {
	var b strings.Builder
	if t.Title != "" {
		fmt.Fprintf(&b, "title: %s\n", clean(t.Title))
	}
	lines := strings.Split(strings.TrimSpace(cleanText(t.Body)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return strings.TrimRight(b.String(), "\n")
	}
	const maxLines = 6
	for i, l := range lines {
		if i == maxLines {
			fmt.Fprintf(&b, "  … %d more line(s)\n", len(lines)-maxLines)
			break
		}
		fmt.Fprintf(&b, "  %s\n", ellipsize(l, 100))
	}
	return strings.TrimRight(b.String(), "\n")
}
