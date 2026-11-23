package pr

import (
	"strings"
	"time"
	"unicode"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
)

// maxJSONBody caps the description in the JSON: a request's body is user text of any size.
const maxJSONBody = 20000

// jsonRequest is a pull or merge request, the same in the list, the status and the view.
type jsonRequest struct {
	Number    int         `json:"number"`
	Title     string      `json:"title"`
	State     string      `json:"state"` // "open", "closed" or "merged"
	Draft     bool        `json:"draft"`
	URL       string      `json:"url"`
	Author    string      `json:"author"`
	Assignees []string    `json:"assignees"`
	Requested []string    `json:"requested_reviewers"` // reviews still asked for
	Labels    []jsonLabel `json:"labels"`
	Head      string      `json:"head"`      // the source branch
	Base      string      `json:"base"`      // the target branch
	HeadRepo  string      `json:"head_repo"` // "owner/name" of the source; empty if unknown or deleted
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// jsonLabel is a tag on a request. Color is six hex digits.
type jsonLabel struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

func toLabels(in []provider.Label) []jsonLabel {
	out := make([]jsonLabel, 0, len(in))
	for _, l := range in {
		out = append(out, jsonLabel{Name: oneLine(l.Name), Color: hexColor(l.Color), Description: oneLine(l.Description)})
	}
	return out
}

// hexColor keeps a label colour to what a style can safely hold: six hex digits, or nothing.
func hexColor(s string) string {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return ""
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return ""
		}
	}
	return s
}

func toRequest(x provider.PullRequest) jsonRequest {
	return jsonRequest{
		Labels: toLabels(x.Labels),
		Number: x.Number, Title: oneLine(x.Title), State: x.State, Draft: x.Draft, URL: x.URL, Author: x.Author,
		Assignees: nonNil(x.Assignees), Requested: nonNil(x.Reviewers), Head: x.HeadRef, Base: x.BaseRef, HeadRepo: x.HeadRepo,
		CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt,
	}
}

// jsonReview is what the reviewers decided.
type jsonReview struct {
	State            string   `json:"state"` // "pending", "approved" or "changes_requested"
	Approvals        []string `json:"approvals"`
	ChangesRequested []string `json:"changes_requested"`
}

func toReview(rv provider.Review) jsonReview {
	state := "pending"
	switch rv.State {
	case provider.ReviewApproved:
		state = "approved"
	case provider.ReviewChangesRequested:
		state = "changes_requested"
	}
	return jsonReview{State: state, Approvals: nonNil(rv.Approvals), ChangesRequested: nonNil(rv.ChangesRequested)}
}

func writeList(p output.Printer, prs []provider.PullRequest) error {
	out := make([]jsonRequest, 0, len(prs))
	for _, x := range prs {
		out = append(out, toRequest(x))
	}
	return encode(p, out)
}

func writeStatus(p output.Printer, c provider.Provider, hosted provider.Repo, branch string, x *provider.PullRequest, review *provider.Review, warnings []string) error {
	doc := struct {
		Provider string       `json:"provider"`
		Noun     string       `json:"noun"` // "pull request" or "merge request"
		Repo     string       `json:"repo"`
		Branch   string       `json:"branch"`
		Request  *jsonRequest `json:"request"` // null when the branch has none
		Review   *jsonReview  `json:"review"`  // null when there is no request, or the reviews could not be read
		Warnings []string     `json:"warnings"`
	}{Provider: c.Name(), Noun: c.Noun(), Repo: hosted.String(), Branch: branch, Warnings: nonNil(warnings)}
	if x != nil {
		r := toRequest(*x)
		doc.Request = &r
	}
	if review != nil {
		rv := toReview(*review)
		doc.Review = &rv
	}
	return encode(p, doc)
}

type jsonFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path"` // set for a rename
	Status    string `json:"status"`   // added, modified, deleted or renamed
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	HasStats  bool   `json:"has_stats"` // false when the provider gives no line counts
}

type jsonCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	Date    string `json:"date"` // RFC 3339, empty when unknown
}

func writeView(p output.Printer, c provider.Provider, hosted provider.Repo, viewer string, x provider.PullRequest, review *provider.Review,
	files []provider.ChangedFile, filesCut bool, commits []provider.CommitSummary, commitsCut bool, warnings []string) error {
	body, cut := bodyText(x.Body)
	doc := struct {
		jsonRequest
		Provider         string       `json:"provider"`
		Noun             string       `json:"noun"`
		Repo             string       `json:"repo"`
		Fork             bool         `json:"fork"`   // the source is another repository
		Viewer           string       `json:"viewer"` // the login of the token's owner; empty without a token
		Body             string       `json:"body"`
		BodyTruncated    bool         `json:"body_truncated"`
		Review           *jsonReview  `json:"review"`
		Files            []jsonFile   `json:"files"`
		FilesTruncated   bool         `json:"files_truncated"`
		Commits          []jsonCommit `json:"commits"` // oldest first
		CommitsTruncated bool         `json:"commits_truncated"`
		Warnings         []string     `json:"warnings"`
	}{
		jsonRequest: toRequest(x), Provider: c.Name(), Noun: c.Noun(), Repo: hosted.String(), Viewer: oneLine(viewer),
		Fork: x.HeadRepo != "" && !strings.EqualFold(x.HeadRepo, hosted.String()),
		Body: body, BodyTruncated: cut, Files: []jsonFile{}, FilesTruncated: filesCut, Commits: []jsonCommit{}, CommitsTruncated: commitsCut,
		Warnings: nonNil(warnings),
	}
	if review != nil {
		rv := toReview(*review)
		doc.Review = &rv
	}
	for _, f := range files {
		doc.Files = append(doc.Files, jsonFile{f.Path, f.OldPath, f.Status, f.Additions, f.Deletions, f.HasStats})
	}
	for _, cm := range commits {
		date := ""
		if !cm.Date.IsZero() {
			date = cm.Date.UTC().Format("2006-01-02T15:04:05Z")
		}
		doc.Commits = append(doc.Commits, jsonCommit{cm.SHA, oneLine(cm.Subject), oneLine(cm.Author), date})
	}
	return encode(p, doc)
}

func encode(p output.Printer, v any) error {
	if err := p.JSON(v); err != nil {
		return apperr.Wrap(apperr.CodeGeneral, "cannot write JSON", "", err)
	}
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// oneLine strips control characters and collapses whitespace: a title or a
// subject is one line of text, and nothing from the provider reaches a terminal
// as an escape sequence.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// bodyText keeps the lines of a description but drops control characters other
// than newline and tab, and cuts it at maxJSONBody runes.
func bodyText(s string) (text string, cut bool) {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if r := []rune(s); len(r) > maxJSONBody {
		return string(r[:maxJSONBody]), true
	}
	return s, false
}
