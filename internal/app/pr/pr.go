// Package pr implements `tide pr`: push the current branch and open a GitHub
// Pull Request against its configured base.
package pr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/domain/branch"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/domain/policy"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/github"
	"github.com/cellestias/git-tide/internal/output"
)

const (
	maxTitle       = 200
	maxBodyCommits = 50
)

// Creator is the slice of the GitHub client this use case needs.
type Creator interface {
	CreatePullRequest(ctx context.Context, repo github.Repo, npr github.NewPullRequest) (github.PullRequest, error)
}

type Options struct {
	Base  string // overrides the base from configuration
	Title string // overrides the generated title
	Body  string // overrides the generated body
	Draft bool
	// Confirm is asked before anything is pushed. Nil means "proceed".
	Confirm func(prompt string) (bool, error)
}

func Run(ctx context.Context, repo *repository.Repository, cfg domain.Config, c Creator, opts Options, p output.Printer) error {
	ghRepo, err := github.RepoFromRemote(ctx, repo, cfg.Remote)
	if errors.Is(err, github.ErrNoRemote) {
		return apperr.Wrap(apperr.CodeConfig, fmt.Sprintf("remote %q is not configured", cfg.Remote),
			"add it with `git remote add` or change `remote` in .tide.yml", err)
	}
	if err != nil {
		return apperr.Wrap(apperr.CodeConfig, fmt.Sprintf("remote %q is not a supported GitHub remote", cfg.Remote),
			"only github.com remotes are supported", err)
	}

	current, err := repo.CurrentBranch(ctx)
	if errors.Is(err, repository.ErrDetachedHead) {
		return apperr.New(apperr.CodeUnsafeRepo, "HEAD is detached; there is no branch to open a pull request from",
			"create one first: git switch -c <branch>")
	}
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot determine current branch", "", err)
	}

	base := opts.Base
	if base == "" {
		base = cfg.BaseFor(current)
	} else if err := branch.ValidName(base); err != nil {
		return apperr.Usage("invalid base branch: %v", err)
	}
	if current == base {
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("%q is the base branch; nothing to open a pull request from", current),
			"switch to a working branch, or pass --base")
	}
	if d := (policy.Protection{Protected: cfg.Branches.Protected}).Push(current); !d.Allowed {
		return apperr.New(apperr.CodePolicy, d.Reason, "")
	}

	baseRef := "refs/remotes/" + cfg.Remote + "/" + base
	p.Step("Fetching %s/%s", cfg.Remote, base)
	if err := repo.Fetch(ctx, cfg.Remote, "+refs/heads/"+base+":"+baseRef); err != nil {
		return apperr.Wrap(apperr.CodeExternal, fmt.Sprintf("cannot fetch %s/%s", cfg.Remote, base),
			"check the base branch name, your network, and credentials", err)
	}

	n, subjects, err := repo.Ahead(ctx, baseRef, "refs/heads/"+current)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot compare with the base branch", "", err)
	}
	if n == 0 {
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("%s has no commits ahead of %s/%s", current, cfg.Remote, base),
			"commit your work first")
	}
	if changes, _ := repo.TrackedChanges(ctx); len(changes) > 0 {
		p.Warn("%d file(s) have uncommitted changes; they are not part of the pull request", len(changes))
	}

	title := opts.Title
	if title == "" {
		title = DefaultTitle(current, subjects)
	}
	body := opts.Body
	if body == "" {
		body = DefaultBody(subjects)
	}

	p.Line("Pull request %s → %s in %s%s", current, base, ghRepo, draftNote(opts.Draft))
	p.Info("title:   %s", clean(title))
	p.Info("commits: %d", n)
	if opts.Confirm != nil {
		ok, err := opts.Confirm(fmt.Sprintf("Push %s to %s and open the pull request? [y/N] ", current, cfg.Remote))
		if err != nil {
			return apperr.Wrap(apperr.CodeGeneral, "cannot read confirmation", "use --yes to skip the prompt", err)
		}
		if !ok {
			return apperr.New(apperr.CodeGeneral, "aborted; nothing was pushed", "")
		}
	}

	p.Step("Pushing %s to %s", current, cfg.Remote)
	if err := repo.PushBranch(ctx, cfg.Remote, current); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "push failed", "a pre-push hook or the remote rejected it; see the message above", err)
	}

	p.Step("Opening pull request")
	created, err := c.CreatePullRequest(ctx, ghRepo, github.NewPullRequest{
		Title: title, Body: body, Head: current, Base: base, Draft: opts.Draft,
	})
	var verr *github.ErrValidation
	switch {
	case errors.As(err, &verr):
		// the message is remote-controlled: use the cleaned text, not the wrapped cause
		return apperr.New(apperr.CodeExternal, "GitHub rejected the pull request: "+clean(verr.Message),
			"the branch was pushed; fix the problem and re-run `tide pr`")
	case errors.Is(err, github.ErrNotFound):
		return apperr.New(apperr.CodeExternal, fmt.Sprintf("%s not found or the token cannot write to it", ghRepo),
			"GITHUB_TOKEN needs pull-request write access; the branch was pushed")
	case err != nil:
		return apperr.Wrap(apperr.CodeExternal, "cannot create pull request", "the branch was pushed; re-run `tide pr`", err)
	}
	p.Success("Opened #%d: %s", created.Number, clean(created.URL))
	return nil
}

// DefaultTitle is the commit subject when the branch has a single commit;
// otherwise it is derived from the branch name ("feature/login-page" →
// "Login page").
func DefaultTitle(branchName string, subjects []string) string {
	if len(subjects) == 1 {
		return truncate(subjects[0])
	}
	name := branchName
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Join(strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' }), " ")
	if name == "" {
		return truncate(branchName)
	}
	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])
	return truncate(string(r))
}

// DefaultBody lists the commit subjects.
func DefaultBody(subjects []string) string {
	var b strings.Builder
	for i, s := range subjects {
		if i == maxBodyCommits {
			fmt.Fprintf(&b, "- … and %d more\n", len(subjects)-maxBodyCommits)
			break
		}
		fmt.Fprintf(&b, "- %s\n", s)
	}
	return b.String()
}

func truncate(s string) string {
	if r := []rune(s); len(r) > maxTitle {
		return string(r[:maxTitle-1]) + "…"
	}
	return s
}

func draftNote(draft bool) string {
	if draft {
		return " (draft)"
	}
	return ""
}

// clean strips control characters so remote-controlled text cannot inject
// terminal escape sequences.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
