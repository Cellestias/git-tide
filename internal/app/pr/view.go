package pr

import (
	"context"
	"fmt"
	"strings"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
)

const (
	maxViewFiles   = 50
	maxViewCommits = 30
	maxBodyLines   = 40
	maxBodyRunes   = 4000
)

// View prints the details of one pull request or merge request. ref is a
// number, "#number" ("!number" on GitLab), or a request URL.
func View(ctx context.Context, repo *repository.Repository, cfg domain.Config, c provider.Provider, refText string, p output.Printer) error {
	r, err := c.ParseRef(refText)
	if err != nil {
		return apperr.Usage("%v", err)
	}
	hosted, err := hostedRepo(ctx, repo, cfg, c)
	if err != nil {
		return err
	}
	if r.Repo != nil && !strings.EqualFold(r.Repo.String(), hosted.String()) {
		return apperr.Usage("%s belongs to %s but remote %q is %s", c.Noun(), r.Repo, cfg.Remote, hosted)
	}

	p.Note("Reading %s %s in %s", c.Noun(), ref(c, r.Number), hosted)
	x, err := c.GetPullRequest(ctx, hosted, r.Number)
	if err != nil {
		return readError(err, c, hosted, fmt.Sprintf("%s %s", c.Noun(), ref(c, r.Number)))
	}
	// The rest is best effort: a failure becomes a warning, never a failed command.
	review, rerr := c.Reviews(ctx, hosted, r.Number)
	if rerr != nil {
		p.Warn("cannot read reviews: %s", clean(rerr.Error()))
	}
	files, ftrunc, ferr := c.ChangedFiles(ctx, hosted, r.Number)
	if ferr != nil {
		p.Warn("cannot read changed files: %s", clean(ferr.Error()))
	}
	commits, ctrunc, cerr := c.Commits(ctx, hosted, r.Number)
	if cerr != nil {
		p.Warn("cannot read commits: %s", clean(cerr.Error()))
	}

	p.Line("%s %s", ref(c, x.Number), clean(x.Title))
	p.Line("%s", clean(x.URL))
	p.Line("")
	p.Line("state:      %s", stateLabel(x))
	p.Line("author:     %s", clean(x.Author))
	p.Line("assignees:  %s", joinOrDash(x.Assignees))
	p.Line("branches:   %s → %s%s", clean(x.HeadRef), clean(x.BaseRef), forkNote(x, hosted))
	p.Line("created:    %s", when(x.CreatedAt))
	p.Line("updated:    %s", when(x.UpdatedAt))
	if rerr == nil {
		p.Line("review:     %s", reviewLine(review, x.Reviewers))
	}
	if body := formatBody(x.Body); body != "" {
		p.Line("")
		p.Line("Description")
		p.Line("%s", body)
	}
	if ferr == nil {
		p.Line("")
		p.Line("%s", filesHeader(files, ftrunc))
		for i, f := range files {
			if i == maxViewFiles {
				p.Line("  … and %d more", len(files)-maxViewFiles)
				break
			}
			p.Line("  %s", fileLine(f))
		}
	}
	if cerr == nil {
		more := ""
		if ctrunc {
			more = "+"
		}
		p.Line("")
		p.Line("Commits (%d%s)", len(commits), more)
		for i, cm := range commits {
			if i == maxViewCommits {
				p.Line("  … and %d more", len(commits)-maxViewCommits)
				break
			}
			p.Line("  %s  %s", shortSHA(clean(cm.SHA)), clean(cm.Subject))
		}
	}
	return nil
}

// forkNote flags a source that is not the repository itself.
func forkNote(x provider.PullRequest, hosted provider.Repo) string {
	if strings.EqualFold(x.HeadRepo, hosted.String()) {
		return ""
	}
	if x.HeadRepo != "" {
		return " (fork: " + clean(x.HeadRepo) + ")"
	}
	return " (fork)"
}

func formatBody(body string) string {
	body = strings.TrimSpace(cleanText(body))
	if body == "" {
		return ""
	}
	cut := false
	if r := []rune(body); len(r) > maxBodyRunes {
		body, cut = string(r[:maxBodyRunes]), true
	}
	lines := strings.Split(strings.ReplaceAll(body, "\t", "    "), "\n")
	if len(lines) > maxBodyLines {
		lines, cut = lines[:maxBodyLines], true
	}
	for i, l := range lines {
		lines[i] = "  " + strings.TrimRight(l, " ")
	}
	if cut {
		lines = append(lines, "  …")
	}
	return strings.Join(lines, "\n")
}

func filesHeader(files []provider.ChangedFile, truncated bool) string {
	var add, del int
	stats := true
	for _, f := range files {
		add, del = add+f.Additions, del+f.Deletions
		stats = stats && f.HasStats
	}
	more := ""
	if truncated {
		more = "+"
	}
	if stats && len(files) > 0 {
		return fmt.Sprintf("Changed files (%d%s, +%d −%d)", len(files), more, add, del)
	}
	return fmt.Sprintf("Changed files (%d%s)", len(files), more)
}

func fileLine(f provider.ChangedFile) string {
	letter := map[string]string{"added": "A", "deleted": "D", "renamed": "R"}[f.Status]
	if letter == "" {
		letter = "M"
	}
	name := clean(f.Path)
	if f.OldPath != "" && f.OldPath != f.Path {
		name = clean(f.OldPath) + " → " + name
	}
	if f.HasStats {
		return fmt.Sprintf("%s  %s  +%d −%d", letter, name, f.Additions, f.Deletions)
	}
	return letter + "  " + name
}
