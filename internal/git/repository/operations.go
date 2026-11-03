package repository

import (
	"context"
	"fmt"
	"strings"
)

// UnmergedFiles lists paths that still have unresolved conflicts.
func (r *Repository) UnmergedFiles(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "-c", "core.quotepath=false", "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// operations are the in-progress operations InProgressOperation can report;
// all of them understand --continue and --abort.
var operations = map[string]bool{"merge": true, "rebase": true, "cherry-pick": true, "revert": true}

// ContinueOperation resumes op after its conflicts were resolved and staged.
// The editor is disabled, so the message Git prepared is used as it is.
func (r *Repository) ContinueOperation(ctx context.Context, op string) error {
	if !operations[op] {
		return fmt.Errorf("unsupported operation %q", op)
	}
	_, err := r.git(ctx, "-c", "core.editor=true", op, "--continue")
	return err
}

// AbortOperation gives up op and restores the state from before it started.
func (r *Repository) AbortOperation(ctx context.Context, op string) error {
	if !operations[op] {
		return fmt.Errorf("unsupported operation %q", op)
	}
	_, err := r.git(ctx, op, "--abort")
	return err
}
