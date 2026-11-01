// Package github isolates GitHub-specific parsing and API access. Nothing here
// runs Git; fetching and checking out stay in internal/git.
package github

import (
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
	endpoint := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", c.BaseURL, url.PathEscape(repo.Owner), url.PathEscape(repo.Name), number)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return PullRequest{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "git-tide")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return PullRequest{}, fmt.Errorf("request GitHub API: %w", redactURLErr(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return PullRequest{}, fmt.Errorf("read GitHub API response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound:
		return PullRequest{}, ErrNotFound
	case resp.StatusCode == http.StatusUnauthorized:
		return PullRequest{}, fmt.Errorf("GitHub rejected the token (401); check GITHUB_TOKEN")
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return PullRequest{}, fmt.Errorf("GitHub API rate limit exceeded; set GITHUB_TOKEN or retry later")
		}
		return PullRequest{}, fmt.Errorf("GitHub denied access (%d); check token permissions", resp.StatusCode)
	default:
		return PullRequest{}, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var raw struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
		User   struct {
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
		Author:  raw.User.Login,
		HeadRef: raw.Head.Ref,
		BaseRef: raw.Base.Ref,
	}
	if raw.Head.Repo != nil {
		pr.HeadRepo = raw.Head.Repo.FullName
	}
	return pr, nil
}

// redactURLErr drops the URL from *url.Error so it cannot leak into output.
func redactURLErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
