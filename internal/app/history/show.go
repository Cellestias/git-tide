package history

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

// Show prints one commit: author, date, message and the files it changed with
// their line counts (`gtide history show`).
func Show(ctx context.Context, repo *repository.Repository, rev string, asJSON, patch bool, p output.Printer) error {
	rev = strings.TrimSpace(rev)
	if rev == "" || strings.HasPrefix(rev, "-") {
		return apperr.Usage("invalid commit %q", rev)
	}
	if ok, err := repo.CommitExists(ctx, rev); err != nil || !ok {
		return apperr.New(apperr.CodeUsage, fmt.Sprintf("commit %q not found", rev), "give a hash, a branch or a tag; see `gtide history` for hashes")
	}
	d, err := repo.Show(ctx, rev, patch)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read the commit", "", err)
	}

	if asJSON {
		out := struct {
			Hash    string     `json:"hash"`
			Author  string     `json:"author"`
			Email   string     `json:"email"`
			Date    time.Time  `json:"date"`
			Subject string     `json:"subject"`
			Body    string     `json:"body"`
			Files   []jsonFile `json:"files"`
			// Only with --patch.
			Patch          *string `json:"patch,omitempty"`
			PatchTruncated bool    `json:"patch_truncated,omitempty"`
		}{Hash: d.FullHash, Author: d.AuthorName, Email: d.AuthorEmail, Date: d.Date, Subject: d.Subject, Body: d.Body, Files: toJSONFiles(d.Files)}
		if patch {
			text := d.Patch
			out.Patch, out.PatchTruncated = &text, d.Truncated
		}
		return encode(p, out)
	}

	p.Line("%s %s", p.Yellow("commit"), p.Yellow(d.FullHash))
	p.Line("Author: %s <%s>", d.AuthorName, d.AuthorEmail)
	p.Line("Date:   %s", d.Date.Format("2006-01-02 15:04"))
	p.Line("")
	p.Line("    %s", p.Bold(d.Subject))
	if d.Body != "" {
		p.Line("")
		for _, l := range strings.Split(d.Body, "\n") {
			p.Line("    %s", l)
		}
	}
	if len(d.Files) == 0 {
		return nil
	}
	p.Line("")
	rows := [][]string{{"FILE", "CHANGES"}}
	var added, deleted int
	for _, f := range d.Files {
		changes := "binary"
		if !f.Binary {
			changes = "+" + strconv.Itoa(f.Added) + " -" + strconv.Itoa(f.Deleted)
			added, deleted = added+f.Added, deleted+f.Deleted
		}
		rows = append(rows, []string{f.Path, changes})
	}
	p.Table(rows, nil)
	p.Line("%d file%s changed, +%d -%d", len(d.Files), plural(len(d.Files)), added, deleted)
	if patch && d.Patch != "" {
		p.Line("")
		_, _ = fmt.Fprint(p.Out, d.Patch)
		if d.Truncated {
			p.Line("%s", p.Dim("... patch cut at "+strconv.Itoa(repository.MaxPatchBytes/1024)+" KiB"))
		}
	}
	return nil
}
