// Package tag implements `gtide tag`: list, create, publish and delete release tags.
package tag

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/domain/branch"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

// semver is a version tag: v1.4.0, 1.4.0, v2.0.0-rc.1, v1.0.0+build5.
var semver = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z][0-9A-Za-z.-]*)?(\+[0-9A-Za-z][0-9A-Za-z.-]*)?$`)

// ValidName accepts version tags only, so a typo cannot start a release build.
func ValidName(name string) error {
	if !semver.MatchString(name) {
		return apperr.Usage("invalid tag %q; use a version such as v1.4.0 or v2.0.0-rc.1", name)
	}
	return nil
}

// DefaultLimit is how many tags `gtide tag` lists unless told otherwise.
const DefaultLimit = 20

// List prints the tags, newest version first.
func List(ctx context.Context, repo *repository.Repository, limit int, p output.Printer) error {
	if limit < 0 {
		return apperr.Usage("--limit must not be negative")
	}
	tags, err := repo.Tags(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot list the tags", "", err)
	}
	if len(tags) == 0 {
		p.Info("No tags yet. Create one with `gtide tag create v1.0.0`.")
		return nil
	}
	total := len(tags)
	if limit > 0 && len(tags) > limit {
		tags = tags[:limit]
	}
	rows := [][]string{{"TAG", "DATE", "TYPE", "MESSAGE"}}
	for _, t := range tags {
		kind := "lightweight"
		if t.Annotated {
			kind = "annotated"
		}
		rows = append(rows, []string{t.Name, t.Date, kind, t.Subject})
	}
	p.Table(rows, func(_, col int, cell string) string {
		switch col {
		case 0:
			return p.Yellow(cell)
		case 1, 2:
			return p.Dim(cell)
		}
		return cell
	})
	if len(tags) < total {
		p.Note("showing the newest %d of %d; raise --limit to see more", len(tags), total)
	}
	return nil
}

// CreateOptions describes a new tag.
type CreateOptions struct {
	Message string // default: the tag name
	Push    bool   // publish it to the configured remote
	// AnyBranch allows tagging a branch that is not protected.
	AnyBranch bool
	// Confirm is asked before publishing. Nil means "proceed".
	Confirm func(prompt string) (bool, error)
}

// Create makes an annotated tag on HEAD. A tag is a promise that this commit is
// a release, so it refuses unless the commit is on a protected branch, is
// already on the remote, and the branch is not behind its upstream. It never
// moves or replaces an existing tag.
func Create(ctx context.Context, repo *repository.Repository, cfg domain.Config, name string, opts CreateOptions, p output.Printer) error {
	if err := ValidName(name); err != nil {
		return err
	}
	current, err := repo.CurrentBranch(ctx)
	if errors.Is(err, repository.ErrDetachedHead) {
		return apperr.New(apperr.CodeUnsafeRepo, "HEAD is detached; tag from a branch", "check out the release branch first")
	}
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot determine current branch", "", err)
	}
	if !opts.AnyBranch && !branch.Contains(cfg.Branches.Protected, current) {
		return apperr.New(apperr.CodePolicy, fmt.Sprintf("%q is not a protected branch; releases are tagged on one of %v", current, cfg.Branches.Protected),
			"switch to the release branch, or pass --any-branch")
	}
	if started, err := repo.HasCommits(ctx); err != nil || !started {
		return apperr.New(apperr.CodeUnsafeRepo, "there is no commit to tag yet", "make the first commit with `gtide save`")
	}
	if ok, err := repo.TagExists(ctx, name); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot look up the tag", "", err)
	} else if ok {
		return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("tag %s already exists", name), "tags are never moved; pick the next version, or remove it first with `gtide tag delete`")
	}
	if changes, _ := repo.TrackedChanges(ctx); len(changes) > 0 {
		p.Warn("%d file(s) have uncommitted changes; the tag points to the last commit, not to them", len(changes))
	}
	if up := repo.Upstream(ctx); up != "" {
		if _, behind, err := repo.AheadBehind(ctx, up, "refs/heads/"+current); err == nil && behind > 0 {
			return apperr.New(apperr.CodeUnsafeRepo, fmt.Sprintf("%s is %d commit%s behind %s", current, behind, plural(behind), up), "bring it up to date with `gtide pull`, then tag")
		}
	}
	if pushed, err := repo.ContainedInRemote(ctx, "HEAD"); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot check the remote branches", "", err)
	} else if !pushed {
		return apperr.New(apperr.CodeUnsafeRepo, "the commit to tag is not on the remote yet", "publish it first with `gtide push` (or merge it through a Pull Request), then tag")
	}

	message := opts.Message
	if message == "" {
		message = name
	}
	if err := repo.CreateTag(ctx, name, message); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot create the tag", "", err)
	}
	short, _ := repo.ShortHead(ctx)
	p.Success("Tagged %s at %s on %s", name, short, current)
	if !opts.Push {
		p.Note("publish it with `gtide tag push %s`", name)
		return nil
	}
	return Push(ctx, repo, cfg, name, opts.Confirm, p)
}

// Push publishes an existing local tag. Pushing a version tag can start a
// release build, so it asks first unless confirm is nil.
func Push(ctx context.Context, repo *repository.Repository, cfg domain.Config, name string, confirm func(string) (bool, error), p output.Printer) error {
	if err := ValidName(name); err != nil {
		return err
	}
	if ok, err := repo.TagExists(ctx, name); err != nil || !ok {
		return apperr.New(apperr.CodeUsage, fmt.Sprintf("tag %s does not exist locally", name), "see `gtide tag`")
	}
	remotes, err := repo.Remotes(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot list remotes", "", err)
	}
	if !branch.Contains(remotes, cfg.Remote) {
		return apperr.New(apperr.CodeConfig, fmt.Sprintf("remote %q is not configured", cfg.Remote), "add it with `gtide remote add <url>`")
	}
	if confirm != nil {
		ok, err := confirm(fmt.Sprintf("Publish tag %s to %s? A version tag can start a release build. [y/N] ", name, cfg.Remote))
		if err != nil {
			return apperr.Wrap(apperr.CodeGeneral, "cannot read confirmation", "use --yes to skip the prompt", err)
		}
		if !ok {
			return apperr.New(apperr.CodeGeneral, "aborted; the tag stays local", "")
		}
	}
	p.Step("Pushing %s to %s", name, cfg.Remote)
	if err := repo.PushTag(ctx, cfg.Remote, name); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot push the tag", "the remote may already have a different tag with this name; tags are never overwritten", err)
	}
	p.Success("Published %s to %s", name, cfg.Remote)
	return nil
}

// Delete removes a local tag, and with remote also the published one. Deleting
// a published tag can break whoever downloaded the release, so it asks first
// unless confirm is nil.
func Delete(ctx context.Context, repo *repository.Repository, cfg domain.Config, name string, remote bool, confirm func(string) (bool, error), p output.Printer) error {
	if err := ValidName(name); err != nil {
		return err
	}
	local, err := repo.TagExists(ctx, name)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot look up the tag", "", err)
	}
	if !local && !remote {
		return apperr.New(apperr.CodeUsage, fmt.Sprintf("tag %s does not exist locally", name), "to delete a published tag add --remote")
	}
	if confirm != nil {
		what := "the local tag " + name
		if remote {
			what = fmt.Sprintf("tag %s locally and on %s", name, cfg.Remote)
		}
		ok, err := confirm(fmt.Sprintf("Delete %s? [y/N] ", what))
		if err != nil {
			return apperr.Wrap(apperr.CodeGeneral, "cannot read confirmation", "use --yes to skip the prompt", err)
		}
		if !ok {
			return apperr.New(apperr.CodeGeneral, "aborted; nothing was deleted", "")
		}
	}
	if local {
		if err := repo.DeleteLocalTag(ctx, name); err != nil {
			return apperr.Wrap(apperr.CodeExternal, "cannot delete the tag", "", err)
		}
		p.Success("Deleted local tag %s", name)
	}
	if remote {
		if err := repo.DeleteRemoteTag(ctx, cfg.Remote, name); err != nil {
			return apperr.Wrap(apperr.CodeExternal, "cannot delete the tag on the remote", "it may not exist there, or the remote refused", err)
		}
		p.Success("Deleted tag %s on %s", name, cfg.Remote)
	}
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
