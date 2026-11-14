// Package review implements `gtide review`.
package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/domain/branch"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
)

type Options struct {
	// Ref is a PR number, "#number", or URL.
	Ref string
	// Reset discards local commits on an existing review branch.
	Reset bool
	// Diff prints the full patch against the base branch, not only the summary.
	Diff bool
	// Confirm is asked before --reset discards local commits. Nil means "proceed".
	Confirm func(prompt string) (bool, error)
}

const maxListedLost = 10

// BranchName is the predictable local branch used to review a PR.
func BranchName(number int) string { return fmt.Sprintf("tide/review/pr-%d", number) }

func Run(ctx context.Context, repo *repository.Repository, cfg domain.Config, prs provider.Provider, opts Options, p output.Printer) error {
	ref, err := prs.ParseRef(opts.Ref)
	if err != nil {
		return apperr.Usage("%v", err)
	}

	noun := prs.Noun()
	ghRepo, err := provider.RepoFromRemote(ctx, repo, prs, cfg.Remote)
	if errors.Is(err, provider.ErrNoRemote) {
		return apperr.Wrap(apperr.CodeConfig, fmt.Sprintf("remote %q is not configured", cfg.Remote),
			"add it with `gtide remote add <url>` or change `remote` in .tide.yml", err)
	}
	if err != nil {
		return apperr.Wrap(apperr.CodeConfig, fmt.Sprintf("remote %q is not a supported %s remote", cfg.Remote, prs.Name()),
			"check the remote URL, or set `provider` in .tide.yml", err)
	}
	if ref.Repo != nil && !strings.EqualFold(ref.Repo.String(), ghRepo.String()) {
		return apperr.Usage(noun+" belongs to %s but remote %q is %s", ref.Repo, cfg.Remote, ghRepo)
	}

	current, err := repo.CurrentBranch(ctx)
	if err != nil && !errors.Is(err, repository.ErrDetachedHead) {
		return apperr.Wrap(apperr.CodeExternal, "cannot determine current branch", "", err)
	}
	if err := ensureSafe(ctx, repo); err != nil {
		return err
	}

	p.Step("Reading %s#%d", ghRepo, ref.Number)
	pr, err := prs.GetPullRequest(ctx, ghRepo, ref.Number)
	if errors.Is(err, provider.ErrNotFound) {
		return apperr.New(apperr.CodeExternal, fmt.Sprintf("%s#%d not found or not accessible", ghRepo, ref.Number),
			"for private repositories set "+prs.TokenEnv())
	}
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read "+noun, "", err)
	}

	hidden := fmt.Sprintf("refs/tide/pr/%d", ref.Number)
	p.Step("Fetching the %s head", noun)
	if err := repo.Fetch(ctx, cfg.Remote, prs.ReviewRefspec(ref.Number, hidden)); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot fetch the "+noun+" head", "check your network and credentials", err)
	}

	name := BranchName(ref.Number)
	if opts.Reset {
		if err := confirmReset(ctx, repo, name, hidden, noun, opts, p); err != nil {
			return err
		}
	}
	if err := checkoutReview(ctx, repo, name, hidden, current, opts.Reset, noun); err != nil {
		return err
	}

	p.Success("On review branch %s", name)
	p.Info("title:     %s", clean(pr.Title))
	p.Info("author:    %s", clean(pr.Author))
	p.Info("state:     %s%s", clean(pr.State), draftNote(pr.Draft))
	p.Info("source:    %s%s", clean(pr.HeadRef), forkNote(pr, ghRepo))
	p.Info("target:    %s", clean(pr.BaseRef))
	if len(pr.Assignees) > 0 {
		p.Info("assignees: %s", clean(strings.Join(pr.Assignees, ", ")))
	}
	if rv, err := prs.Reviews(ctx, ghRepo, ref.Number); err == nil {
		p.Info("review:    %s", reviewSummary(rv, pr.Reviewers))
	}
	p.Info("url:       %s", clean(pr.URL))

	showDiff(ctx, repo, cfg, pr, opts, p)
	if !opts.Diff {
		p.Info("next:      git diff %s/%s...HEAD    (return with: git switch %s)", cfg.Remote, clean(pr.BaseRef), orDash(current))
	}
	return nil
}

// confirmReset warns about, and asks to discard, local commits on an existing
// review branch that are not part of the request. It never discards silently.
func confirmReset(ctx context.Context, repo *repository.Repository, name, hidden, noun string, opts Options, p output.Printer) error {
	exists, err := repo.LocalBranchExists(ctx, name)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect local branches", "", err)
	}
	if !exists {
		return nil
	}
	n, subjects, err := repo.Ahead(ctx, hidden, "refs/heads/"+name)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot compare the review branch with the "+noun, "", err)
	}
	if n == 0 {
		return nil
	}
	p.Warn("--reset will discard %d local commit(s) on %s that are not part of the %s:", n, name, noun)
	for i, s := range subjects {
		if i == maxListedLost {
			p.Warn("  … and %d more", len(subjects)-maxListedLost)
			break
		}
		p.Warn("  - %s", clean(s))
	}
	if tip, err := repo.ShortRev(ctx, "refs/heads/"+name); err == nil {
		p.Warn("the current tip is %s; to keep the work run: git branch <new-name> %s", tip, tip)
	}
	if opts.Confirm == nil {
		return nil
	}
	ok, err := opts.Confirm(fmt.Sprintf("Discard them and reset %s to the %s? [y/N] ", name, noun))
	if err != nil {
		return apperr.Wrap(apperr.CodeGeneral, "cannot read confirmation", "use --yes to skip the prompt", err)
	}
	if !ok {
		return apperr.New(apperr.CodeGeneral, "aborted; the review branch was left untouched", "")
	}
	return nil
}

// showDiff prints what the request changes relative to its base: always a
// per-file summary, and the whole patch with --diff. It degrades to a warning.
func showDiff(ctx context.Context, repo *repository.Repository, cfg domain.Config, pr provider.PullRequest, opts Options, p output.Printer) {
	if err := branch.ValidName(pr.BaseRef); err != nil {
		p.Warn("cannot show the diff: the base branch name is not valid")
		return
	}
	base := "refs/remotes/" + cfg.Remote + "/" + pr.BaseRef
	if err := repo.Fetch(ctx, cfg.Remote, "+refs/heads/"+pr.BaseRef+":"+base); err != nil {
		p.Warn("cannot fetch %s/%s, so no diff is shown: %v", cfg.Remote, clean(pr.BaseRef), err)
		return
	}
	stat, err := repo.DiffStat(ctx, base, "HEAD")
	if err != nil {
		p.Warn("cannot compute the diff: %v", err)
		return
	}
	if strings.TrimSpace(stat) == "" {
		p.Info("changes:   none relative to %s/%s", cfg.Remote, clean(pr.BaseRef))
		return
	}
	p.Line("")
	p.Line("Changes relative to %s/%s:", cfg.Remote, clean(pr.BaseRef))
	p.Line("%s", strings.TrimRight(cleanText(stat), "\n"))
	if !opts.Diff {
		return
	}
	patch, err := repo.Diff(ctx, base, "HEAD")
	if err != nil {
		p.Warn("cannot compute the diff: %v", err)
		return
	}
	p.Line("")
	p.Line("%s", strings.TrimRight(cleanText(patch), "\n"))
}

func reviewSummary(rv provider.Review, requested []string) string {
	var parts []string
	switch rv.State {
	case provider.ReviewChangesRequested:
		parts = append(parts, "changes requested by "+strings.Join(rv.ChangesRequested, ", "))
	case provider.ReviewApproved:
		parts = append(parts, "approved by "+strings.Join(rv.Approvals, ", "))
	default:
		parts = append(parts, "review required")
	}
	if len(requested) > 0 {
		parts = append(parts, "waiting on "+strings.Join(requested, ", "))
	}
	return clean(strings.Join(parts, "; "))
}

func forkNote(pr provider.PullRequest, hosted provider.Repo) string {
	if strings.EqualFold(pr.HeadRepo, hosted.String()) {
		return ""
	}
	if pr.HeadRepo != "" {
		return " (fork: " + clean(pr.HeadRepo) + ")"
	}
	return " (fork)"
}

func draftNote(draft bool) string {
	if draft {
		return " (draft)"
	}
	return ""
}

// cleanText is clean for multi-line git output: it keeps newlines and tabs.
func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

func checkoutReview(ctx context.Context, repo *repository.Repository, name, ref, current string, reset bool, noun string) error {
	exists, err := repo.LocalBranchExists(ctx, name)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect local branches", "", err)
	}
	wrap := func(err error) error {
		return apperr.Wrap(apperr.CodeExternal, "cannot check out the review branch", "", err)
	}
	switch {
	case !exists:
		if err := repo.CheckoutBranchAt(ctx, name, ref, false); err != nil {
			return wrap(err)
		}
	case reset:
		if err := repo.CheckoutBranchAt(ctx, name, ref, true); err != nil {
			return wrap(err)
		}
	default:
		ok, err := repo.IsAncestor(ctx, "refs/heads/"+name, ref)
		if err != nil {
			return wrap(err)
		}
		if !ok {
			return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("local %s has commits that are not in the %s", name, noun),
				"re-run with --reset to discard them, or rename the branch to keep them")
		}
		if current != name {
			if err := repo.Checkout(ctx, name); err != nil {
				return wrap(err)
			}
		}
		if err := repo.MergeFastForward(ctx, ref); err != nil {
			return wrap(err)
		}
	}
	return nil
}

func ensureSafe(ctx context.Context, repo *repository.Repository) error {
	op, err := repo.InProgressOperation(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect repository state", "", err)
	}
	if op != "" {
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("a %s is in progress", op),
			fmt.Sprintf("finish or abort it first (git %s --continue / --abort)", op))
	}
	changes, err := repo.TrackedChanges(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read working tree status", "", err)
	}
	if len(changes) > 0 {
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("%d file(s) have uncommitted changes", len(changes)),
			"commit or stash them before switching branches")
	}
	return nil
}

// clean strips control characters so PR-controlled text cannot inject
// terminal escape sequences.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

func orDash(s string) string {
	if s == "" {
		return "<previous branch>"
	}
	return s
}
