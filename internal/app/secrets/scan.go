// Package secrets implements `gtide secrets scan`: look for likely secrets in the
// files of the repository, and, when asked, in what its history ever added. It
// uses the same detectors as the commit hook (the built-in ones, the custom
// patterns of .tide.yml and the local ones of secret.tide.yml) and, like them,
// never prints the matched text: only the file, the line and the rule.
package secrets

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/domain/secret"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

const (
	maxFileSize  = 1 << 20 // a bigger file is skipped
	maxFiles     = 20000   // a repository with more is scanned only in part
	maxFindings  = 500
	sniffBytes   = 8000
	maxLineBytes = 1 << 20 // a longer line makes the file count as skipped

	// DefaultHistoryLimit is how many commits --history reads when --limit is not given.
	DefaultHistoryLimit = 300
	// MaxHistoryLimit caps --limit: a commit's diff is read into memory.
	MaxHistoryLimit = 5000
)

type Options struct {
	History bool // also look in what the newest commits added
	Limit   int  // commits for History; 0 means DefaultHistoryLimit
	JSON    bool
	Fail    bool // exit with a policy error when something is found
}

// FileFinding is a likely secret in a file as it is now.
type FileFinding struct {
	Path    string
	Line    int
	Rule    string
	Tracked bool // false: the file is not committed (and not ignored, or it would not be scanned)
}

// HistoryFinding is a likely secret a commit added.
type HistoryFinding struct {
	Commit string
	Date   time.Time
	Path   string
	Line   int
	Rule   string
	// InFiles is true when the same rule still matches in that file: the secret is
	// in the work tree too, not only in the history.
	InFiles bool
}

// Result is what a scan found.
type Result struct {
	Disabled  bool // secrets.enabled is false: nothing was scanned
	Scanned   int  // files read
	Skipped   int  // files left out: binary, too big, too long a line, or not a regular file
	Files     []FileFinding
	History   []HistoryFinding
	Commits   int // commits read for History
	Truncated bool
}

// Scan looks for likely secrets and prints what it found.
func Scan(ctx context.Context, repo *repository.Repository, cfg domain.Config, opts Options, p output.Printer) error {
	limit := opts.Limit
	switch {
	case limit < 0 || limit > MaxHistoryLimit:
		return apperr.Usage("--limit must be between 1 and %d", MaxHistoryLimit)
	case limit == 0:
		limit = DefaultHistoryLimit
	}
	if opts.Limit != 0 && !opts.History {
		return apperr.Usage("--limit needs --history")
	}

	res := Result{}
	if !cfg.Secrets.Enabled {
		res.Disabled = true
	} else {
		detector := secret.NewDetector(cfg.SecretRules())
		var err error
		if res, err = scanFiles(ctx, repo, detector); err != nil {
			return err
		}
		if opts.History {
			if err := scanHistory(ctx, repo, detector, limit, &res); err != nil {
				return err
			}
		}
	}

	if opts.JSON {
		if err := writeJSON(p, res); err != nil {
			return err
		}
	} else {
		render(p, res, opts.History)
	}
	if opts.Fail && (len(res.Files) > 0 || len(res.History) > 0) {
		return apperr.New(apperr.CodePolicy, fmt.Sprintf("%d likely secret%s found", len(res.Files)+len(res.History), plural(len(res.Files)+len(res.History))),
			"remove them, and rotate the ones that were committed: a secret in the history stays exposed")
	}
	return nil
}

func scanFiles(ctx context.Context, repo *repository.Repository, d *secret.Detector) (Result, error) {
	var res Result
	tracked, untracked, err := repo.ListFiles(ctx)
	if err != nil {
		return res, apperr.Wrap(apperr.CodeExternal, "cannot list the files of the repository", "", err)
	}
	isTracked := make(map[string]bool, len(tracked))
	for _, t := range tracked {
		isTracked[t] = true
	}
	all := append(append([]string{}, tracked...), untracked...)
	sort.Strings(all)
	if len(all) > maxFiles {
		all, res.Truncated = all[:maxFiles], true
	}
	for _, rel := range all {
		if err := ctx.Err(); err != nil {
			return res, apperr.Wrap(apperr.CodeGeneral, "interrupted", "", err)
		}
		findings, ok := scanFile(filepath.Join(repo.Root, filepath.FromSlash(rel)), rel, d)
		if !ok {
			res.Skipped++
			continue
		}
		res.Scanned++
		for _, f := range findings {
			if len(res.Files) >= maxFindings {
				res.Truncated = true
				return res, nil
			}
			res.Files = append(res.Files, FileFinding{Path: rel, Line: f.Line, Rule: f.Rule, Tracked: isTracked[rel]})
		}
	}
	return res, nil
}

// scanFile reads one file. ok is false when it was left out: not a regular file
// (a symlink is never followed), too big, binary, or a line too long to read.
func scanFile(abs, rel string, d *secret.Detector) (findings []secret.Finding, ok bool) {
	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileSize {
		return nil, false
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, sniffBytes)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, false
	}
	if bytes.IndexByte(head[:n], 0) >= 0 {
		return nil, false // binary
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, false
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for line := 1; sc.Scan(); line++ {
		findings = append(findings, d.ScanLine(rel, line, sc.Text())...)
	}
	if sc.Err() != nil {
		return nil, false
	}
	return findings, true
}

func scanHistory(ctx context.Context, repo *repository.Repository, d *secret.Detector, limit int, res *Result) error {
	commits, err := repo.HistoryAdditions(ctx, limit)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read the history", "", err)
	}
	res.Commits = len(commits)
	inFiles := map[[2]string]bool{}
	for _, f := range res.Files {
		inFiles[[2]string{f.Path, f.Rule}] = true
	}
	seen := map[[3]string]bool{} // one finding per commit, file and rule
	for _, c := range commits {
		for _, l := range c.Lines {
			for _, f := range d.ScanLine(l.Path, l.Line, l.Text) {
				key := [3]string{c.Hash, f.Path, f.Rule}
				if seen[key] {
					continue
				}
				seen[key] = true
				if len(res.History) >= maxFindings {
					res.Truncated = true
					return nil
				}
				res.History = append(res.History, HistoryFinding{
					Commit: c.Hash, Date: c.Date, Path: f.Path, Line: f.Line, Rule: f.Rule,
					InFiles: inFiles[[2]string{f.Path, f.Rule}],
				})
			}
		}
	}
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// clean strips control characters from a path, which is repository text.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}
