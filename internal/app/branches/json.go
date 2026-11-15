package branches

import (
	"time"

	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/output"
)

type jsonUpstream struct {
	Name   string `json:"name"`
	Ahead  int    `json:"ahead"`
	Behind int    `json:"behind"`
	Gone   bool   `json:"gone"` // the branch no longer exists on the remote
}

type jsonBase struct {
	Name   string `json:"name"`
	Ahead  int    `json:"ahead"`
	Behind int    `json:"behind"`
}

// jsonHosted is the provider's view, only with --hosted. A nil field means the
// provider could not be asked (the command warns on stderr and goes on).
type jsonHosted struct {
	Protected *bool `json:"protected"`
	Request   *int  `json:"request"` // number of the open pull/merge request, null when none or unknown
}

type jsonBranch struct {
	Name       string        `json:"name"`
	Current    bool          `json:"current"`
	RemoteOnly bool          `json:"remote_only"`
	Updated    time.Time     `json:"updated"` // date of the last commit
	Author     string        `json:"author"`  // of the last commit
	Email      string        `json:"email"`
	Upstream   *jsonUpstream `json:"upstream"` // null when none (and for remote-only branches)
	Base       *jsonBase     `json:"base"`     // null on the base branch itself or when it cannot be found
	Protected  bool          `json:"protected"`
	Merged     bool          `json:"merged"`
	Hosted     *jsonHosted   `json:"hosted,omitempty"`
}

func renderJSON(p output.Printer, rows []row, gh *githubInfo) error {
	out := make([]jsonBranch, 0, len(rows))
	for _, r := range rows {
		b := jsonBranch{
			Name: r.ref.Name, Current: r.ref.Current, RemoteOnly: r.remoteOnly, Updated: r.ref.Updated,
			Author: r.ref.AuthorName, Email: r.ref.AuthorEmail, Protected: r.protected, Merged: r.merged,
		}
		if !r.remoteOnly && (r.ref.Upstream != "" || r.ref.UpGone) {
			b.Upstream = &jsonUpstream{r.ref.Upstream, r.ref.UpAhead, r.ref.UpBehind, r.ref.UpGone}
		}
		if r.hasBase {
			b.Base = &jsonBase{r.base, r.ahead, r.behind}
		}
		if gh != nil {
			h := &jsonHosted{}
			if !gh.protectedErr {
				v := gh.protected[r.ref.Name]
				h.Protected = &v
			}
			if n, ok := gh.prs[r.ref.Name]; ok && !gh.prsErr {
				h.Request = &n
			}
			b.Hosted = h
		}
		out = append(out, b)
	}
	if err := p.JSON(out); err != nil {
		return apperr.Wrap(apperr.CodeGeneral, "cannot write JSON", "", err)
	}
	return nil
}
