package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/cellestias/git-tide/internal/provider"
)

var (
	_ provider.CommitReader = (*Client)(nil)
	_ provider.Editor       = (*Client)(nil)
)

// maxCommitFiles is how many files GitHub lists for one commit.
const maxCommitFiles = 300

// CommitDetail reads one commit with its files and their hunks.
func (c *Client) CommitDetail(ctx context.Context, repo Repo, sha string) (provider.CommitDetail, error) {
	body, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/commits/%s", c.repoURL(repo), url.PathEscape(sha)), nil)
	if err != nil {
		return provider.CommitDetail{}, err
	}
	var raw struct {
		SHA    string `json:"sha"`
		Author *struct {
			Login string `json:"login"`
		} `json:"author"`
		Commit struct {
			Message string `json:"message"`
			Author  struct {
				Name  string    `json:"name"`
				Email string    `json:"email"`
				Date  time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`
		Files []struct {
			Filename  string `json:"filename"`
			Previous  string `json:"previous_filename"`
			Status    string `json:"status"`
			Additions int    `json:"additions"`
			Deletions int    `json:"deletions"`
			Patch     string `json:"patch"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return provider.CommitDetail{}, fmt.Errorf("decode GitHub API response: %w", err)
	}
	subject, rest := splitMessage(raw.Commit.Message)
	d := provider.CommitDetail{
		SHA: raw.SHA, Name: raw.Commit.Author.Name, Email: raw.Commit.Author.Email, Date: raw.Commit.Author.Date,
		Author: raw.Commit.Author.Name, Subject: subject, Body: rest, Cut: len(raw.Files) >= maxCommitFiles,
	}
	if raw.Author != nil && raw.Author.Login != "" {
		d.Author = raw.Author.Login
	}
	for _, f := range raw.Files {
		status := f.Status
		switch status {
		case "removed":
			status = "deleted"
		case "changed":
			status = "modified"
		}
		d.Files = append(d.Files, provider.CommitFile{Path: f.Filename, OldPath: f.Previous, Status: status, Additions: f.Additions, Deletions: f.Deletions, Patch: f.Patch})
	}
	return d, nil
}

// splitMessage cuts a commit message into its subject and the rest.
func splitMessage(m string) (subject, body string) {
	for i := 0; i < len(m); i++ {
		if m[i] == '\n' {
			return trim(m[:i]), trim(m[i+1:])
		}
	}
	return trim(m), ""
}

func trim(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\n' || s[start] == '\r' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\r' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

// Labels lists the labels of the repository.
func (c *Client) Labels(ctx context.Context, repo Repo) (labels []provider.Label, truncated bool, err error) {
	truncated, err = c.eachItem(ctx, c.repoURL(repo)+"/labels", func(raw json.RawMessage) error {
		var l struct {
			Name        string `json:"name"`
			Color       string `json:"color"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(raw, &l); err != nil {
			return err
		}
		labels = append(labels, provider.Label{Name: l.Name, Color: l.Color, Description: l.Description})
		return nil
	})
	return labels, truncated, err
}

// People lists who can be assigned a request, which is also who can be asked to review it.
func (c *Client) People(ctx context.Context, repo Repo) (logins []string, truncated bool, err error) {
	truncated, err = c.eachItem(ctx, c.repoURL(repo)+"/assignees", func(raw json.RawMessage) error {
		var u rawUser
		if err := json.Unmarshal(raw, &u); err != nil {
			return err
		}
		if u.Login != "" {
			logins = append(logins, u.Login)
		}
		return nil
	})
	return logins, truncated, err
}

// Edit applies the changes one call at a time.
func (c *Client) Edit(ctx context.Context, repo Repo, number int, e provider.PullEdit) error {
	send := func(method, endpoint, field string, names []string) error {
		if len(names) == 0 {
			return nil
		}
		payload, err := json.Marshal(map[string][]string{field: names})
		if err != nil {
			return err
		}
		_, err = c.do(ctx, method, endpoint, payload)
		return err
	}
	pull := fmt.Sprintf("%s/%d", c.pullsURL(repo), number)
	issue := fmt.Sprintf("%s/issues/%d", c.repoURL(repo), number)
	steps := []func() error{
		func() error { return send(http.MethodPost, pull+"/requested_reviewers", "reviewers", e.AddReviewers) },
		func() error {
			return send(http.MethodDelete, pull+"/requested_reviewers", "reviewers", e.RemoveReviewers)
		},
		func() error { return send(http.MethodPost, issue+"/assignees", "assignees", e.AddAssignees) },
		func() error { return send(http.MethodDelete, issue+"/assignees", "assignees", e.RemoveAssignees) },
		func() error { return send(http.MethodPost, issue+"/labels", "labels", e.AddLabels) },
		func() error {
			for _, name := range e.RemoveLabels {
				_, err := c.do(ctx, http.MethodDelete, issue+"/labels/"+url.PathEscape(name), nil)
				if err != nil && !errors.Is(err, ErrNotFound) {
					return err // a label the request does not have is already as asked
				}
			}
			return nil
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}
