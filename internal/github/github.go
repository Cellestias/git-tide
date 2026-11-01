// Package github isolates GitHub-specific parsing and API access. Nothing here
// runs Git; fetching and checking out stay in internal/git.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultAPIURL is the public GitHub REST API root.
const DefaultAPIURL = "https://api.github.com"

// Repo identifies a GitHub repository.
type Repo struct {
	Owner string
	Name  string
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

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

// PRRef is a parsed Pull Request identifier. Repo is nil for bare numbers.
type PRRef struct {
	Number int
	Repo   *Repo
}

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

// PullRequest is the subset of PR metadata Tide displays.
type PullRequest struct {
	Number   int
	Title    string
	State    string
	URL      string
	Author   string
	HeadRef  string
	BaseRef  string
	HeadRepo string // full name of the head repository; empty if it was deleted
}

// ErrNotFound means the PR or repository does not exist or is not accessible.
var ErrNotFound = errors.New("pull request not found or not accessible")

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

func checkBaseURL(base string) error {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid GitHub API URL %q", base)
	}
	loopback := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
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

// NewPullRequest describes a pull request to create.
type NewPullRequest struct {
	Title string
	Body  string
	Head  string // branch in the same repository
	Base  string
	Draft bool
}

// ErrValidation means GitHub rejected the request (HTTP 422), for example
// because a pull request for the branch already exists. The message comes from
// GitHub and is safe to show.
type ErrValidation struct{ Message string }

func (e *ErrValidation) Error() string { return "GitHub rejected the pull request: " + e.Message }

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
	var rdr io.Reader
	if payload != nil {
		rdr = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "git-tide")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request GitHub API: %w", redactURLErr(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read GitHub API response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated:
		return body, nil
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("GitHub rejected the token (401); check GITHUB_TOKEN")
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, fmt.Errorf("GitHub API rate limit exceeded; set GITHUB_TOKEN or retry later")
		}
		return nil, fmt.Errorf("GitHub denied access (%d); check token permissions", resp.StatusCode)
	case resp.StatusCode == http.StatusUnprocessableEntity:
		return nil, &ErrValidation{Message: validationMessage(body)}
	default:
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
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
		Number  int    `json:"number"`
		Title   string `json:"title"`
		State   string `json:"state"`
		HTMLURL string `json:"html_url"`
		User    struct {
			Login string `json:"login"`
		} `json:"user"`
		Head struct {
			Ref  string `json:"ref"`
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
		Number:  raw.Number,
		Title:   raw.Title,
		State:   raw.State,
		URL:     raw.HTMLURL,
		Author:  raw.User.Login,
		HeadRef: raw.Head.Ref,
		BaseRef: raw.Base.Ref,
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
	truncated, err = c.eachPage(ctx, c.repoURL(repo)+"/branches", func(raw json.RawMessage) error {
		var items []struct {
			Name      string `json:"name"`
			Protected bool   `json:"protected"`
		}
		if err := json.Unmarshal(raw, &items); err != nil {
			return err
		}
		for _, it := range items {
			protected[it.Name] = it.Protected
		}
		return nil
	}, func(raw json.RawMessage) int {
		var items []json.RawMessage
		_ = json.Unmarshal(raw, &items)
		return len(items)
	})
	return protected, truncated, err
}

// ListOpenPullRequests returns the open pull requests of repo.
func (c *Client) ListOpenPullRequests(ctx context.Context, repo Repo) (prs []PullRequest, truncated bool, err error) {
	truncated, err = c.eachPage(ctx, c.pullsURL(repo)+"?state=open", func(raw json.RawMessage) error {
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return err
		}
		for _, it := range items {
			pr, err := decodePullRequest(it)
			if err != nil {
				return err
			}
			prs = append(prs, pr)
		}
		return nil
	}, func(raw json.RawMessage) int {
		var items []json.RawMessage
		_ = json.Unmarshal(raw, &items)
		return len(items)
	})
	return prs, truncated, err
}

func (c *Client) repoURL(repo Repo) string {
	return fmt.Sprintf("%s/repos/%s/%s", c.BaseURL, url.PathEscape(repo.Owner), url.PathEscape(repo.Name))
}

// eachPage walks a paginated list endpoint. handle consumes one page; count
// returns how many entries the page held, and a short page ends the walk.
func (c *Client) eachPage(ctx context.Context, endpoint string, handle func(json.RawMessage) error, count func(json.RawMessage) int) (truncated bool, err error) {
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	for page := 1; page <= maxPages; page++ {
		body, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s%sper_page=%d&page=%d", endpoint, sep, pageSize, page), nil)
		if err != nil {
			return false, err
		}
		if err := handle(body); err != nil {
			return false, fmt.Errorf("decode GitHub API response: %w", err)
		}
		if count(body) < pageSize {
			return false, nil
		}
	}
	return true, nil
}

// remoteReader is the part of the repository service RepoFromRemote needs.
type remoteReader interface {
	RemoteURL(ctx context.Context, remote string) (string, error)
	RemoteConfiguredURL(ctx context.Context, remote string) (string, error)
}

// Errors returned by RepoFromRemote.
var (
	ErrNoRemote          = errors.New("remote is not configured")
	ErrUnsupportedRemote = errors.New("remote is not a supported GitHub remote")
)

// RepoFromRemote identifies the GitHub repository behind a Git remote. Git
// may rewrite the URL through url.<base>.insteadOf into something that is not
// GitHub-shaped, so the URL as written is tried as a fallback.
func RepoFromRemote(ctx context.Context, r remoteReader, remote string) (Repo, error) {
	resolved, err := r.RemoteURL(ctx, remote)
	if err != nil {
		return Repo{}, fmt.Errorf("%w: %q", ErrNoRemote, remote)
	}
	repo, perr := ParseRemoteURL(resolved)
	if perr == nil {
		return repo, nil
	}
	if raw, err := r.RemoteConfiguredURL(ctx, remote); err == nil {
		if repo, err := ParseRemoteURL(raw); err == nil {
			return repo, nil
		}
	}
	return Repo{}, fmt.Errorf("%w: %v", ErrUnsupportedRemote, perr)
}

// redactURLErr drops the URL from *url.Error so it cannot leak into output.
func redactURLErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
