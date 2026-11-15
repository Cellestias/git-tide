package secrets

import (
	"fmt"
	"time"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/output"
)

// maxRows caps the lines printed per list; the JSON and the counts are complete.
const maxRows = 30

func render(p output.Printer, res Result, history bool) {
	if res.Disabled {
		p.Warn("secret scanning is off (secrets.enabled: false in .tide.yml): nothing was scanned")
		return
	}
	switch {
	case len(res.Files) == 0:
		p.Success("No likely secrets in the %d file%s scanned", res.Scanned, plural(res.Scanned))
	default:
		tracked := 0
		for _, f := range res.Files {
			if f.Tracked {
				tracked++
			}
		}
		p.Warn("%d likely secret%s in the files (%d committed, %d not committed)", len(res.Files), plural(len(res.Files)), tracked, len(res.Files)-tracked)
		rows := [][]string{{"FILE", "RULE", "STATE"}}
		for i, f := range res.Files {
			if i == maxRows {
				break
			}
			state := "committed"
			if !f.Tracked {
				state = "not committed, not ignored"
			}
			rows = append(rows, []string{fmt.Sprintf("%s:%d", clean(f.Path), f.Line), f.Rule, state})
		}
		p.Table(rows, nil)
		if len(res.Files) > maxRows {
			p.Line("    ... and %d more (--json lists them all)", len(res.Files)-maxRows)
		}
	}
	p.Info("scanned        %d file%s, %d skipped (binary, too big, or not a regular file)", res.Scanned, plural(res.Scanned), res.Skipped)
	p.Info("matching       the built-in detectors, the custom patterns of .tide.yml and the local ones of secret.tide.yml")
	p.Info("shown          the file, the line and the rule: never the value")

	if history {
		switch {
		case len(res.History) == 0:
			p.Success("No likely secrets added by the %d newest commit%s of any branch or tag", res.Commits, plural(res.Commits))
		default:
			still := 0
			for _, h := range res.History {
				if h.InFiles {
					still++
				}
			}
			p.Warn("%d likely secret%s added by commits (%d still in the files, %d only in the history)", len(res.History), plural(len(res.History)), still, len(res.History)-still)
			rows := [][]string{{"COMMIT", "DATE", "FILE", "RULE", "NOW"}}
			for i, h := range res.History {
				if i == maxRows {
					break
				}
				now := "only in the history"
				if h.InFiles {
					now = "still in the file"
				}
				short := h.Commit
				if len(short) > 10 {
					short = short[:10]
				}
				rows = append(rows, []string{short, dateOf(h.Date), fmt.Sprintf("%s:%d", clean(h.Path), h.Line), h.Rule, now})
			}
			p.Table(rows, nil)
			if len(res.History) > maxRows {
				p.Line("    ... and %d more (--json lists them all)", len(res.History)-maxRows)
			}
			p.Note("a secret that was committed stays exposed even after the file is cleaned: rotate it first. Tide never rewrites history")
		}
	} else if len(res.Files) == 0 {
		p.Note("add --history to look in what the commits of every branch ever added")
	}
	if res.Truncated {
		p.Warn("the scan was cut: there are more files or findings than Tide reads")
	}
	if len(res.Files) > 0 {
		p.Note("remove the value from the file and use a variable instead; for a false positive add tide:allow-secret to that line")
	}
}

func dateOf(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02")
}

type jsonFile struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Rule    string `json:"rule"`
	Tracked bool   `json:"tracked"`
}

type jsonHistory struct {
	Commit  string    `json:"commit"`
	Date    time.Time `json:"date"`
	Path    string    `json:"path"`
	Line    int       `json:"line"`
	Rule    string    `json:"rule"`
	InFiles bool      `json:"in_files"`
}

func writeJSON(p output.Printer, res Result) error {
	out := struct {
		Disabled  bool          `json:"disabled"`
		Scanned   int           `json:"scanned"`
		Skipped   int           `json:"skipped"`
		Files     []jsonFile    `json:"files"`
		History   []jsonHistory `json:"history"`
		Commits   int           `json:"commits"`
		Truncated bool          `json:"truncated"`
	}{Disabled: res.Disabled, Scanned: res.Scanned, Skipped: res.Skipped, Files: []jsonFile{}, History: []jsonHistory{}, Commits: res.Commits, Truncated: res.Truncated}
	for _, f := range res.Files {
		out.Files = append(out.Files, jsonFile(f))
	}
	for _, h := range res.History {
		out.History = append(out.History, jsonHistory(h))
	}
	if err := p.JSON(out); err != nil {
		return apperr.Wrap(apperr.CodeGeneral, "cannot write JSON", "", err)
	}
	return nil
}
