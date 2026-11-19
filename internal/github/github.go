// Package github isolates GitHub-specific parsing and API access. Nothing here
// runs Git; fetching and checking out stay in internal/git.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cellestias/git-tide/internal/httpx"
	"github.com/cellestias/git-tide/internal/provider"
)

// DefaultAPIURL is the public GitHub REST API root.
const DefaultAPIURL = "https://api.github.com"

// Provider-neutral types shared with every adapter.
type (
	Repo           = provider.Repo
	PullRequest    = provider.PullRequest
	NewPullRequest = provider.NewPullRequest
	PRRef          = provider.Ref
	ErrValidation  = provider.ErrValidation
	ListOptions    = provider.ListOptions
	ChangedFile    = provider.ChangedFile
	CommitSummary  = provider.CommitSummary
	Review         = provider.Review
)

// ErrNotFound means the PR or repository does not exist or is not accessible.
var ErrNotFound = provider.ErrNotFound

var (
	scpLike  = regexp.MustCompile(`^[A-Za-z0-9._-]+@github\.com:([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+?)(?:\.git)?/?$`)
	nameSafe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// ParseRemoteURL extracts the repository from a github.com remote URL. It
// accepts https://, ssh:// and scp-like (git@github.com:o/r.git) forms.
func ParseRemoteURL(raw string) (Repo, error) {
	raw = strings.TrimSpace(raw)
	if m := scpLike.FindStringSubmatch(raw); m != nil {
		return Repo{Owner: m[1], Name: m[2]}, nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "ssh" && u.Scheme != "git") {
		return Repo{}, fmt.Errorf("unsupported remote URL format")
	}
	if !strings.EqualFold(u.Hostname(), "github.com") {
		return Repo{}, fmt.Errorf("remote host %q is not github.com", u.Hostname())
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 {
		return Repo{}, fmt.Errorf("remote path %q is not owner/repo", u.Path)
	}
	name := strings.TrimSuffix(parts[1], ".git")
	if !nameSafe.MatchString(parts[0]) || !nameSafe.MatchString(name) {
		return Repo{}, fmt.Errorf("remote path %q contains invalid characters", u.Path)
	}
	return Repo{Owner: parts[0], Name: name}, nil
}

var prURL = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)/pull/(\d+)(?:[/?#].*)?$`)

// ParsePRRef accepts "123", "#123", or a full pull request URL.
func ParsePRRef(s string) (PRRef, error) {
	s = strings.TrimSpace(s)
	if m := prURL.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[3])
		if err != nil || n <= 0 {
			return PRRef{}, fmt.Errorf("invalid pull request number in %q", s)
		}
		return PRRef{Number: n, Repo: &Repo{Owner: m[1], Name: m[2]}}, nil
	}
	n, err := strconv.Atoi(strings.TrimPrefix(s, "#"))
	if err != nil || n <= 0 || n > 1<<31-1 {
		return PRRef{}, fmt.Errorf("%q is not a pull request number or URL", s)
	}
	return PRRef{Number: n}, nil
}

// Client is a minimal GitHub REST client.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient builds a client from the environment. The token is read from
// GITHUB_TOKEN or GH_TOKEN and is optional for public repositories.
// TIDE_GITHUB_API_URL overrides the API root (HTTPS required, except loopback).
func NewClient() (*Client, error) {
	base := os.Getenv("TIDE_GITHUB_API_URL")
	if base == "" {
		base = DefaultAPIURL
	}
	if err := checkBaseURL(base); err != nil {
		return nil, err
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	return &Client{BaseURL: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}, nil
}

// Provider methods: *Client satisfies provider.Provider.

func (c *Client) Name() string     { return provider.GitHub }
func (c *Client) Noun() string     { return "pull request" }
func (c *Client) TokenEnv() string { return "GITHUB_TOKEN" }
func (c *Client) HasToken() bool   { return c != nil && c.Token != "" }

func (c *Client) ParseRemoteURL(raw string) (Repo, error) { return ParseRemoteURL(raw) }
func (c *Client) ParseRef(s string) (PRRef, error)        { return ParsePRRef(s) }

// ReviewRefspec fetches refs/pull/<n>/head, which also exists for fork PRs.
func (c *Client) ReviewRefspec(number int, dst string) string {
	return fmt.Sprintf("+refs/pull/%d/head:%s", number, dst)
}

func checkBaseURL(base string) error {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid GitHub API URL %q", base)
	}
	loopback := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && (u.Scheme != "http" || !loopback) {
		return fmt.Errorf("GitHub API URL must use https")
	}
	return nil
}

// GetPullRequest fetches PR metadata.
func (c *Client) GetPullRequest(ctx context.Context, repo Repo, number int) (PullRequest, error) {
	body, err := c.do(ctx, http.MethodGet, c.pullsURL(repo)+"/"+strconv.Itoa(number), nil)
	if err != nil {
		return PullRequest{}, err
	}
	return decodePullRequest(body)
}

// CreatePullRequest opens a pull request and returns it, including its URL.
func (c *Client) CreatePullRequest(ctx context.Context, repo Repo, npr NewPullRequest) (PullRequest, error) {
	payload, err := json.Marshal(map[string]any{
		"title": npr.Title, "body": npr.Body, "head": npr.Head, "base": npr.Base, "draft": npr.Draft,
	})
	if err != nil {
		return PullRequest{}, err
	}
	body, err := c.do(ctx, http.MethodPost, c.pullsURL(repo), payload)
	if err != nil {
		return PullRequest{}, err
	}
	return decodePullRequest(body)
}

func (c *Client) pullsURL(repo Repo) string {
	return fmt.Sprintf("%s/repos/%s/%s/pulls", c.BaseURL, url.PathEscape(repo.Owner), url.PathEscape(repo.Name))
}

// do performs a request and maps error statuses to errors that never contain
// the token or the request URL.
func (c *Client) do(ctx context.Context, method, endpoint string, payload []byte) ([]byte, error) {
	return c.doAccept(ctx, method, endpoint, payload, "application/vnd.github+json")
}

// doAccept is do with another media type asked for, such as the raw diff.
func (c *Client) doAccept(ctx context.Context, method, endpoint string, payload []byte, accept string) ([]byte, error) {
	hdr := map[string]string{
		"Accept":               accept,
		"X-GitHub-Api-Version": "2022-11-28",
		"User-Agent":           "git-tide",
	}
	if payload != nil {
		hdr["Content-Type"] = "application/json"
	}
	if c.Token != "" {
		hdr["Authorization"] = "Bearer " + c.Token
	}
	resp, err := httpx.Do(ctx, c.HTTP, httpx.Request{Method: method, URL: endpoint, Payload: payload, Header: hdr}, httpx.DefaultPolicy)
	if err != nil {
		return nil, fmt.Errorf("request GitHub API: %w", err)
	}
	body := resp.Body

	switch resp.Status {
	case http.StatusOK, http.StatusCreated:
		return body, nil
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("GitHub rejected the token (401); check GITHUB_TOKEN")
	case http.StatusForbidden, http.StatusTooManyRequests:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, fmt.Errorf("GitHub API rate limit exceeded; set GITHUB_TOKEN or retry later")
		}
		return nil, fmt.Errorf("GitHub denied access (%d); check token permissions", resp.Status)
	case http.StatusUnprocessableEntity:
		return nil, &ErrValidation{Message: validationMessage(body)}
	default:
		return nil, fmt.Errorf("GitHub API returned status %d", resp.Status)
	}
}

func validationMessage(body []byte) string {
	var v struct {
		Message string `json:"message"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &v) != nil || v.Message == "" {
		return "validation failed"
	}
	parts := []string{v.Message}
	for _, e := range v.Errors {
		if e.Message != "" {
			parts = append(parts, e.Message)
		}
	}
	return strings.Join(parts, ": ")
}

func decodePullRequest(body []byte) (PullRequest, error) {
	var raw struct {
		Number    int        `json:"number"`
		Title     string     `json:"title"`
		Body      string     `json:"body"`
		State     string     `json:"state"`
		Draft     bool       `json:"draft"`
		HTMLURL   string     `json:"html_url"`
		MergedAt  *time.Time `json:"merged_at"`
		CreatedAt time.Time  `json:"created_at"`
		UpdatedAt time.Time  `json:"updated_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		Assignees []struct {
			Login string `json:"login"`
		} `json:"assignees"`
		RequestedReviewers []struct {
			Login string `json:"login"`
		} `json:"requested_reviewers"`
		Head struct {
			Ref  string `json:"ref"`
			SHA  string `json:"sha"`
			Repo *struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return PullRequest{}, fmt.Errorf("decode GitHub API response: %w", err)
	}
	pr := PullRequest{
		Number:    raw.Number,
		Title:     raw.Title,
		Body:      raw.Body,
		State:     raw.State,
		Draft:     raw.Draft,
		URL:       raw.HTMLURL,
		Author:    raw.User.Login,
		HeadRef:   raw.Head.Ref,
		HeadSHA:   raw.Head.SHA,
		BaseRef:   raw.Base.Ref,
		CreatedAt: raw.CreatedAt,
		UpdatedAt: raw.UpdatedAt,
	}
	if raw.MergedAt != nil {
		pr.State = provider.StateMerged
	}
	for _, a := range raw.Assignees {
		pr.Assignees = append(pr.Assignees, a.Login)
	}
	for _, r := range raw.RequestedReviewers {
		pr.Reviewers = append(pr.Reviewers, r.Login)
	}
	if raw.Head.Repo != nil {
		pr.HeadRepo = raw.Head.Repo.FullName
	}
	return pr, nil
}

const (
	pageSize = 100
	maxPages = 10 // lists are capped at pageSize*maxPages entries
)

// ListProtectedBranches returns, for every branch of repo, whether it is
// protected on GitHub. truncated reports that the list was capped.
func (c *Client) ListProtectedBranches(ctx context.Context, repo Repo) (protected map[string]bool, truncated bool, err error) {
	protected = map[string]bool{}
	truncated, err = c.eachItem(ctx, c.repoURL(repo)+"/branches", func(raw json.RawMessage) error {
		var it struct {
			Name      string `json:"name"`
			Protected bool   `json:"protected"`
		}
		if err := json.Unmarshal(raw, &it); err != nil {
			return err
		}
		protected[it.Name] = it.Protected
		return nil
	})
	return protected, truncated, err
}

// ListOpenPullRequests returns the open pull requests of repo.
func (c *Client) ListOpenPullRequests(ctx context.Context, repo Repo) (prs []PullRequest, truncated bool, err error) {
	truncated, err = c.eachItem(ctx, c.pullsURL(repo)+"?state=open", func(raw json.RawMessage) error {
		pr, err := decodePullRequest(raw)
		if err != nil {
			return err
		}
		prs = append(prs, pr)
		return nil
	})
	return prs, truncated, err
}

func (c *Client) repoURL(repo Repo) string {
	return fmt.Sprintf("%s/repos/%s/%s", c.BaseURL, url.PathEscape(repo.Owner), url.PathEscape(repo.Name))
}

// eachPage walks a paginated list endpoint; handle consumes one page.
func (c *Client) eachPage(ctx context.Context, endpoint string, handle func([]json.RawMessage) error) (truncated bool, err error) {
	return httpx.Pager{
		Get:   func(ctx context.Context, u string) ([]byte, error) { return c.do(ctx, http.MethodGet, u, nil) },
		Label: "GitHub", PageSize: pageSize, MaxPages: maxPages,
	}.Each(ctx, endpoint, handle)
}

// errStop ends a paginated walk early; it is not an error for the caller.
var errStop = httpx.ErrStop

// eachItem walks a list endpoint and calls fn for every element of every page.
func (c *Client) eachItem(ctx context.Context, endpoint string, fn func(json.RawMessage) error) (truncated bool, err error) {
	return c.eachPage(ctx, endpoint, func(items []json.RawMessage) error {
		for _, it := range items {
			if err := fn(it); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListPullRequests lists pull requests newest first. GitHub filters only by
// open/closed and head branch on the server, so merged/closed, author and
// assignee are applied here.
func (c *Client) ListPullRequests(ctx context.Context, repo Repo, opts ListOptions) (prs []PullRequest, truncated bool, err error) {
	state := opts.State
	if state == "" {
		state = provider.StateOpen
	}
	api := map[string]string{provider.StateOpen: "open", provider.StateClosed: "closed", provider.StateMerged: "closed", provider.StateAll: "all"}[state]
	if api == "" {
		return nil, false, fmt.Errorf("unknown state %q", state)
	}
	q := url.Values{"state": {api}, "sort": {"created"}, "direction": {"desc"}}
	if opts.Head != "" {
		q.Set("head", repo.Owner+":"+opts.Head)
	}
	truncated, err = c.eachItem(ctx, c.pullsURL(repo)+"?"+q.Encode(), func(raw json.RawMessage) error {
		pr, err := decodePullRequest(raw)
		if err != nil {
			return err
		}
		switch {
		case state != provider.StateAll && pr.State != state, // "closed" from the API also holds merged requests
			opts.Author != "" && !strings.EqualFold(pr.Author, opts.Author),
			opts.Assignee != "" && !hasLogin(pr.Assignees, opts.Assignee):
			return nil
		}
		prs = append(prs, pr)
		if opts.Limit > 0 && len(prs) >= opts.Limit {
			return errStop
		}
		return nil
	})
	return prs, truncated, err
}

func hasLogin(list []string, login string) bool {
	for _, l := range list {
		if strings.EqualFold(l, login) {
			return true
		}
	}
	return false
}

// CurrentUser returns the login the token belongs to.
func (c *Client) CurrentUser(ctx context.Context) (string, error) {
	body, err := c.do(ctx, http.MethodGet, c.BaseURL+"/user", nil)
	if err != nil {
		return "", err
	}
	var u struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &u); err != nil || u.Login == "" {
		return "", fmt.Errorf("decode GitHub API response: no login")
	}
	return u.Login, nil
}

// ChangedFiles lists the files of a pull request.
func (c *Client) ChangedFiles(ctx context.Context, repo Repo, number int) (files []ChangedFile, truncated bool, err error) {
	truncated, err = c.eachItem(ctx, fmt.Sprintf("%s/%d/files", c.pullsURL(repo), number), func(raw json.RawMessage) error {
		var f struct {
			Filename  string `json:"filename"`
			Previous  string `json:"previous_filename"`
			Status    string `json:"status"`
			Additions int    `json:"additions"`
			Deletions int    `json:"deletions"`
		}
		if err := json.Unmarshal(raw, &f); err != nil {
			return err
		}
		status := f.Status
		switch status {
		case "removed":
			status = "deleted"
		case "changed":
			status = "modified"
		}
		files = append(files, ChangedFile{Path: f.Filename, OldPath: f.Previous, Status: status, Additions: f.Additions, Deletions: f.Deletions, HasStats: true})
		return nil
	})
	return files, truncated, err
}

// Commits lists the commits of a pull request, oldest first.
func (c *Client) Commits(ctx context.Context, repo Repo, number int) (commits []CommitSummary, truncated bool, err error) {
	truncated, err = c.eachItem(ctx, fmt.Sprintf("%s/%d/commits", c.pullsURL(repo), number), func(raw json.RawMessage) error {
		var cm struct {
			SHA    string `json:"sha"`
			Commit struct {
				Message string `json:"message"`
			} `json:"commit"`
		}
		if err := json.Unmarshal(raw, &cm); err != nil {
			return err
		}
		subject, _, _ := strings.Cut(cm.Commit.Message, "\n")
		commits = append(commits, CommitSummary{SHA: cm.SHA, Subject: strings.TrimSpace(subject)})
		return nil
	})
	return commits, truncated, err
}

// Reviews reduces the review history to each reviewer's latest decision:
// comments do not count and a dismissed review is forgotten.
func (c *Client) Reviews(ctx context.Context, repo Repo, number int) (Review, error) {
	latest := map[string]string{}
	var order []string
	_, err := c.eachItem(ctx, fmt.Sprintf("%s/%d/reviews", c.pullsURL(repo), number), func(raw json.RawMessage) error {
		var r struct {
			State string `json:"state"`
			User  struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		if r.User.Login == "" {
			return nil
		}
		switch r.State {
		case "APPROVED", "CHANGES_REQUESTED":
			if _, seen := latest[r.User.Login]; !seen {
				order = append(order, r.User.Login)
			}
			latest[r.User.Login] = r.State
		case "DISMISSED":
			delete(latest, r.User.Login)
		}
		return nil
	})
	if err != nil {
		return Review{}, err
	}
	var rv Review
	for _, u := range order {
		switch latest[u] {
		case "APPROVED":
			rv.Approvals = append(rv.Approvals, u)
		case "CHANGES_REQUESTED":
			rv.ChangesRequested = append(rv.ChangesRequested, u)
		}
	}
	switch {
	case len(rv.ChangesRequested) > 0:
		rv.State = provider.ReviewChangesRequested
	case len(rv.Approvals) > 0:
		rv.State = provider.ReviewApproved
	}
	return rv, nil
}
