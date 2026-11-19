package pr

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
)

var shaRe = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

// ---- one commit of a request ----------------------------------------------------

type jsonCommitFile struct {
	Path         string `json:"path"`
	OldPath      string `json:"old_path"`
	Status       string `json:"status"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	Patch        string `json:"patch"`         // the hunks, from the first @@ line
	PatchMissing bool   `json:"patch_missing"` // the file changed but the provider gave no hunks: binary, or too large
}

// CommitOptions are the arguments of `gtide pr commit`.
type CommitOptions struct {
	Ref  string
	SHA  string
	JSON bool
}

// ShowCommit prints one commit of a request with the files it changed.
func ShowCommit(ctx context.Context, repo *repository.Repository, cfg domain.Config, c provider.Provider, opts CommitOptions, p output.Printer) error {
	if !shaRe.MatchString(opts.SHA) {
		return apperr.Usage("invalid commit %q: give at least 7 hexadecimal characters", opts.SHA)
	}
	reader, ok := c.(provider.CommitReader)
	if !ok {
		return apperr.New(apperr.CodeConfig, fmt.Sprintf("reading a commit of a %s is not available for %s yet", c.Noun(), providerLabel(c)), "")
	}
	hosted, _, number, err := target(ctx, repo, cfg, c, opts.Ref, p)
	if err != nil {
		return err
	}
	p.Note("Reading commit %s of %s %s in %s", shortSHA(opts.SHA), c.Noun(), ref(c, number), hosted)
	d, err := reader.CommitDetail(ctx, hosted, opts.SHA)
	if err != nil {
		return readError(err, c, hosted, "commit "+shortSHA(opts.SHA))
	}
	if opts.JSON {
		files := make([]jsonCommitFile, 0, len(d.Files))
		for _, f := range d.Files {
			files = append(files, jsonCommitFile{Path: f.Path, OldPath: f.OldPath, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions,
				Patch: cleanText(f.Patch), PatchMissing: f.Patch == "" && f.Additions+f.Deletions > 0})
		}
		date := ""
		if !d.Date.IsZero() {
			date = d.Date.UTC().Format("2006-01-02T15:04:05Z")
		}
		body, cut := cleanText(d.Body), false
		if r := []rune(body); len(r) > maxCommentJSON {
			body, cut = string(r[:maxCommentJSON]), true
		}
		return encode(p, struct {
			Number         int              `json:"number"`
			SHA            string           `json:"sha"`
			Author         string           `json:"author"`
			Name           string           `json:"name"`
			Email          string           `json:"email"`
			Date           string           `json:"date"`
			Subject        string           `json:"subject"`
			Body           string           `json:"body"`
			BodyTruncated  bool             `json:"body_truncated"`
			FilesTruncated bool             `json:"files_truncated"`
			Files          []jsonCommitFile `json:"files"`
		}{number, d.SHA, oneLine(d.Author), oneLine(d.Name), oneLine(d.Email), date, oneLine(d.Subject), body, cut, d.Cut, files})
	}
	p.Line("%s %s", p.Yellow(shortSHA(d.SHA)), p.Bold(oneLine(d.Subject)))
	p.Line("%s  %s", oneLine(d.Author), p.Dim(when(d.Date)))
	for _, f := range d.Files {
		p.Line("  %s", fileLine(p, provider.ChangedFile{Path: f.Path, OldPath: f.OldPath, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions, HasStats: true}))
	}
	return nil
}

// ---- who reviews, who is assigned, which labels --------------------------------

// EditOptions are the arguments of `gtide pr edit`.
type EditOptions struct {
	Ref             string
	AddReviewers    []string
	RemoveReviewers []string
	AddAssignees    []string
	RemoveAssignees []string
	AddLabels       []string
	RemoveLabels    []string
}

const maxLabelRunes = 50

func checkLogins(flag string, list []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range list {
		for _, one := range strings.Split(raw, ",") {
			one = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(one), "@"))
			if one == "" || seen[strings.ToLower(one)] {
				continue
			}
			if !loginRe.MatchString(one) {
				return nil, apperr.Usage("invalid %s %q", flag, one)
			}
			seen[strings.ToLower(one)] = true
			out = append(out, one)
		}
	}
	return out, nil
}

func checkLabels(flag string, list []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range list {
		for _, one := range strings.Split(raw, ",") {
			one = strings.TrimSpace(one)
			if one == "" || seen[strings.ToLower(one)] {
				continue
			}
			if len([]rune(one)) > maxLabelRunes || strings.IndexFunc(one, unicode.IsControl) >= 0 {
				return nil, apperr.Usage("invalid %s %q", flag, one)
			}
			seen[strings.ToLower(one)] = true
			out = append(out, one)
		}
	}
	return out, nil
}

// Edit changes the reviewers, the assignees and the labels of a request.
func Edit(ctx context.Context, repo *repository.Repository, cfg domain.Config, c provider.Provider, opts EditOptions, out *provider.PullEdit, p output.Printer) error {
	var e provider.PullEdit
	var err error
	for _, f := range []struct {
		dst  *[]string
		src  []string
		flag string
		fn   func(string, []string) ([]string, error)
	}{
		{&e.AddReviewers, opts.AddReviewers, "--add-reviewer", checkLogins},
		{&e.RemoveReviewers, opts.RemoveReviewers, "--remove-reviewer", checkLogins},
		{&e.AddAssignees, opts.AddAssignees, "--add-assignee", checkLogins},
		{&e.RemoveAssignees, opts.RemoveAssignees, "--remove-assignee", checkLogins},
		{&e.AddLabels, opts.AddLabels, "--add-label", checkLabels},
		{&e.RemoveLabels, opts.RemoveLabels, "--remove-label", checkLabels},
	} {
		if *f.dst, err = f.fn(f.flag, f.src); err != nil {
			return err
		}
	}
	if e.Empty() {
		return apperr.Usage("nothing to change: pass --add-reviewer, --remove-reviewer, --add-assignee, --remove-assignee, --add-label or --remove-label")
	}
	if err := needsToken(c); err != nil {
		return err
	}
	editor, ok := c.(provider.Editor)
	if !ok {
		return apperr.New(apperr.CodeConfig, fmt.Sprintf("changing a %s is not available for %s yet", c.Noun(), providerLabel(c)), "")
	}
	hosted, _, number, err := target(ctx, repo, cfg, c, opts.Ref, p)
	if err != nil {
		return err
	}
	if err := editor.Edit(ctx, hosted, number, e); err != nil {
		return readError(err, c, hosted, fmt.Sprintf("%s %s", c.Noun(), ref(c, number)))
	}
	if out != nil {
		*out = e
	}
	p.Success("Updated %s %s", c.Noun(), ref(c, number))
	return nil
}

// ChoicesOptions are the arguments of `gtide pr options`.
type ChoicesOptions struct{ JSON bool }

// Choices lists the labels of the repository and the people a request can be assigned to or sent for review.
func Choices(ctx context.Context, repo *repository.Repository, cfg domain.Config, c provider.Provider, opts ChoicesOptions, p output.Printer) error {
	editor, ok := c.(provider.Editor)
	if !ok {
		return apperr.New(apperr.CodeConfig, fmt.Sprintf("listing labels and people is not available for %s yet", providerLabel(c)), "")
	}
	hosted, err := hostedRepo(ctx, repo, cfg, c)
	if err != nil {
		return err
	}
	p.Note("Reading the labels and the people of %s", hosted)
	labels, lcut, err := editor.Labels(ctx, hosted)
	if err != nil {
		return readError(err, c, hosted, "the labels")
	}
	people, pcut, err := editor.People(ctx, hosted)
	if err != nil {
		return readError(err, c, hosted, "the people")
	}
	if lcut || pcut {
		p.Warn("the lists were cut: the repository has more than can be read")
	}
	if opts.JSON {
		names := make([]string, 0, len(people))
		for _, n := range people {
			names = append(names, oneLine(n))
		}
		return encode(p, struct {
			Labels    []jsonLabel `json:"labels"`
			People    []string    `json:"people"`
			Truncated bool        `json:"truncated"`
		}{toLabels(labels), names, lcut || pcut})
	}
	p.Line("%s", p.Bold(fmt.Sprintf("Labels (%d)", len(labels))))
	for _, l := range labels {
		p.Line("  %s  %s", oneLine(l.Name), p.Dim(oneLine(l.Description)))
	}
	p.Line("%s", p.Bold(fmt.Sprintf("People (%d)", len(people))))
	for _, n := range people {
		p.Line("  %s", oneLine(n))
	}
	return nil
}
