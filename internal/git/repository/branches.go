package repository

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// BranchRef describes one local branch, or one remote branch when Remote is true.
type BranchRef struct {
	Name        string // short name; for remote branches without the "<remote>/" prefix
	Remote      bool
	Current     bool
	Updated     time.Time // committer date of the tip
	AuthorName  string
	AuthorEmail string
	Upstream    string // e.g. "origin/feature/x"; empty if none (local branches only)
	UpAhead     int
	UpBehind    int
	UpGone      bool // the upstream branch no longer exists on the remote
}

// Ref returns the fully qualified ref of the branch.
func (b BranchRef) Ref(remote string) string {
	if b.Remote {
		return "refs/remotes/" + remote + "/" + b.Name
	}
	return "refs/heads/" + b.Name
}

var (
	trackAhead  = regexp.MustCompile(`ahead (\d+)`)
	trackBehind = regexp.MustCompile(`behind (\d+)`)
)

// ListBranches returns the local branches and, with includeRemote, the
// remote-tracking branches of remote (without the symbolic HEAD).
func (r *Repository) ListBranches(ctx context.Context, remote string, includeRemote bool) ([]BranchRef, error) {
	patterns := []string{"refs/heads"}
	if includeRemote {
		patterns = append(patterns, "refs/remotes/"+remote)
	}
	format := strings.Join([]string{
		"%(refname)", "%(HEAD)", "%(committerdate:unix)", "%(authorname)", "%(authoremail)",
		"%(upstream:short)", "%(upstream:track)",
	}, "%00")
	out, err := r.git(ctx, append([]string{"for-each-ref", "--format=" + format}, patterns...)...)
	if err != nil {
		return nil, err
	}

	var refs []BranchRef
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\x00")
		if len(f) != 7 {
			return nil, fmt.Errorf("unexpected for-each-ref output %q", line)
		}
		b := BranchRef{
			Current:     f[1] == "*",
			AuthorName:  f[3],
			AuthorEmail: strings.Trim(f[4], "<>"),
			Upstream:    f[5],
		}
		if ts, err := strconv.ParseInt(f[2], 10, 64); err == nil {
			b.Updated = time.Unix(ts, 0)
		}
		switch {
		case strings.HasPrefix(f[0], "refs/heads/"):
			b.Name = strings.TrimPrefix(f[0], "refs/heads/")
		case strings.HasPrefix(f[0], "refs/remotes/"+remote+"/"):
			b.Name = strings.TrimPrefix(f[0], "refs/remotes/"+remote+"/")
			b.Remote = true
			if b.Name == "HEAD" {
				continue
			}
		default:
			continue
		}
		if strings.Contains(f[6], "gone") {
			b.UpGone = true
		}
		if m := trackAhead.FindStringSubmatch(f[6]); m != nil {
			b.UpAhead, _ = strconv.Atoi(m[1])
		}
		if m := trackBehind.FindStringSubmatch(f[6]); m != nil {
			b.UpBehind, _ = strconv.Atoi(m[1])
		}
		refs = append(refs, b)
	}
	return refs, nil
}

// AheadBehind counts the commits in head that base lacks (ahead) and the
// commits in base that head lacks (behind).
func (r *Repository) AheadBehind(ctx context.Context, base, head string) (ahead, behind int, err error) {
	out, err := r.git(ctx, "rev-list", "--left-right", "--count", base+"..."+head)
	if err != nil {
		return 0, 0, err
	}
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d\t%d", &behind, &ahead); err != nil {
		return 0, 0, fmt.Errorf("parse rev-list output %q: %w", out, err)
	}
	return ahead, behind, nil
}

// FetchPrune refreshes all remote-tracking refs of remote and drops those
// whose branch no longer exists there.
func (r *Repository) FetchPrune(ctx context.Context, remote string) error {
	_, err := r.git(ctx, "fetch", "--no-tags", "--prune", "--", remote)
	return err
}

// UserEmail returns the configured user.email, or "".
func (r *Repository) UserEmail(ctx context.Context) string {
	out, err := r.git(ctx, "config", "--get", "user.email")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
