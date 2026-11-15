package status

import (
	"context"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

type jsonDistance struct {
	Name   string `json:"name"`
	Ahead  int    `json:"ahead"`
	Behind int    `json:"behind"`
	// Known is false when Git could not count the commits; ahead and behind are then 0.
	Known bool `json:"known"`
}

type jsonFile struct {
	Path string `json:"path"`
	From string `json:"from,omitempty"` // the old path of a rename
	// Index and Worktree are the two Git status letters (" " for none), as in `git status --short`.
	Index     string `json:"index"`
	Worktree  string `json:"worktree"`
	Staged    bool   `json:"staged"`
	Unstaged  bool   `json:"unstaged"`
	Untracked bool   `json:"untracked"`
	Conflict  bool   `json:"conflict"`
}

func toDistance(d *distanceTo) *jsonDistance {
	if d == nil {
		return nil
	}
	return &jsonDistance{d.Name, d.Ahead, d.Behind, d.Known}
}

// RunJSON prints what Run shows, as one JSON object on stdout.
func RunJSON(ctx context.Context, repo *repository.Repository, cfg domain.Config, p output.Printer) error {
	s, err := collect(ctx, repo, cfg)
	if err != nil {
		return err
	}
	files := make([]jsonFile, 0, len(s.entries))
	for _, e := range s.entries {
		files = append(files, jsonFile{
			Path: e.Path, From: e.From, Index: string(e.X), Worktree: string(e.Y),
			Staged: e.Staged(), Unstaged: e.Unstaged(), Untracked: e.Untracked(), Conflict: e.Conflict(),
		})
	}
	out := struct {
		Branch    string        `json:"branch"` // "" when HEAD is detached
		Detached  bool          `json:"detached"`
		Head      string        `json:"head,omitempty"` // short hash, only when detached
		Upstream  *jsonDistance `json:"upstream"`       // null when the branch has none
		Base      *jsonDistance `json:"base"`           // null on the base branch itself
		Operation string        `json:"operation"`      // "merge", "rebase"... or ""
		Clean     bool          `json:"clean"`
		Files     []jsonFile    `json:"files"`
	}{s.branch, s.detached, s.head, toDistance(s.upstream), toDistance(s.base), s.operation, len(s.entries) == 0, files}
	if err := p.JSON(out); err != nil {
		return apperr.Wrap(apperr.CodeGeneral, "cannot write JSON", "", err)
	}
	return nil
}

type jsonChange struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Binary  bool   `json:"binary"`
}

func diffJSON(ctx context.Context, repo *repository.Repository, opts DiffOptions, path string, p output.Printer) error {
	files, err := repo.ChangeStat(ctx, opts.Staged, path)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read the diff", "", err)
	}
	changes := make([]jsonChange, 0, len(files))
	for _, f := range files {
		changes = append(changes, jsonChange{f.Path, f.Added, f.Deleted, f.Binary})
	}
	scope := "worktree"
	if opts.Staged {
		scope = "staged"
	}
	out := struct {
		Scope string       `json:"scope"` // "worktree" (against the index) or "staged" (against the last commit)
		Files []jsonChange `json:"files"`
		Patch *string      `json:"patch,omitempty"` // only with --patch
	}{Scope: scope, Files: changes}
	if opts.Patch {
		patch, err := repo.ChangePatch(ctx, opts.Staged, path)
		if err != nil {
			return apperr.Wrap(apperr.CodeExternal, "cannot read the diff", "", err)
		}
		out.Patch = &patch
	}
	if err := p.JSON(out); err != nil {
		return apperr.Wrap(apperr.CodeGeneral, "cannot write JSON", "", err)
	}
	return nil
}
