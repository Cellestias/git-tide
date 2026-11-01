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

	if !cfg.Secrets.Enabled {
		return nil
	}
	added, err := repo.StagedAdditions(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot inspect staged changes", "", err)
	}
	detector := secret.NewDetector(cfg.SecretRules())
	var findings []secret.Finding
	for _, l := range added {
		findings = append(findings, detector.ScanLine(l.Path, l.Line, l.Text)...)
	}
	if len(findings) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d likely secret(s) in staged changes:", len(findings))
	for i, f := range findings {
		if i == maxFindings {
			fmt.Fprintf(&b, "\n  ... and %d more", len(findings)-maxFindings)
			break
		}
		fmt.Fprintf(&b, "\n  %s", f)
	}
	return apperr.New(apperr.CodePolicy, b.String(),
		"remove the secret from the change; for a false positive add `"+secret.AllowMarker+"` to that line")
}

// CommitMsg validates the message in the file Git passes to the commit-msg hook.
func CommitMsg(cfg domain.Config, file string) error {
	f, err := os.Open(file)
	if err != nil {
		return apperr.Wrap(apperr.CodeGeneral, "cannot read commit message file", "", err)
	}
	defer f.Close()
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
