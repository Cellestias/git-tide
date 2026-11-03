// Package hooks implements the checks Git hooks delegate to Tide.
package hooks

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/domain/commit"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/domain/conflict"
	"github.com/cellestias/git-tide/internal/domain/policy"
	"github.com/cellestias/git-tide/internal/domain/secret"
	"github.com/cellestias/git-tide/internal/git/repository"
)

const (
	maxMessageSize = 1 << 20
	maxFindings    = 20
)

// PreCommit rejects commits on protected branches and staged likely secrets.
func PreCommit(ctx context.Context, repo *repository.Repository, cfg domain.Config) error {
	current, err := repo.CurrentBranch(ctx)
	if err != nil && !errors.Is(err, repository.ErrDetachedHead) {
		return apperr.Wrap(apperr.CodeExternal, "cannot determine current branch", "", err)
	}
	if d := (policy.Protection{Protected: cfg.Branches.Protected}).Commit(current); !d.Allowed {
		return apperr.New(apperr.CodePolicy, d.Reason, "to bypass this local guardrail once: git commit --no-verify")
	}

	added, err := repo.StagedAdditions(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect staged changes", "", err)
	}

	var markers []string
	for _, l := range added {
		if conflict.IsMarker(l.Text) {
			markers = append(markers, fmt.Sprintf("%s:%d", l.Path, l.Line))
		}
	}
	if len(markers) > 0 {
		return apperr.New(apperr.CodePolicy, summary(fmt.Sprintf("%d merge conflict marker(s) in staged changes:", len(markers)), markers),
			"resolve the conflict and remove the <<<<<<< / >>>>>>> lines; if a line is meant to be there add `"+conflict.AllowMarker+"` to it")
	}

	if !cfg.Secrets.Enabled {
		return nil
	}
	detector := secret.NewDetector(cfg.SecretRules())
	var findings []string
	for _, l := range added {
		for _, f := range detector.ScanLine(l.Path, l.Line, l.Text) {
			findings = append(findings, f.String())
		}
	}
	if len(findings) == 0 {
		return nil
	}
	return apperr.New(apperr.CodePolicy, summary(fmt.Sprintf("%d likely secret(s) in staged changes:", len(findings)), findings),
		"remove the secret from the change; for a false positive add `"+secret.AllowMarker+"` to that line")
}

// summary renders a heading and up to maxFindings indented items.
func summary(heading string, items []string) string {
	var b strings.Builder
	b.WriteString(heading)
	for i, it := range items {
		if i == maxFindings {
			fmt.Fprintf(&b, "\n  ... and %d more", len(items)-maxFindings)
			break
		}
		fmt.Fprintf(&b, "\n  %s", it)
	}
	return b.String()
}

// CommitMsg validates the message in the file Git passes to the commit-msg hook.
func CommitMsg(cfg domain.Config, file string) error {
	f, err := os.Open(file)
	if err != nil {
		return apperr.Wrap(apperr.CodeGeneral, "cannot read commit message file", "", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxMessageSize))
	if err != nil {
		return apperr.Wrap(apperr.CodeGeneral, "cannot read commit message file", "", err)
	}
	rules := commit.Rules{RequireScope: cfg.Commits.RequireScope}
	if err := rules.Validate(string(data)); err != nil {
		return apperr.New(apperr.CodePolicy, "invalid commit message: "+err.Error(), "example: "+rules.Example())
	}
	return nil
}

// PrePush rejects pushes that update or delete protected branches. in carries
// the lines Git writes to the hook: "<local ref> <local sha> <remote ref> <remote sha>".
func PrePush(cfg domain.Config, in io.Reader) error {
	prot := policy.Protection{Protected: cfg.Branches.Protected}
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 4 {
			continue
		}
		name, ok := strings.CutPrefix(fields[2], "refs/heads/")
		if !ok {
			continue
		}
		if d := prot.Push(name); !d.Allowed {
			return apperr.New(apperr.CodePolicy, d.Reason, "to bypass this local guardrail once: git push --no-verify")
		}
	}
	return sc.Err()
}
