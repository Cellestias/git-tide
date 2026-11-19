// Package provider defines the contract every Git hosting adapter (GitHub,
// GitLab, ...) implements, plus the types commands exchange with it. Commands
// depend on this package only, never on a concrete adapter, so a new provider
// needs no change to the public command interface.
package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Provider names accepted in `provider:` of .tide.yml.
const (
	Auto   = "auto"
	GitHub = "github"
	GitLab = "gitlab"
)

// Repo identifies a hosted repository. For GitLab, Owner may be a nested group
// path such as "group/subgroup".
type Repo struct {
	Owner string
	Name  string
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

// PullRequest is the provider-neutral view of a Pull Request (GitHub) or Merge
// Request (GitLab).
type PullRequest struct {
	Number    int
	Title     string
	Body      string
	State     string // StateOpen, StateClosed or StateMerged
	Draft     bool
	URL       string
	Author    string
	Assignees []string
	Reviewers []string // reviews still requested
	HeadRef   string
	HeadSHA   string // the newest commit of the source branch
	BaseRef   string
	HeadRepo  string // full path of the head repository; empty if unknown or deleted
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Request states, the same on every provider.
const (
	StateOpen   = "open"
	StateClosed = "closed"
	StateMerged = "merged"
	StateAll    = "all" // only valid as a filter
)

// ValidState reports whether s is a state a list can be filtered by.
func ValidState(s string) bool {
	switch s {
	case StateOpen, StateClosed, StateMerged, StateAll:
		return true
	}
	return false
}

// ListOptions filters ListPullRequests. Empty fields do not filter; State
// defaults to open. Author and Assignee are login names (never "me": callers
// resolve that with CurrentUser). Head limits the list to requests whose
// source branch lives in the repository itself.
type ListOptions struct {
	State    string
	Author   string
	Assignee string
	Head     string
	Limit    int // 0 means the adapter's cap
}

// ChangedFile is one file a pull request touches. Status is added, modified,
// deleted or renamed. HasStats is false when the provider reports no line counts.
type ChangedFile struct {
	Path      string
	OldPath   string // set for renames
	Status    string
	Additions int
	Deletions int
	HasStats  bool
}

// CommitSummary is a commit of a pull request, oldest first in a list.
type CommitSummary struct {
	SHA     string
	Subject string
}

// Review states, derived from the individual reviews.
const (
	ReviewPending          = ""                  // nobody approved or asked for changes yet
	ReviewApproved         = "approved"          // approved and nothing blocks it
	ReviewChangesRequested = "changes requested" // someone asked for changes
)

// Review summarises what reviewers decided.
type Review struct {
	State            string
	Approvals        []string
	ChangesRequested []string
}

// NewPullRequest describes a pull/merge request to create.
type NewPullRequest struct {
	Title string
	Body  string
	Head  string // branch in the same repository
	Base  string
	Draft bool
}

// Ref is a parsed pull/merge request identifier. Repo is nil for bare numbers.
type Ref struct {
	Number int
	Repo   *Repo
}

// ErrNotFound means the request or repository does not exist or is not accessible.
var ErrNotFound = errors.New("pull request not found or not accessible")

// ErrValidation means the provider rejected a request, for example because one
// already exists for the branch. The message comes from the provider and is
// safe to show once control characters are stripped.
type ErrValidation struct{ Message string }

func (e *ErrValidation) Error() string { return "the provider rejected the request: " + e.Message }

// Provider is the API a hosting adapter offers.
type Provider interface {
	// Name is the provider key: "github" or "gitlab".
	Name() string
	// Noun is what the provider calls a pull request: "pull request" or "merge request".
	Noun() string
	// TokenEnv is the conventional environment variable for the token.
	TokenEnv() string
	// HasToken reports whether credentials are available.
	HasToken() bool

	ParseRemoteURL(raw string) (Repo, error)
	ParseRef(s string) (Ref, error)
	// ReviewRefspec returns the refspec that fetches request number into dst.
	ReviewRefspec(number int, dst string) string

	GetPullRequest(ctx context.Context, repo Repo, number int) (PullRequest, error)
	CreatePullRequest(ctx context.Context, repo Repo, npr NewPullRequest) (PullRequest, error)
	ListOpenPullRequests(ctx context.Context, repo Repo) (prs []PullRequest, truncated bool, err error)
	// ListPullRequests lists requests newest first, filtered by opts.
	ListPullRequests(ctx context.Context, repo Repo, opts ListOptions) (prs []PullRequest, truncated bool, err error)
	// CurrentUser is the login the token belongs to; it needs a token.
	CurrentUser(ctx context.Context) (string, error)
	ChangedFiles(ctx context.Context, repo Repo, number int) (files []ChangedFile, truncated bool, err error)
	// Commits returns the request's commits, oldest first.
	Commits(ctx context.Context, repo Repo, number int) (commits []CommitSummary, truncated bool, err error)
	Reviews(ctx context.Context, repo Repo, number int) (Review, error)
	ListProtectedBranches(ctx context.Context, repo Repo) (protected map[string]bool, truncated bool, err error)
}

// DetectName infers the provider from a remote URL (HTTPS, ssh:// or scp-like).
// It returns "" when the host is not recognised; self-hosted instances whose
// hostname does not start with "gitlab." must set `provider: gitlab` explicitly.
func DetectName(rawURL string) string {
	host := strings.ToLower(Host(rawURL))
	switch {
	case host == "github.com" || host == "www.github.com":
		return GitHub
	case host == "gitlab.com" || host == "www.gitlab.com" || strings.HasPrefix(host, "gitlab."):
		return GitLab
	}
	return ""
}

// Host extracts the hostname from a remote URL, or "".
func Host(rawURL string) string {
	raw := strings.TrimSpace(rawURL)
	if !strings.Contains(raw, "://") {
		// scp-like: [user@]host:path
		if i := strings.Index(raw, ":"); i > 0 {
			h := raw[:i]
			if at := strings.LastIndex(h, "@"); at >= 0 {
				h = h[at+1:]
			}
			return h
		}
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// Errors returned by RepoFromRemote.
var (
	ErrNoRemote          = errors.New("remote is not configured")
	ErrUnsupportedRemote = errors.New("remote is not a supported remote")
)

// RemoteReader is the part of the repository service RepoFromRemote needs.
type RemoteReader interface {
	RemoteURL(ctx context.Context, remote string) (string, error)
	RemoteConfiguredURL(ctx context.Context, remote string) (string, error)
}

// RepoFromRemote identifies the hosted repository behind a Git remote. Git may
// rewrite the URL through url.<base>.insteadOf into something that is not
// provider-shaped, so the URL as written is tried as a fallback.
func RepoFromRemote(ctx context.Context, r RemoteReader, p Provider, remote string) (Repo, error) {
	resolved, err := r.RemoteURL(ctx, remote)
	if err != nil {
		return Repo{}, fmt.Errorf("%w: %q", ErrNoRemote, remote)
	}
	repo, perr := p.ParseRemoteURL(resolved)
	if perr == nil {
		return repo, nil
	}
	if raw, err := r.RemoteConfiguredURL(ctx, remote); err == nil {
		if repo, err := p.ParseRemoteURL(raw); err == nil {
			return repo, nil
		}
	}
	return Repo{}, fmt.Errorf("%w: %w", ErrUnsupportedRemote, perr)
}

// RemoteURLs returns the resolved and as-written URLs of remote (either may be "").
func RemoteURLs(ctx context.Context, r RemoteReader, remote string) (resolved, configured string, err error) {
	resolved, err = r.RemoteURL(ctx, remote)
	if err != nil {
		return "", "", fmt.Errorf("%w: %q", ErrNoRemote, remote)
	}
	configured, _ = r.RemoteConfiguredURL(ctx, remote)
	return resolved, configured, nil
}
