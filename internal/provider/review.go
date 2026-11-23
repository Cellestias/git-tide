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

// Label is a tag on a request. Color is six hex digits without the leading #.
type Label struct {
	Name        string
	Color       string
	Description string
}

// CommitFile is one file of a commit, with the hunks it changed.
type CommitFile struct {
	Path      string
	OldPath   string // set for a rename
	Status    string // added, modified, deleted or renamed
	Additions int
	Deletions int
	Patch     string // the hunks, from the first @@ line; empty for a binary file or one too large
}

// CommitDetail is one commit of a request: who made it, when, and what it changed.
type CommitDetail struct {
	SHA     string
	Author  string
	Name    string
	Email   string
	Date    time.Time
	Subject string
	Body    string
	Files   []CommitFile
	Cut     bool // the provider listed only the first files
}

// CommitReader reads one commit with its files, without it being in the local repository.
type CommitReader interface {
	CommitDetail(ctx context.Context, repo Repo, sha string) (CommitDetail, error)
}

// PullEdit changes who reviews, who is assigned and which labels a request has.
// Names already there are left alone when added, and names that are not there are left alone when removed.
type PullEdit struct {
	AddReviewers    []string
	RemoveReviewers []string
	AddAssignees    []string
	RemoveAssignees []string
	AddLabels       []string
	RemoveLabels    []string
}

// Empty reports whether the edit asks for nothing.
func (e PullEdit) Empty() bool {
	return len(e.AddReviewers)+len(e.RemoveReviewers)+len(e.AddAssignees)+len(e.RemoveAssignees)+len(e.AddLabels)+len(e.RemoveLabels) == 0
}

// Editor is what an adapter offers to manage a request after it was opened.
type Editor interface {
	// Labels lists the labels of the repository.
	Labels(ctx context.Context, repo Repo) (labels []Label, truncated bool, err error)
	// People lists the logins that can be assigned a request or asked for a review.
	People(ctx context.Context, repo Repo) (logins []string, truncated bool, err error)
	// Edit applies the changes, each one on its own, and stops at the first the provider refuses.
	Edit(ctx context.Context, repo Repo, number int, e PullEdit) error
}
