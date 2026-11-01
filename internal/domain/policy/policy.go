// Package policy evaluates repository operations against configured rules.
// It is pure: it receives facts and returns decisions.
package policy

import (
	"fmt"

	"github.com/cellestias/git-tide/internal/domain/branch"
)

// Decision is an allow/deny outcome with the reason for a denial.
type Decision struct {
	Allowed bool
	Reason  string
}

var allow = Decision{Allowed: true}

// Protection evaluates operations against a set of protected branches.
type Protection struct {
	Protected []string
}

// Commit decides whether a commit may be created on current. An empty current
// (detached HEAD) is allowed because no branch is being protected.
func (p Protection) Commit(current string) Decision {
	if current != "" && branch.Contains(p.Protected, current) {
		return Decision{Reason: fmt.Sprintf("direct commits to protected branch %q are not allowed; create a working branch first", current)}
	}
	return allow
}

// Push decides whether updating or deleting the remote branch is allowed.
func (p Protection) Push(remoteBranch string) Decision {
	if branch.Contains(p.Protected, remoteBranch) {
		return Decision{Reason: fmt.Sprintf("pushing to protected branch %q is not allowed; open a Pull Request instead", remoteBranch)}
	}
	return allow
}
