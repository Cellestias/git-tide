package repository

import (
	"context"
	"strings"
)

// DiffStat summarises, per file, what head changed since it forked from base
// (the three-dot form, which is what a pull request shows). External diff
// drivers and textconv filters are disabled: the content comes from a
// contributor and must not trigger programs.
func (r *Repository) DiffStat(ctx context.Context, base, head string) (string, error) {
	return r.git(ctx, "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--stat", base+"..."+head, "--")
}

// Diff is the full patch behind DiffStat.
func (r *Repository) Diff(ctx context.Context, base, head string) (string, error) {
	return r.git(ctx, "diff", "--no-color", "--no-ext-diff", "--no-textconv", base+"..."+head, "--")
}

// ShortRev abbreviates the commit rev resolves to.
func (r *Repository) ShortRev(ctx context.Context, rev string) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--short", "--verify", rev+"^{commit}")
	return strings.TrimSpace(out), err
}
