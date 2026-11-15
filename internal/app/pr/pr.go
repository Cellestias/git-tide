// Package pr implements `gtide pr`: push the current branch and open a Pull
// Request (GitHub) or Merge Request (GitLab) against its configured base.
package pr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	appsecurity "github.com/cellestias/git-tide/internal/app/security"
	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/domain/branch"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/domain/policy"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
)

const (
	maxTitle       = 200
	maxBodyCommits = 50
)

type Options struct {
	Base  string // overrides the base from configuration
	Title string // overrides the generated title
	Body  string // overrides the generated body
	Draft bool
	// Confirm is asked before anything is pushed. Nil means "proceed".
	Confirm func(prompt string) (bool, error)
	// Ask reads one line of free text (the title and body prompts). Nil means
	// non-interactive: description.pr.md is used when present, else the defaults.
	Ask func(prompt string) (string, error)
	// Security is the alerts check before the branch is pushed. It gets the files
	// the push changes and may stop it. Nil means no check.
	Security func(ctx context.Context, touched []string) error
}

func Run(ctx context.Context, repo *repository.Repository, cfg domain.Config, c provider.Provider, opts Options, p output.Printer) error {
	noun := c.Noun()
	ghRepo, err := hostedRepo(ctx, repo, cfg, c)
	if err != nil {
		return err
	}

	current, err := repo.CurrentBranch(ctx)
	if errors.Is(err, repository.ErrDetachedHead) {
		return apperr.New(apperr.CodeUnsafeRepo, "HEAD is detached; there is no branch to open a "+noun+" from",
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
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("%q is the base branch; nothing to open a "+noun+" from", current),
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

	if opts.Security != nil {
		touched := appsecurity.Touched(ctx, repo, cfg, cfg.Remote, current, "refs/heads/"+current)
		if err := opts.Security(ctx, touched); err != nil {
			return err
		}
		defer appsecurity.MarkChecked()()
	}

	title, body, err := compose(repo.Root, current, base, subjects, opts, p)
	if err != nil {
		return err
	}

	p.Line("%s %s → %s in %s%s", capitalize(noun), current, base, ghRepo, draftNote(opts.Draft))
	p.Info("title:   %s", clean(title))
	p.Info("commits: %d", n)
	if opts.Confirm != nil {
		ok, err := opts.Confirm(fmt.Sprintf("Push %s to %s and open the %s? [y/N] ", current, cfg.Remote, noun))
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

	p.Step("Opening %s", noun)
	created, err := c.CreatePullRequest(ctx, ghRepo, provider.NewPullRequest{
		Title: title, Body: body, Head: current, Base: base, Draft: opts.Draft,
	})
	var verr *provider.ErrValidation
	switch {
	case errors.As(err, &verr):
		// the message is remote-controlled: use the cleaned text, not the wrapped cause
		return apperr.New(apperr.CodeExternal, fmt.Sprintf("%s rejected the %s: %s", providerLabel(c), noun, clean(verr.Message)),
			"the branch was pushed; fix the problem and re-run `gtide pr`")
	case errors.Is(err, provider.ErrNotFound):
		return apperr.New(apperr.CodeExternal, fmt.Sprintf("%s not found or the token cannot write to it", ghRepo),
			c.TokenEnv()+" needs "+noun+" write access; the branch was pushed")
	case err != nil:
		return apperr.Wrap(apperr.CodeExternal, "cannot create "+noun, "the branch was pushed; re-run `gtide pr`", err)
	}
	p.Success("Opened %s: %s", ref(c, created.Number), clean(created.URL))
	return nil
}

// compose decides the title and body. Flags win. Otherwise description.pr.md,
// when present and accepted, then what the user types at the prompts, then the
// defaults derived from the commits.
func compose(root, current, base string, subjects []string, opts Options, p output.Printer) (title, body string, err error) {
	title, body = opts.Title, opts.Body
	if title == "" || body == "" {
		tpl, found, err := LoadTemplate(root)
		if err != nil {
			return "", "", err
		}
		useFile := found
		if found {
			tpl = tpl.Render(Vars{Branch: current, Base: base, Commits: subjects})
			if opts.Ask != nil && opts.Confirm != nil {
				p.Line("Found %s:", TemplateFile)
				p.Line("%s", tpl.Preview())
				if useFile, err = opts.Confirm("Build the pull request from " + TemplateFile + "? [y/N] "); err != nil {
					return "", "", apperr.Wrap(apperr.CodeGeneral, "cannot read confirmation", "use --yes to skip the prompt", err)
				}
			}
		}
		switch {
		case useFile:
			if title == "" {
				title = tpl.Title
			}
			if body == "" {
				body = tpl.Body
			}
		case opts.Ask != nil:
			if title == "" {
				if title, err = ask(opts.Ask, fmt.Sprintf("Title (Enter for %q): ", ellipsize(clean(DefaultTitle(current, subjects)), 60))); err != nil {
					return "", "", err
				}
				title = truncate(strings.Join(strings.Fields(clean(title)), " "))
			}
			if body == "" {
				if body, err = ask(opts.Ask, "Body (Enter for the commit list): "); err != nil {
					return "", "", err
				}
				body = cleanText(body)
			}
		}
	}
	if strings.TrimSpace(title) == "" {
		title = DefaultTitle(current, subjects)
	}
	if strings.TrimSpace(body) == "" {
		body = DefaultBody(subjects)
	}
	return title, body, nil
}

func ask(fn func(string) (string, error), prompt string) (string, error) {
	answer, err := fn(prompt)
	if err != nil {
		return "", apperr.Wrap(apperr.CodeGeneral, "cannot read the answer", "use --title and --body, or --yes, to skip the prompts", err)
	}
	return strings.TrimSpace(answer), nil
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

// providerLabel is the display name of the hosting service.
func providerLabel(p provider.Provider) string {
	if p.Name() == provider.GitLab {
		return "GitLab"
	}
	return "GitHub"
}

// ref renders a request number the way the provider does: #12 or !12.
func ref(p provider.Provider, n int) string {
	if p.Name() == provider.GitLab {
		return fmt.Sprintf("!%d", n)
	}
	return fmt.Sprintf("#%d", n)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
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
