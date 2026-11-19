package provider

import (
	"context"
	"time"
)

// Kinds of Comment.
const (
	KindConversation = "conversation" // said in the request's discussion, about the whole of it
	KindInline       = "inline"       // said on a line of the diff
	KindReview       = "review"       // the text sent with a review decision
)

// Sides of the diff an inline comment is on.
const (
	SideRight = "right" // the new version of the file
	SideLeft  = "left"  // the old version of the file
)

// Comment is something said on a pull or merge request.
type Comment struct {
	ID          int64
	Kind        string
	Author      string
	Body        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	URL         string
	Path        string // inline comments: the file
	Line        int    // inline comments: the line, on Side; 0 when the line is no longer in the diff
	Side        string // inline comments: SideRight or SideLeft
	DiffHunk    string // inline comments: the lines of the diff around it
	CommitSHA   string // inline comments: the commit it was written on
	InReplyTo   int64  // inline comments: the comment this answers, 0 for the first of a thread
	Outdated    bool   // inline comments: the code it points at changed since
	ReviewState string // review comments: ReviewApproved, ReviewChangesRequested or "commented"
}

// Diff is the whole patch of a request, as unified diff text.
type Diff struct {
	Patch     string
	Truncated bool // the patch was cut at the adapter's cap
	HeadSHA   string
}

// NewComment is what is said with AddComment. With Path empty it goes to the
// discussion of the request; with Path and Line it goes on that line of the
// diff, and InReplyTo makes it an answer in an existing thread.
type NewComment struct {
	Body      string
	Path      string
	Line      int
	Side      string
	CommitSHA string // the head commit the line belongs to; needed for a new inline comment
	InReplyTo int64
}

// Review decisions.
const (
	DecisionApprove        = "approve"
	DecisionRequestChanges = "request_changes"
	DecisionComment        = "comment"
)

// ReviewSubmission is a decision on a request, with the text that goes with it.
type ReviewSubmission struct {
	Decision string
	Body     string
}

// Reviewer is what an adapter offers to read and take part in a review. It is
// separate from Provider so an adapter can offer it later than the rest.
type Reviewer interface {
	// Comments lists everything said on the request, oldest first.
	Comments(ctx context.Context, repo Repo, number int) (comments []Comment, truncated bool, err error)
	// PullDiff returns the patch of the whole request.
	PullDiff(ctx context.Context, repo Repo, number int) (Diff, error)
	// AddComment says something on the request and returns what was created.
	AddComment(ctx context.Context, repo Repo, number int, c NewComment) (Comment, error)
	// SubmitReview approves, asks for changes or just comments, on behalf of the token's owner.
	SubmitReview(ctx context.Context, repo Repo, number int, r ReviewSubmission) error
}
