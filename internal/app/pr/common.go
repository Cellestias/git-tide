package pr

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/provider"
)

// hostedRepo identifies the repository behind the configured remote.
func hostedRepo(ctx context.Context, repo *repository.Repository, cfg domain.Config, c provider.Provider) (provider.Repo, error) {
	r, err := provider.RepoFromRemote(ctx, repo, c, cfg.Remote)
	if errors.Is(err, provider.ErrNoRemote) {
		return provider.Repo{}, apperr.Wrap(apperr.CodeConfig, fmt.Sprintf("remote %q is not configured", cfg.Remote),
			"add it with `git remote add` or change `remote` in .tide.yml", err)
	}
	if err != nil {
		return provider.Repo{}, apperr.Wrap(apperr.CodeConfig, fmt.Sprintf("remote %q is not a supported %s remote", cfg.Remote, c.Name()),
			"check the remote URL, or set `provider` in .tide.yml", err)
	}
	return r, nil
}

// readError turns a failed read from the provider into a command error.
func readError(err error, c provider.Provider, hosted provider.Repo, what string) error {
	if errors.Is(err, provider.ErrNotFound) {
		return apperr.New(apperr.CodeExternal, fmt.Sprintf("%s not found or not accessible in %s", what, hosted),
			"for private repositories set "+c.TokenEnv())
	}
	var verr *provider.ErrValidation
	if errors.As(err, &verr) {
		return apperr.New(apperr.CodeExternal, fmt.Sprintf("%s rejected the request: %s", providerLabel(c), clean(verr.Message)), "")
	}
	return apperr.Wrap(apperr.CodeExternal, "cannot read "+what, "", err)
}

var loginRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,99}$`)

// resolveUser expands "me" to the token's login and validates any other login.
func resolveUser(ctx context.Context, c provider.Provider, flag, who string) (string, error) {
	if who == "" {
		return "", nil
	}
	if !strings.EqualFold(who, "me") {
		if !loginRe.MatchString(who) {
			return "", apperr.Usage("invalid %s %q", flag, who)
		}
		return who, nil
	}
	if !c.HasToken() {
		return "", apperr.New(apperr.CodeConfig, flag+" me needs a token to know who you are",
			"set "+c.TokenEnv()+" (or provider_token in secret.tide.yml), or pass a login name")
	}
	login, err := c.CurrentUser(ctx)
	if err != nil {
		return "", apperr.Wrap(apperr.CodeExternal, "cannot determine the token's user", "check "+c.TokenEnv(), err)
	}
	return login, nil
}

// reviewLine describes the review state in one phrase.
func reviewLine(rv provider.Review, requested []string) string {
	var parts []string
	switch rv.State {
	case provider.ReviewChangesRequested:
		parts = append(parts, "changes requested by "+strings.Join(rv.ChangesRequested, ", "))
	case provider.ReviewApproved:
		parts = append(parts, "approved by "+strings.Join(rv.Approvals, ", "))
	default:
		if len(rv.Approvals) > 0 {
			parts = append(parts, "approvals so far: "+strings.Join(rv.Approvals, ", ")+"; more needed")
		} else {
			parts = append(parts, "review required")
		}
	}
	if len(requested) > 0 {
		parts = append(parts, "waiting on "+strings.Join(requested, ", "))
	}
	return clean(strings.Join(parts, "; "))
}

// stateLabel is the state shown to the user, with the draft marker.
func stateLabel(pr provider.PullRequest) string {
	if pr.State == provider.StateOpen && pr.Draft {
		return "open (draft)"
	}
	return pr.State
}

func joinOrDash(list []string) string {
	if len(list) == 0 {
		return "-"
	}
	return clean(strings.Join(list, ", "))
}

func when(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

// cleanText is clean for multi-line text: it keeps newlines and tabs.
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

// ellipsize shortens s to at most n runes.
func ellipsize(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// shortSHA abbreviates a commit hash for display.
func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
