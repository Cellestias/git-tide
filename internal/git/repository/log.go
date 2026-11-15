package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// LogEntry is one commit as shown by `gtide history`.
type LogEntry struct {
	FullHash    string
	Hash        string // abbreviated
	AuthorName  string
	AuthorEmail string
	Date        time.Time // author date
	Subject     string
	Files       []FileChange // only with LogQuery.Files
	Patch       string       // only with LogQuery.Patch: the diff of the commit, limited to LogQuery.Path
	Truncated   bool         // Patch was cut at MaxPatchBytes
}

// MaxPatchBytes caps the patch kept for one commit, so a huge generated file
// cannot flood the output or a program reading it.
const MaxPatchBytes = 256 << 10

// LogQuery selects commits. Rev is a fully qualified ref (refs/heads/x), so a
// branch name can never be read as an option.
type LogQuery struct {
	Rev     string
	Exclude string // a ref whose commits are left out (base scoping); "" for none
	Author  string // matched as a case-insensitive plain substring of "Name <email>"
	Search  string // matched as a case-insensitive plain substring of the message
	Path    string // relative to the repository root
	Follow  bool   // follow renames of Path; Git supports it for a single file only
	Since   string // Git date, e.g. "2 weeks ago" or "2026-01-31"; "" for none
	Until   string
	Merges  bool // include merge commits
	Files   bool // fill LogEntry.Files (one extra pass over the diffs)
	Patch   bool // fill LogEntry.Patch with the diff of each commit (limited to Path)
	Limit   int  // 0 means no limit
}

// Log lists commits reachable from q.Rev, newest first.
func (r *Repository) Log(ctx context.Context, q LogQuery) ([]LogEntry, error) {
	// A patch can hold any byte, so the record separator is random per call: file
	// content cannot forge a commit boundary.
	sep := "\x1e"
	if q.Patch {
		var b [12]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		sep = "\x1e" + hex.EncodeToString(b[:]) + "\x1e"
	}
	args := []string{"-c", "core.quotepath=false", "--literal-pathspecs", "log", "--format=" + sep + "%H%x00%h%x00%an%x00%ae%x00%at%x00%s"}
	if q.Files {
		args = append(args, "--numstat")
	}
	if q.Patch {
		args = append(args, "--patch", "--no-color", "--no-ext-diff", "--no-textconv")
	}
	if q.Limit > 0 {
		args = append(args, "-n", strconv.Itoa(q.Limit))
	}
	if q.Author != "" || q.Search != "" {
		args = append(args, "--fixed-strings", "--regexp-ignore-case")
	}
	if q.Author != "" {
		args = append(args, "--author="+q.Author)
	}
	if q.Search != "" {
		args = append(args, "--grep="+q.Search)
	}
	if q.Since != "" {
		args = append(args, "--since="+q.Since)
	}
	if q.Until != "" {
		args = append(args, "--until="+q.Until)
	}
	if !q.Merges {
		args = append(args, "--no-merges")
	}
	if q.Path != "" && q.Follow {
		args = append(args, "--follow")
	}
	args = append(args, q.Rev)
	if q.Exclude != "" {
		args = append(args, "^"+q.Exclude)
	}
	args = append(args, "--")
	if q.Path != "" {
		args = append(args, q.Path)
	}
	out, err := r.git(ctx, args...)
	if err != nil {
		return nil, err
	}

	var entries []LogEntry
	for _, rec := range strings.Split(out, sep) {
		if strings.TrimSpace(rec) == "" {
			continue
		}
		head, numstat, _ := strings.Cut(rec, "\n")
		f := strings.Split(head, "\x00")
		if len(f) != 6 {
			return nil, fmt.Errorf("unexpected log output %q", head)
		}
		e := LogEntry{FullHash: f[0], Hash: f[1], AuthorName: f[2], AuthorEmail: f[3], Subject: f[5]}
		if ts, err := strconv.ParseInt(f[4], 10, 64); err == nil {
			e.Date = time.Unix(ts, 0)
		}
		if q.Patch {
			numstat, e.Patch, e.Truncated = splitPatch(numstat)
		}
		e.Files = parseNumstat(numstat)
		entries = append(entries, e)
	}
	return entries, nil
}

// splitPatch separates the numstat lines from the diff that follows them. A
// numstat line starts with a digit or "-", so "diff --git" marks the patch.
func splitPatch(text string) (numstat, patch string, truncated bool) {
	i := 0
	if !strings.HasPrefix(text, "diff --git ") {
		j := strings.Index(text, "\ndiff --git ")
		if j < 0 {
			return text, "", false
		}
		i = j + 1
	}
	numstat, patch = text[:i], strings.TrimRight(text[i:], "\n")+"\n"
	if len(patch) > MaxPatchBytes {
		cut := strings.LastIndex(patch[:MaxPatchBytes], "\n") + 1
		patch, truncated = patch[:cut], true
	}
	return numstat, patch, truncated
}

// parseNumstat reads `git --numstat` lines: "added<TAB>deleted<TAB>path", with
// "-" for both counts on a binary file.
func parseNumstat(text string) []FileChange {
	var files []FileChange
	for _, line := range strings.Split(text, "\n") {
		c := strings.SplitN(line, "\t", 3)
		if len(c) != 3 {
			continue
		}
		fc := FileChange{Path: c[2]}
		if c[0] == "-" {
			fc.Binary = true
		} else {
			fc.Added, _ = strconv.Atoi(c[0])
			fc.Deleted, _ = strconv.Atoi(c[1])
		}
		files = append(files, fc)
	}
	return files
}

// FileChange is one file of a commit with its added and deleted line counts.
// Binary files have no counts.
type FileChange struct {
	Path    string
	Added   int
	Deleted int
	Binary  bool
}

// CommitDetail is a commit with its message body and changed files.
type CommitDetail struct {
	LogEntry
	Body  string
	Files []FileChange
}

// Show returns the details of the commit rev. For a merge commit the files are
// the changes against its first parent.
func (r *Repository) Show(ctx context.Context, rev string) (CommitDetail, error) {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return CommitDetail{}, fmt.Errorf("invalid revision %q", rev)
	}
	format := "--format=%H%x00%h%x00%an%x00%ae%x00%at%x00%s%x00%b%x1e"
	out, err := r.git(ctx, "-c", "core.quotepath=false", "show", "--numstat", "--no-renames", "-m", "--first-parent", format, rev, "--")
	if err != nil {
		return CommitDetail{}, err
	}
	head, files, ok := strings.Cut(out, "\x1e")
	if !ok {
		return CommitDetail{}, fmt.Errorf("unexpected show output")
	}
	f := strings.SplitN(head, "\x00", 7)
	if len(f) != 7 {
		return CommitDetail{}, fmt.Errorf("unexpected show output")
	}
	d := CommitDetail{
		Body:     strings.TrimSpace(f[6]),
		LogEntry: LogEntry{FullHash: f[0], Hash: f[1], AuthorName: f[2], AuthorEmail: f[3], Subject: f[5]},
	}
	if ts, err := strconv.ParseInt(f[4], 10, 64); err == nil {
		d.Date = time.Unix(ts, 0)
	}
	d.Files = parseNumstat(files)
	return d, nil
}
