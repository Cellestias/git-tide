package history

import (
	"sort"
	"strconv"
	"time"

	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

type authorCount struct {
	Author  string    `json:"author"`
	Email   string    `json:"email"`
	Commits int       `json:"commits"`
	Last    time.Time `json:"last"`
}

// summarize counts the commits per author (by email, the name shown is the
// latest one used), most active first.
func summarize(p output.Printer, entries []repository.LogEntry, label, filters string, asJSON bool) error {
	byEmail := map[string]*authorCount{}
	for _, e := range entries { // newest first, so the first sighting holds the latest name and date
		a, ok := byEmail[e.AuthorEmail]
		if !ok {
			a = &authorCount{Author: e.AuthorName, Email: e.AuthorEmail, Last: e.Date}
			byEmail[e.AuthorEmail] = a
		}
		a.Commits++
	}
	list := make([]authorCount, 0, len(byEmail))
	for _, a := range byEmail {
		list = append(list, *a)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Commits != list[j].Commits {
			return list[i].Commits > list[j].Commits
		}
		return list[i].Email < list[j].Email
	})

	if asJSON {
		return encode(p, list)
	}
	if len(list) == 0 {
		p.Info("No commits match on %s%s", label, filters)
		return nil
	}
	rows := [][]string{{"COMMITS", "AUTHOR", "LAST COMMIT"}}
	for _, a := range list {
		rows = append(rows, []string{strconv.Itoa(a.Commits), a.Author, a.Last.Format("2006-01-02")})
	}
	p.Note("%d commit%s by %d author%s on %s%s", len(entries), plural(len(entries)), len(list), plural(len(list)), label, filters)
	p.Table(rows, func(_, col int, cell string) string {
		switch col {
		case 0:
			return p.Yellow(cell)
		case 2:
			return p.Dim(cell)
		}
		return cell
	})
	return nil
}
