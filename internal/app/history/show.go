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
func Show(ctx context.Context, repo *repository.Repository, rev string, asJSON bool, p output.Printer) error {
	rev = strings.TrimSpace(rev)
	if rev == "" || strings.HasPrefix(rev, "-") {
		return apperr.Usage("invalid commit %q", rev)
	}
	if ok, err := repo.CommitExists(ctx, rev); err != nil || !ok {
		return apperr.New(apperr.CodeUsage, fmt.Sprintf("commit %q not found", rev), "give a hash, a branch or a tag; see `gtide history` for hashes")
	}
	d, err := repo.Show(ctx, rev)
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
		}{d.FullHash, d.AuthorName, d.AuthorEmail, d.Date, d.Subject, d.Body, toJSONFiles(d.Files)}
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
	return nil
}
