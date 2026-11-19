package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cellestias/git-tide/internal/provider"
)

// maxDiffBytes caps the patch of a request: a huge one would not be read anyway.
const maxDiffBytes = 4 << 20

var _ provider.Reviewer = (*Client)(nil)

type rawUser struct {
	Login string `json:"login"`
}

type rawComment struct {
	ID        int64     `json:"id"`
	User      rawUser   `json:"user"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	HTMLURL   string    `json:"html_url"`
	// inline comments only
	Path       string `json:"path"`
	Line       *int   `json:"line"`
	OrigLine   *int   `json:"original_line"`
	Side       string `json:"side"`
	DiffHunk   string `json:"diff_hunk"`
	CommitID   string `json:"commit_id"`
	InReplyTo  int64  `json:"in_reply_to_id"`
	PositionIs *int   `json:"position"` // null when the commented line is no longer in the diff
}

func (r rawComment) inline() provider.Comment {
	c := provider.Comment{
		ID: r.ID, Kind: provider.KindInline, Author: r.User.Login, Body: r.Body, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		URL: r.HTMLURL, Path: r.Path, DiffHunk: r.DiffHunk, CommitSHA: r.CommitID, InReplyTo: r.InReplyTo, Outdated: r.PositionIs == nil,
		Side: provider.SideRight,
	}
	if strings.EqualFold(r.Side, "LEFT") {
		c.Side = provider.SideLeft
	}
	switch {
	case r.Line != nil:
		c.Line = *r.Line
	case r.OrigLine != nil:
		c.Line = *r.OrigLine
	}
	return c
}

// Comments lists the discussion, the comments on lines and the text of the
// reviews, oldest first. A review that only holds inline comments is left out:
// the comments are listed on their own.
func (c *Client) Comments(ctx context.Context, repo Repo, number int) (comments []provider.Comment, truncated bool, err error) {
	issue := fmt.Sprintf("%s/issues/%d/comments", c.repoURL(repo), number)
	t1, err := c.eachItem(ctx, issue, func(raw json.RawMessage) error {
		var r rawComment
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		comments = append(comments, provider.Comment{ID: r.ID, Kind: provider.KindConversation, Author: r.User.Login, Body: r.Body,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, URL: r.HTMLURL})
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	t2, err := c.eachItem(ctx, fmt.Sprintf("%s/%d/comments", c.pullsURL(repo), number), func(raw json.RawMessage) error {
		var r rawComment
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		comments = append(comments, r.inline())
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	t3, err := c.eachItem(ctx, fmt.Sprintf("%s/%d/reviews", c.pullsURL(repo), number), func(raw json.RawMessage) error {
		var r struct {
			ID          int64     `json:"id"`
			User        rawUser   `json:"user"`
			Body        string    `json:"body"`
			State       string    `json:"state"`
			SubmittedAt time.Time `json:"submitted_at"`
			HTMLURL     string    `json:"html_url"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		state := "commented"
		switch r.State {
		case "APPROVED":
			state = provider.ReviewApproved
		case "CHANGES_REQUESTED":
			state = provider.ReviewChangesRequested
		case "PENDING":
			return nil // not sent yet
		}
		if state == "commented" && strings.TrimSpace(r.Body) == "" {
			return nil
		}
		comments = append(comments, provider.Comment{ID: r.ID, Kind: provider.KindReview, Author: r.User.Login, Body: r.Body,
			CreatedAt: r.SubmittedAt, UpdatedAt: r.SubmittedAt, URL: r.HTMLURL, ReviewState: state})
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.SliceStable(comments, func(i, j int) bool { return comments[i].CreatedAt.Before(comments[j].CreatedAt) })
	return comments, t1 || t2 || t3, nil
}

// PullDiff fetches the unified diff of the whole request.
func (c *Client) PullDiff(ctx context.Context, repo Repo, number int) (provider.Diff, error) {
	body, err := c.doAccept(ctx, http.MethodGet, fmt.Sprintf("%s/%d", c.pullsURL(repo), number), nil, "application/vnd.github.v3.diff")
	if err != nil {
		return provider.Diff{}, err
	}
	d := provider.Diff{Patch: string(body)}
	if len(body) > maxDiffBytes {
		d.Patch, d.Truncated = cutAtLine(string(body), maxDiffBytes), true
	}
	if pr, err := c.GetPullRequest(ctx, repo, number); err == nil {
		d.HeadSHA = pr.HeadSHA
	}
	return d, nil
}

// cutAtLine keeps at most max bytes, ending on a whole line.
func cutAtLine(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	if i := strings.LastIndexByte(s, '\n'); i > 0 {
		s = s[:i+1]
	}
	return s
}

// AddComment says something in the discussion, or on a line of the diff.
func (c *Client) AddComment(ctx context.Context, repo Repo, number int, n provider.NewComment) (provider.Comment, error) {
	if n.Path == "" {
		payload, err := json.Marshal(map[string]any{"body": n.Body})
		if err != nil {
			return provider.Comment{}, err
		}
		body, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", c.repoURL(repo), number), payload)
		if err != nil {
			return provider.Comment{}, err
		}
		var r rawComment
		if err := json.Unmarshal(body, &r); err != nil {
			return provider.Comment{}, fmt.Errorf("decode GitHub API response: %w", err)
		}
		return provider.Comment{ID: r.ID, Kind: provider.KindConversation, Author: r.User.Login, Body: r.Body, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, URL: r.HTMLURL}, nil
	}
	fields := map[string]any{"body": n.Body}
	if n.InReplyTo != 0 {
		fields["in_reply_to"] = n.InReplyTo
	} else {
		side := "RIGHT"
		if n.Side == provider.SideLeft {
			side = "LEFT"
		}
		fields["commit_id"], fields["path"], fields["line"], fields["side"] = n.CommitSHA, n.Path, n.Line, side
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return provider.Comment{}, err
	}
	body, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/%d/comments", c.pullsURL(repo), number), payload)
	if err != nil {
		return provider.Comment{}, err
	}
	var r rawComment
	if err := json.Unmarshal(body, &r); err != nil {
		return provider.Comment{}, fmt.Errorf("decode GitHub API response: %w", err)
	}
	return r.inline(), nil
}

// SubmitReview sends a decision with its text.
func (c *Client) SubmitReview(ctx context.Context, repo Repo, number int, r provider.ReviewSubmission) error {
	event := map[string]string{
		provider.DecisionApprove: "APPROVE", provider.DecisionRequestChanges: "REQUEST_CHANGES", provider.DecisionComment: "COMMENT",
	}[r.Decision]
	if event == "" {
		return fmt.Errorf("unknown review decision %q", r.Decision)
	}
	fields := map[string]any{"event": event}
	if strings.TrimSpace(r.Body) != "" {
		fields["body"] = r.Body
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPost, fmt.Sprintf("%s/%d/reviews", c.pullsURL(repo), number), payload)
	return err
}
