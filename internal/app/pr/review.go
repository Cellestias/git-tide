package pr

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
)

// maxCommentJSON caps one comment in the JSON: the body is user text of any size.
const maxCommentJSON = 20000

// target resolves the request a review command is about, and the adapter that can review it.
func target(ctx context.Context, repo *repository.Repository, cfg domain.Config, c provider.Provider, refText string, p output.Printer) (provider.Repo, provider.Reviewer, int, error) {
	rev, ok := c.(provider.Reviewer)
	if !ok {
		return provider.Repo{}, nil, 0, apperr.New(apperr.CodeConfig,
			fmt.Sprintf("reading the comments and the diff of a %s is not available for %s yet", c.Noun(), providerLabel(c)), "")
	}
	r, err := c.ParseRef(refText)
	if err != nil {
		return provider.Repo{}, nil, 0, apperr.Usage("%v", err)
	}
	hosted, err := hostedRepo(ctx, repo, cfg, c)
	if err != nil {
		return provider.Repo{}, nil, 0, err
	}
	if r.Repo != nil && !strings.EqualFold(r.Repo.String(), hosted.String()) {
		return provider.Repo{}, nil, 0, apperr.Usage("%s belongs to %s but remote %q is %s", c.Noun(), r.Repo, cfg.Remote, hosted)
	}
	return hosted, rev, r.Number, nil
}

// needsToken fails clearly when what the command does needs a token that is not there.
func needsToken(c provider.Provider) error {
	if c.HasToken() {
		return nil
	}
	return apperr.New(apperr.CodeConfig, c.TokenEnv()+" is not set",
		"export "+c.TokenEnv()+" (or provider_token in secret.tide.yml, or .env at the repository root); it needs "+c.Noun()+" write access")
}

// ---- diff -------------------------------------------------------------------

// jsonDiffFile is one file of the patch of a request.
type jsonDiffFile struct {
	Path    string `json:"path"`
	OldPath string `json:"old_path"` // set for a rename
	Status  string `json:"status"`   // added, modified, deleted or renamed
	Binary  bool   `json:"binary"`
	Patch   string `json:"patch"` // the hunks, from the first @@ line; empty for a binary file
}

// splitPatch cuts a unified diff into its files.
func splitPatch(patch string) []jsonDiffFile {
	var files []jsonDiffFile
	var cur *jsonDiffFile
	var body []string
	inHunks := false
	flush := func() {
		if cur != nil {
			cur.Patch = strings.TrimRight(strings.Join(body, "\n"), "\n")
			files = append(files, *cur)
		}
		cur, body, inHunks = nil, nil, false
	}
	for _, line := range strings.Split(strings.TrimRight(patch, "\n"), "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			cur = &jsonDiffFile{Status: "modified"}
			cur.OldPath, cur.Path = pathsOfHeader(strings.TrimPrefix(line, "diff --git "))
			continue
		}
		if cur == nil {
			continue
		}
		if inHunks {
			body = append(body, line)
			continue
		}
		switch {
		case strings.HasPrefix(line, "@@"):
			inHunks = true
			body = append(body, line)
		case strings.HasPrefix(line, "new file mode"):
			cur.Status = "added"
		case strings.HasPrefix(line, "deleted file mode"):
			cur.Status = "deleted"
		case strings.HasPrefix(line, "rename from "):
			cur.Status, cur.OldPath = "renamed", unquotePath(strings.TrimPrefix(line, "rename from "))
		case strings.HasPrefix(line, "rename to "):
			cur.Status, cur.Path = "renamed", unquotePath(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "Binary files"), strings.HasPrefix(line, "GIT binary patch"):
			cur.Binary = true
		}
	}
	flush()
	for i := range files {
		if files[i].Status != "renamed" {
			files[i].OldPath = ""
		}
	}
	return files
}

// pathsOfHeader reads `a/old b/new` of a "diff --git" line; the two are the same unless it is a rename.
func pathsOfHeader(h string) (oldPath, newPath string) {
	if strings.HasPrefix(h, `"`) { // quoted: the paths hold special characters
		if end := closingQuote(h); end > 0 {
			rest := strings.TrimSpace(h[end+1:])
			return strings.TrimPrefix(unquotePath(h[:end+1]), "a/"), strings.TrimPrefix(unquotePath(rest), "b/")
		}
	}
	if i := strings.LastIndex(h, " b/"); i >= 0 {
		return strings.TrimPrefix(h[:i], "a/"), h[i+3:]
	}
	return h, h
}

func closingQuote(s string) int {
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
		} else if s[i] == '"' {
			return i
		}
	}
	return -1
}

func unquotePath(s string) string {
	if strings.HasPrefix(s, `"`) {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
}

// DiffOptions are the arguments of `gtide pr diff`.
type DiffOptions struct {
	Ref  string
	File string // only this file
	JSON bool
}

// Diff prints the patch of a request, or the same as JSON with one entry per file.
func Diff(ctx context.Context, repo *repository.Repository, cfg domain.Config, c provider.Provider, opts DiffOptions, p output.Printer) error {
	hosted, rev, number, err := target(ctx, repo, cfg, c, opts.Ref, p)
	if err != nil {
		return err
	}
	p.Note("Reading the diff of %s %s in %s", c.Noun(), ref(c, number), hosted)
	d, err := rev.PullDiff(ctx, hosted, number)
	if err != nil {
		return readError(err, c, hosted, fmt.Sprintf("%s %s", c.Noun(), ref(c, number)))
	}
	if d.Truncated {
		p.Warn("the patch was cut: it is too large to read in full")
	}
	files := splitPatch(d.Patch)
	if opts.File != "" {
		var only []jsonDiffFile
		for _, f := range files {
			if f.Path == opts.File || f.OldPath == opts.File {
				only = append(only, f)
			}
		}
		files = only
		if len(files) == 0 {
			return apperr.Usage("%s does not change %s", ref(c, number), clean(opts.File))
		}
	}
	if opts.JSON {
		if files == nil {
			files = []jsonDiffFile{}
		}
		return encode(p, struct {
			Number    int            `json:"number"`
			HeadSHA   string         `json:"head_sha"`
			Truncated bool           `json:"truncated"`
			Files     []jsonDiffFile `json:"files"`
		}{number, d.HeadSHA, d.Truncated, files})
	}
	if opts.File == "" {
		p.Line("%s", strings.TrimRight(cleanText(d.Patch), "\n"))
		return nil
	}
	for _, f := range files {
		p.Line("%s", cleanText(f.Patch))
	}
	return nil
}

// ---- comments ---------------------------------------------------------------

type jsonComment struct {
	ID          int64  `json:"id"`
	Kind        string `json:"kind"`
	Author      string `json:"author"`
	Body        string `json:"body"`
	BodyCut     bool   `json:"body_truncated"`
	CreatedAt   string `json:"created_at"`
	URL         string `json:"url"`
	Path        string `json:"path"`
	Line        int    `json:"line"`
	Side        string `json:"side"`
	DiffHunk    string `json:"diff_hunk"`
	CommitSHA   string `json:"commit_sha"`
	InReplyTo   int64  `json:"in_reply_to"`
	Outdated    bool   `json:"outdated"`
	ReviewState string `json:"review_state"`
}

func toComment(x provider.Comment) jsonComment {
	body, cut := cleanText(x.Body), false
	if r := []rune(body); len(r) > maxCommentJSON {
		body, cut = string(r[:maxCommentJSON]), true
	}
	return jsonComment{
		ID: x.ID, Kind: x.Kind, Author: oneLine(x.Author), Body: body, BodyCut: cut, CreatedAt: x.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		URL: x.URL, Path: x.Path, Line: x.Line, Side: x.Side, DiffHunk: cleanText(x.DiffHunk), CommitSHA: x.CommitSHA,
		InReplyTo: x.InReplyTo, Outdated: x.Outdated, ReviewState: x.ReviewState,
	}
}

// CommentsOptions are the arguments of `gtide pr comments`.
type CommentsOptions struct {
	Ref  string
	JSON bool
}

// Comments prints everything said on a request: the discussion, the comments on lines and the reviews.
func Comments(ctx context.Context, repo *repository.Repository, cfg domain.Config, c provider.Provider, opts CommentsOptions, p output.Printer) error {
	hosted, rev, number, err := target(ctx, repo, cfg, c, opts.Ref, p)
	if err != nil {
		return err
	}
	p.Note("Reading the comments of %s %s in %s", c.Noun(), ref(c, number), hosted)
	list, truncated, err := rev.Comments(ctx, hosted, number)
	if err != nil {
		return readError(err, c, hosted, fmt.Sprintf("%s %s", c.Noun(), ref(c, number)))
	}
	if truncated {
		p.Warn("the comments were cut: the request has more than can be read")
	}
	if opts.JSON {
		out := make([]jsonComment, 0, len(list))
		for _, x := range list {
			out = append(out, toComment(x))
		}
		return encode(p, struct {
			Number    int           `json:"number"`
			Truncated bool          `json:"truncated"`
			Comments  []jsonComment `json:"comments"`
		}{number, truncated, out})
	}
	if len(list) == 0 {
		p.Note("Nothing was said yet.")
		return nil
	}
	for _, x := range list {
		where := ""
		switch x.Kind {
		case provider.KindInline:
			where = fmt.Sprintf("  %s:%d", clean(x.Path), x.Line)
			if x.Outdated {
				where += " (outdated)"
			}
		case provider.KindReview:
			if x.ReviewState != "commented" {
				where = "  " + x.ReviewState
			}
		}
		p.Line("%s %s%s", p.Bold(oneLine(x.Author)), p.Dim(x.CreatedAt.Format("2006-01-02 15:04")), where)
		if body := strings.TrimSpace(cleanText(x.Body)); body != "" {
			p.Line("  %s", strings.ReplaceAll(body, "\n", "\n  "))
		}
		p.Line("")
	}
	return nil
}

// ---- taking part --------------------------------------------------------------

// CommentOptions are the arguments of `gtide pr comment`.
type CommentOptions struct {
	Ref       string
	Body      string
	Path      string // with Line: a comment on that line of the diff
	Line      int
	Side      string // "right" (default) or "left"
	InReplyTo int64
}

// Commented is what a comment or a review left on the request.
type Commented struct {
	Number    int
	URL       string
	CommentID int64
	Decision  string
}

// Comment says something on a request: in its discussion, on a line of the diff or in answer to a comment.
func Comment(ctx context.Context, repo *repository.Repository, cfg domain.Config, c provider.Provider, opts CommentOptions, out *Commented, p output.Printer) error {
	if strings.TrimSpace(opts.Body) == "" {
		return apperr.Usage("say something: pass --body")
	}
	if opts.Path == "" && (opts.Line != 0 || opts.InReplyTo != 0) {
		return apperr.Usage("--line and --reply-to need --file")
	}
	if opts.Path != "" && opts.InReplyTo == 0 && opts.Line <= 0 {
		return apperr.Usage("a comment on a file needs --line (or --reply-to for an answer)")
	}
	side := provider.SideRight
	switch strings.ToLower(opts.Side) {
	case "", "right":
	case "left":
		side = provider.SideLeft
	default:
		return apperr.Usage("invalid --side %q: use right or left", opts.Side)
	}
	if err := needsToken(c); err != nil {
		return err
	}
	hosted, rev, number, err := target(ctx, repo, cfg, c, opts.Ref, p)
	if err != nil {
		return err
	}
	n := provider.NewComment{Body: opts.Body, Path: opts.Path, Line: opts.Line, Side: side, InReplyTo: opts.InReplyTo}
	if opts.Path != "" && opts.InReplyTo == 0 {
		x, err := c.GetPullRequest(ctx, hosted, number)
		if err != nil {
			return readError(err, c, hosted, fmt.Sprintf("%s %s", c.Noun(), ref(c, number)))
		}
		n.CommitSHA = x.HeadSHA
	}
	made, err := rev.AddComment(ctx, hosted, number, n)
	if err != nil {
		return readError(err, c, hosted, fmt.Sprintf("%s %s", c.Noun(), ref(c, number)))
	}
	if out != nil {
		*out = Commented{Number: number, URL: made.URL, CommentID: made.ID}
	}
	p.Success("Commented on %s %s", c.Noun(), ref(c, number))
	return nil
}

// ReviewOptions are the arguments of `gtide pr approve` and `gtide pr request-changes`.
type ReviewOptions struct {
	Ref      string
	Decision string // provider.DecisionApprove, DecisionRequestChanges or DecisionComment
	Body     string
}

// Review approves a request, asks for changes or sends a review that is only a comment.
func Review(ctx context.Context, repo *repository.Repository, cfg domain.Config, c provider.Provider, opts ReviewOptions, out *Commented, p output.Printer) error {
	if opts.Decision != provider.DecisionApprove && strings.TrimSpace(opts.Body) == "" {
		return apperr.Usage("say why: pass --body")
	}
	if err := needsToken(c); err != nil {
		return err
	}
	hosted, rev, number, err := target(ctx, repo, cfg, c, opts.Ref, p)
	if err != nil {
		return err
	}
	if err := rev.SubmitReview(ctx, hosted, number, provider.ReviewSubmission{Decision: opts.Decision, Body: opts.Body}); err != nil {
		return readError(err, c, hosted, fmt.Sprintf("%s %s", c.Noun(), ref(c, number)))
	}
	if out != nil {
		*out = Commented{Number: number, Decision: opts.Decision}
	}
	verb := map[string]string{provider.DecisionApprove: "Approved", provider.DecisionRequestChanges: "Asked for changes on", provider.DecisionComment: "Reviewed"}[opts.Decision]
	p.Success("%s %s %s", verb, c.Noun(), ref(c, number))
	return nil
}
