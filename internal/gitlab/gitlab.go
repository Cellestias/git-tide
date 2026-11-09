// Package gitlab is the GitLab adapter: it maps Tide's pull request workflow to
// GitLab Merge Requests over the REST API. It needs no `glab` binary and runs
// no Git; fetching and checking out stay in internal/git.
package gitlab

import (
	"context"
	"encoding/json"
	"errors"
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

// DefaultHost is used when the remote host is unknown.
const DefaultHost = "gitlab.com"

type (
	Repo           = provider.Repo
	PullRequest    = provider.PullRequest
	NewPullRequest = provider.NewPullRequest
	Ref            = provider.Ref
	ErrValidation  = provider.ErrValidation
	ListOptions    = provider.ListOptions
	ChangedFile    = provider.ChangedFile
	CommitSummary  = provider.CommitSummary
	Review         = provider.Review
)

// ErrNotFound is returned when the project or merge request is not accessible.
var ErrNotFound = provider.ErrNotFound

var (
	scpLike  = regexp.MustCompile(`^[A-Za-z0-9._-]+@([A-Za-z0-9.-]+):(/?[A-Za-z0-9._/-]+?)(?:\.git)?/?$`)
	nameSafe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	hostSafe = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
)

// splitPath turns "group/sub/project(.git)" into a Repo with a nested owner.
func splitPath(p string) (Repo, error) {
	p = strings.TrimSuffix(strings.Trim(p, "/"), ".git")
	parts := strings.Split(p, "/")
	if len(parts) < 2 {
		return Repo{}, fmt.Errorf("remote path %q is not namespace/project", p)
	}
	for _, seg := range parts {
		if !nameSafe.MatchString(seg) || seg == "." || seg == ".." {
			return Repo{}, fmt.Errorf("remote path %q contains invalid characters", p)
		}
	}
	return Repo{Owner: strings.Join(parts[:len(parts)-1], "/"), Name: parts[len(parts)-1]}, nil
}

// ParseRemoteURL extracts the project from a GitLab remote URL. It accepts
// https://, ssh://, git:// and scp-like (git@gitlab.com:group/proj.git) forms on
// any host, so self-hosted instances work when `provider: gitlab` is set.
func ParseRemoteURL(raw string) (Repo, error) {
	_, repo, err := parseRemote(raw)
	return repo, err
}

func parseRemote(raw string) (host string, repo Repo, err error) {
	raw = strings.TrimSpace(raw)
	if m := scpLike.FindStringSubmatch(raw); m != nil {
		repo, err = splitPath(m[2])
		return m[1], repo, err
	}
	u, perr := url.Parse(raw)
	if perr != nil || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "ssh" && u.Scheme != "git") {
		return "", Repo{}, fmt.Errorf("unsupported remote URL format")
	}
	if u.Hostname() == "" || !hostSafe.MatchString(u.Hostname()) {
		return "", Repo{}, fmt.Errorf("remote has no valid host")
	}
	repo, err = splitPath(u.Path)
	return u.Hostname(), repo, err
}

// HostOf returns the hostname of a remote URL, or "".
func HostOf(raw string) string {
	h, _, err := parseRemote(raw)
	if err != nil {
		return ""
	}
	return h
}

var mrURL = regexp.MustCompile(`^https?://([A-Za-z0-9.-]+(?::\d+)?)/([A-Za-z0-9._/-]+?)/(?:-/)?merge_requests/(\d+)(?:[/?#].*)?$`)

// ParseRef accepts "123", "#123", "!123", or a full merge request URL.
func ParseRef(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if m := mrURL.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[3])
		if err != nil || n <= 0 {
			return Ref{}, fmt.Errorf("invalid merge request number in %q", s)
		}
		repo, err := splitPath(m[2])
		if err != nil {
			return Ref{}, err
		}
		return Ref{Number: n, Repo: &repo}, nil
	}
	n, err := strconv.Atoi(strings.TrimLeft(s, "#!"))
	if err != nil || n <= 0 || n > 1<<31-1 || strings.Count(s, "#")+strings.Count(s, "!") > 1 {
		return Ref{}, fmt.Errorf("%q is not a merge request number or URL", s)
	}
	return Ref{Number: n}, nil
}

// Client is a minimal GitLab REST client.
type Client struct {
	BaseURL string // e.g. https://gitlab.com/api/v4
	Token   string
	HTTP    *http.Client
}

// NewClient builds a client for host (the remote's hostname; "" means gitlab.com).
// TIDE_GITLAB_API_URL overrides the API root (HTTPS required, except loopback).
// The token is read from GITLAB_TOKEN and may be replaced by the caller.
func NewClient(host string) (*Client, error) {
	base := os.Getenv("TIDE_GITLAB_API_URL")
	if base == "" {
		if host == "" {
			host = DefaultHost
		}
		base = "https://" + host + "/api/v4"
	}
	if err := checkBaseURL(base); err != nil {
		return nil, err
	}
	return &Client{BaseURL: strings.TrimRight(base, "/"), Token: os.Getenv("GITLAB_TOKEN"), HTTP: &http.Client{Timeout: 30 * time.Second}}, nil
}

func checkBaseURL(base string) error {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid GitLab API URL %q", base)
	}
	loopback := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && (u.Scheme != "http" || !loopback) {
		return fmt.Errorf("GitLab API URL must use https")
	}
	return nil
}

func (c *Client) Name() string     { return provider.GitLab }
func (c *Client) Noun() string     { return "merge request" }
func (c *Client) TokenEnv() string { return "GITLAB_TOKEN" }
func (c *Client) HasToken() bool   { return c != nil && c.Token != "" }

func (c *Client) ParseRemoteURL(raw string) (Repo, error) { return ParseRemoteURL(raw) }
func (c *Client) ParseRef(s string) (Ref, error)          { return ParseRef(s) }

// ReviewRefspec fetches refs/merge-requests/<iid>/head, which also exists for fork MRs.
func (c *Client) ReviewRefspec(number int, dst string) string {
	return fmt.Sprintf("+refs/merge-requests/%d/head:%s", number, dst)
}

func (c *Client) projectURL(repo Repo) string {
	return fmt.Sprintf("%s/projects/%s", c.BaseURL, url.PathEscape(repo.String()))
}

func (c *Client) mrsURL(repo Repo) string { return c.projectURL(repo) + "/merge_requests" }

// GetPullRequest fetches merge request metadata.
func (c *Client) GetPullRequest(ctx context.Context, repo Repo, number int) (PullRequest, error) {
	body, err := c.do(ctx, http.MethodGet, c.mrsURL(repo)+"/"+strconv.Itoa(number), nil)
	if err != nil {
		return PullRequest{}, err
	}
	return decode(body, repo)
}

// CreatePullRequest opens a merge request. A draft gets GitLab's "Draft: " title prefix.
func (c *Client) CreatePullRequest(ctx context.Context, repo Repo, npr NewPullRequest) (PullRequest, error) {
	title := npr.Title
	if npr.Draft && !strings.HasPrefix(strings.ToLower(title), "draft:") {
		title = "Draft: " + title
	}
	payload, err := json.Marshal(map[string]any{
		"title": title, "description": npr.Body, "source_branch": npr.Head, "target_branch": npr.Base,
	})
	if err != nil {
		return PullRequest{}, err
	}
	body, err := c.do(ctx, http.MethodPost, c.mrsURL(repo), payload)
	if err != nil {
		return PullRequest{}, err
	}
	return decode(body, repo)
}

const (
	pageSize = 100
	maxPages = 10
)

// ListOpenPullRequests returns the open merge requests of repo.
func (c *Client) ListOpenPullRequests(ctx context.Context, repo Repo) (prs []PullRequest, truncated bool, err error) {
	truncated, err = c.eachPage(ctx, c.mrsURL(repo)+"?state=opened", func(items []json.RawMessage) error {
		for _, it := range items {
			pr, err := decode(it, repo)
			if err != nil {
				return err
			}
			prs = append(prs, pr)
		}
		return nil
	})
	return prs, truncated, err
}

// ListProtectedBranches returns the protected branch names of repo. GitLab
// lists only protected branches, so every returned name maps to true; wildcard
// rules (such as "release/*") are listed under their pattern.
func (c *Client) ListProtectedBranches(ctx context.Context, repo Repo) (protected map[string]bool, truncated bool, err error) {
	protected = map[string]bool{}
	truncated, err = c.eachPage(ctx, c.projectURL(repo)+"/protected_branches", func(items []json.RawMessage) error {
		for _, it := range items {
			var b struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(it, &b); err != nil {
				return err
			}
			protected[b.Name] = true
		}
		return nil
	})
	return protected, truncated, err
}

func (c *Client) eachPage(ctx context.Context, endpoint string, handle func([]json.RawMessage) error) (truncated bool, err error) {
	return httpx.Pager{
		Get:   func(ctx context.Context, u string) ([]byte, error) { return c.do(ctx, http.MethodGet, u, nil) },
		Label: "GitLab", PageSize: pageSize, MaxPages: maxPages,
	}.Each(ctx, endpoint, handle)
}

// do performs a request and maps error statuses to errors that never contain
// the token or the request URL.
func (c *Client) do(ctx context.Context, method, endpoint string, payload []byte) ([]byte, error) {
	hdr := map[string]string{"Accept": "application/json", "User-Agent": "git-tide"}
	if payload != nil {
		hdr["Content-Type"] = "application/json"
	}
	if c.Token != "" {
		hdr["PRIVATE-TOKEN"] = c.Token
	}
	resp, err := httpx.Do(ctx, c.HTTP, httpx.Request{Method: method, URL: endpoint, Payload: payload, Header: hdr}, httpx.DefaultPolicy)
	if err != nil {
		return nil, fmt.Errorf("request GitLab API: %w", err)
	}
	body := resp.Body

	switch resp.Status {
	case http.StatusOK, http.StatusCreated:
		return body, nil
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("GitLab rejected the token (401); check GITLAB_TOKEN")
	case http.StatusForbidden:
		return nil, fmt.Errorf("GitLab denied access (403); check token permissions (api scope)")
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("GitLab API rate limit exceeded; retry later")
	case http.StatusConflict, http.StatusUnprocessableEntity, http.StatusBadRequest:
		return nil, &ErrValidation{Message: validationMessage(body)}
	default:
		return nil, fmt.Errorf("GitLab API returned status %d", resp.Status)
	}
}

// validationMessage reads GitLab's {"message": "..."} or {"message": {"field": [...]}} / {"error": "..."}.
func validationMessage(body []byte) string {
	var v struct {
		Message json.RawMessage `json:"message"`
		Error   string          `json:"error"`
	}
	if json.Unmarshal(body, &v) != nil {
		return "validation failed"
	}
	if len(v.Message) > 0 {
		var s string
		if json.Unmarshal(v.Message, &s) == nil && s != "" {
			return s
		}
		var list []string
		if json.Unmarshal(v.Message, &list) == nil && len(list) > 0 {
			return strings.Join(list, "; ")
		}
		var m map[string][]string
		if json.Unmarshal(v.Message, &m) == nil && len(m) > 0 {
			var parts []string
			for k, msgs := range m {
				parts = append(parts, k+": "+strings.Join(msgs, ", "))
			}
			return strings.Join(parts, "; ")
		}
	}
	if v.Error != "" {
		return v.Error
	}
	return "validation failed"
}

func decode(body []byte, repo Repo) (PullRequest, error) {
	var raw struct {
		IID             int       `json:"iid"`
		Title           string    `json:"title"`
		Description     string    `json:"description"`
		State           string    `json:"state"`
		Draft           bool      `json:"draft"`
		WIP             bool      `json:"work_in_progress"`
		WebURL          string    `json:"web_url"`
		SourceBranch    string    `json:"source_branch"`
		TargetBranch    string    `json:"target_branch"`
		SourceProjectID int       `json:"source_project_id"`
		TargetProjectID int       `json:"target_project_id"`
		CreatedAt       time.Time `json:"created_at"`
		UpdatedAt       time.Time `json:"updated_at"`
		Author          struct {
			Username string `json:"username"`
		} `json:"author"`
		Assignees []struct {
			Username string `json:"username"`
		} `json:"assignees"`
		Reviewers []struct {
			Username string `json:"username"`
		} `json:"reviewers"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return PullRequest{}, fmt.Errorf("decode GitLab API response: %w", err)
	}
	pr := PullRequest{
		Number:    raw.IID,
		Title:     raw.Title,
		Body:      raw.Description,
		State:     normalizeState(raw.State),
		Draft:     raw.Draft || raw.WIP,
		URL:       raw.WebURL,
		Author:    raw.Author.Username,
		HeadRef:   raw.SourceBranch,
		BaseRef:   raw.TargetBranch,
		CreatedAt: raw.CreatedAt,
		UpdatedAt: raw.UpdatedAt,
	}
	for _, a := range raw.Assignees {
		pr.Assignees = append(pr.Assignees, a.Username)
	}
	for _, r := range raw.Reviewers {
		pr.Reviewers = append(pr.Reviewers, r.Username)
	}
	// Only a same-project source is reported as the repository itself, which is
	// what callers use to tell fork requests apart.
	if raw.SourceProjectID == raw.TargetProjectID {
		pr.HeadRepo = repo.String()
	}
	return pr, nil
}

// normalizeState maps GitLab's opened/locked/closed/merged to Tide's states.
func normalizeState(s string) string {
	switch s {
	case "opened", "locked":
		return provider.StateOpen
	}
	return s
}

// errStop ends a paginated walk early; it is not an error for the caller.
var errStop = httpx.ErrStop

// ListPullRequests lists merge requests newest first, filtered by opts.
func (c *Client) ListPullRequests(ctx context.Context, repo Repo, opts ListOptions) (prs []PullRequest, truncated bool, err error) {
	state := opts.State
	if state == "" {
		state = provider.StateOpen
	}
	api := map[string]string{provider.StateOpen: "opened", provider.StateClosed: "closed", provider.StateMerged: "merged", provider.StateAll: "all"}[state]
	if api == "" {
		return nil, false, fmt.Errorf("unknown state %q", state)
	}
	q := url.Values{"state": {api}, "order_by": {"created_at"}, "sort": {"desc"}}
	if opts.Author != "" {
		q.Set("author_username", opts.Author)
	}
	if opts.Assignee != "" {
		q.Set("assignee_username", opts.Assignee)
	}
	if opts.Head != "" {
		q.Set("source_branch", opts.Head)
	}
	truncated, err = c.eachPage(ctx, c.mrsURL(repo)+"?"+q.Encode(), func(items []json.RawMessage) error {
		for _, it := range items {
			pr, err := decode(it, repo)
			if err != nil {
				return err
			}
			prs = append(prs, pr)
			if opts.Limit > 0 && len(prs) >= opts.Limit {
				return errStop
			}
		}
		return nil
	})
	return prs, truncated, err
}

// CurrentUser returns the username the token belongs to.
func (c *Client) CurrentUser(ctx context.Context) (string, error) {
	body, err := c.do(ctx, http.MethodGet, c.BaseURL+"/user", nil)
	if err != nil {
		return "", err
	}
	var u struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(body, &u); err != nil || u.Username == "" {
		return "", fmt.Errorf("decode GitLab API response: no username")
	}
	return u.Username, nil
}

// ChangedFiles lists the files of a merge request. GitLab reports no line
// counts here, so HasStats stays false.
func (c *Client) ChangedFiles(ctx context.Context, repo Repo, number int) (files []ChangedFile, truncated bool, err error) {
	truncated, err = c.eachPage(ctx, fmt.Sprintf("%s/%d/diffs", c.mrsURL(repo), number), func(items []json.RawMessage) error {
		for _, it := range items {
			var d struct {
				OldPath string `json:"old_path"`
				NewPath string `json:"new_path"`
				New     bool   `json:"new_file"`
				Renamed bool   `json:"renamed_file"`
				Deleted bool   `json:"deleted_file"`
			}
			if err := json.Unmarshal(it, &d); err != nil {
				return err
			}
			f := ChangedFile{Path: d.NewPath, Status: "modified"}
			switch {
			case d.New:
				f.Status = "added"
			case d.Deleted:
				f.Status, f.Path = "deleted", d.OldPath
			case d.Renamed:
				f.Status, f.OldPath = "renamed", d.OldPath
			}
			files = append(files, f)
		}
		return nil
	})
	return files, truncated, err
}

// Commits lists the commits of a merge request, oldest first (GitLab returns
// them newest first).
func (c *Client) Commits(ctx context.Context, repo Repo, number int) (commits []CommitSummary, truncated bool, err error) {
	truncated, err = c.eachPage(ctx, fmt.Sprintf("%s/%d/commits", c.mrsURL(repo), number), func(items []json.RawMessage) error {
		for _, it := range items {
			var cm struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			}
			if err := json.Unmarshal(it, &cm); err != nil {
				return err
			}
			commits = append(commits, CommitSummary{SHA: cm.ID, Subject: cm.Title})
		}
		return nil
	})
	for i, j := 0, len(commits)-1; i < j; i, j = i+1, j-1 {
		commits[i], commits[j] = commits[j], commits[i]
	}
	return commits, truncated, err
}

// Reviews reads the merge request's approvals. GitLab has no "changes
// requested" state in its REST API, so the result is approved or pending. A
// project without the approvals feature yields a pending, empty review.
func (c *Client) Reviews(ctx context.Context, repo Repo, number int) (Review, error) {
	body, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/%d/approvals", c.mrsURL(repo), number), nil)
	if errors.Is(err, ErrNotFound) {
		return Review{}, nil
	}
	if err != nil {
		return Review{}, err
	}
	var a struct {
		ApprovedBy []struct {
			User struct {
				Username string `json:"username"`
			} `json:"user"`
		} `json:"approved_by"`
		ApprovalsLeft *int `json:"approvals_left"`
	}
	if err := json.Unmarshal(body, &a); err != nil {
		return Review{}, fmt.Errorf("decode GitLab API response: %w", err)
	}
	var rv Review
	for _, ap := range a.ApprovedBy {
		rv.Approvals = append(rv.Approvals, ap.User.Username)
	}
	if len(rv.Approvals) > 0 && (a.ApprovalsLeft == nil || *a.ApprovalsLeft == 0) {
		rv.State = provider.ReviewApproved
	}
	return rv, nil
}
