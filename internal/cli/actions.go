package cli

import (
	"context"

	appcommit "github.com/cellestias/git-tide/internal/app/commit"
	"github.com/cellestias/git-tide/internal/app/initrepo"
	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	sec "github.com/cellestias/git-tide/internal/domain/security"
	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
)

// actionDoc is what a command that changes something prints with --json: one
// object, always the same envelope, with what is specific to the command in
// details. A failure prints nothing on stdout: the exit code and stderr say what
// went wrong, as they do for every other command.
type actionDoc struct {
	OK     bool   `json:"ok"`
	Action string `json:"action"`
	Branch string `json:"branch"` // the branch checked out afterwards; "" when HEAD is detached
	Head   string `json:"head"`   // full hash of HEAD afterwards
	// Details holds what is specific to the command; see docs/json.md.
	Details any `json:"details"`
}

// act runs an action. Without --json it is just run. With it, the human log
// (the progress lines, the success line) goes to stderr, so stdout holds only
// the document printed once the action has worked. details runs after the action,
// so it can read the repository as the action left it.
func (e *env) act(ctx context.Context, repo *repository.Repository, asJSON bool, action string, run func(p output.Printer) error, details func() any) error {
	if !asJSON {
		return run(e.p)
	}
	if err := run(e.quiet(true)); err != nil {
		return err
	}
	doc := actionDoc{OK: true, Action: action, Details: map[string]any{}}
	if repo != nil {
		if b, err := repo.CurrentBranch(ctx); err == nil {
			doc.Branch = b
		}
		if h, err := repo.Head(ctx); err == nil {
			doc.Head = h
		}
	}
	if details != nil {
		if d := details(); d != nil {
			doc.Details = d
		}
	}
	if err := e.p.JSON(doc); err != nil {
		return apperr.Wrap(apperr.CodeGeneral, "cannot write JSON", "", err)
	}
	return nil
}

// quiet is the printer for an action: with --json its log goes to stderr, so that
// stdout is left for the document alone.
func (e *env) quiet(asJSON bool) output.Printer {
	p := e.p
	if asJSON {
		p.Out, p.ColorOut = e.p.Err, e.p.ColorErr
	}
	return p
}

type fileChange struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Binary  bool   `json:"binary"`
}

func toChanges(files []repository.FileChange) []fileChange {
	out := make([]fileChange, 0, len(files))
	for _, f := range files {
		out = append(out, fileChange{f.Path, f.Added, f.Deleted, f.Binary})
	}
	return out
}

// commitDetails describes the commit HEAD points to.
func commitDetails(ctx context.Context, repo *repository.Repository, extra map[string]any) any {
	d := map[string]any{}
	for k, v := range extra {
		d[k] = v
	}
	if c, err := repo.Show(ctx, "HEAD", false); err == nil {
		d["commit"], d["subject"], d["files"] = c.FullHash, c.Subject, toChanges(c.Files)
	}
	return d
}

// localBranches lists the names of the local branches.
func localBranches(ctx context.Context, repo *repository.Repository) map[string]bool {
	set := map[string]bool{}
	refs, err := repo.ListBranches(ctx, "", false)
	if err != nil {
		return set
	}
	for _, r := range refs {
		if !r.Remote {
			set[r.Name] = true
		}
	}
	return set
}

// headInfo is the branch and the commit HEAD is on, for a before-and-after.
type headInfo struct {
	Branch string `json:"branch"`
	Head   string `json:"head"`
}

func (e *env) headNow(ctx context.Context, repo *repository.Repository) headInfo {
	var h headInfo
	if b, err := repo.CurrentBranch(ctx); err == nil {
		h.Branch = b
	}
	if c, err := repo.Head(ctx); err == nil {
		h.Head = c
	}
	return h
}

// commitThenAlerts makes the commit, then, at levels medium and high, lists the
// open security alerts. The alerts never undo or fail a commit that was made.
func (e *env) commitThenAlerts(ctx context.Context, repo *repository.Repository, cfg domain.Config, opts appcommit.Options, action string, asJSON bool) error {
	return e.act(ctx, repo, asJSON, action, func(p output.Printer) error {
		if err := appcommit.Run(ctx, repo, cfg, opts, p); err != nil {
			return err
		}
		if cfg.SecurityLevel() != sec.Off {
			_ = e.securityGate(repo, cfg)(ctx, sec.StageCommit, nil)
		}
		return nil
	}, func() any { return commitDetails(ctx, repo, nil) })
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// remoteDetails names a remote and the URL it has now, with a password hidden.
func (e *env) remoteDetails(ctx context.Context, repo *repository.Repository, name string) any {
	url, _ := repo.RemoteConfiguredURL(ctx, name)
	return map[string]any{"name": name, "url": executor.Redact(url), "provider": provider.DetectName(url)}
}

// initDetails describes what `gtide init` did: each file as created or kept, the
// remote, and the hooks.
func initDetails(res initrepo.Result) any {
	changes := make([]string, 0, len(res.HookChanges))
	for _, c := range res.HookChanges {
		changes = append(changes, c.Describe())
	}
	d := map[string]any{
		"config":        map[string]any{"path": res.ConfigPath, "created": res.ConfigCreated},
		"secret_file":   map[string]any{"path": res.SecretPath, "created": res.SecretCreated},
		"gitignore":     map[string]any{"created": res.IgnoreCreated, "updated": res.IgnoreUpdated},
		"provider":      res.Provider,
		"base":          res.Base,
		"hooks_skipped": res.HooksSkipped,
		"hook_changes":  changes,
		"notes":         append([]string{}, res.Notes...),
		"remote":        nil,
	}
	if res.RemoteURL != "" {
		d["remote"] = map[string]any{"name": res.RemoteName, "url": executor.Redact(res.RemoteURL), "added": res.RemoteAdded}
	}
	return d
}
