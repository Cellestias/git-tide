package doctor

import (
	"github.com/cellestias/git-tide/internal/apperr"
	"github.com/cellestias/git-tide/internal/output"
)

type jsonCheck struct {
	Level  string `json:"level"` // "ok", "warn" or "fail"
	Name   string `json:"name"`
	Detail string `json:"detail"`
	Hint   string `json:"hint"`
}

// WriteJSON prints the checks as one JSON object on stdout:
// {"ok": bool, "warnings": n, "failures": n, "checks": [...]}. ok is false when a check failed.
func WriteJSON(p output.Printer, checks []Check) error {
	out := struct {
		OK       bool        `json:"ok"`
		Warnings int         `json:"warnings"`
		Failures int         `json:"failures"`
		Checks   []jsonCheck `json:"checks"`
	}{Checks: make([]jsonCheck, 0, len(checks))}
	for _, c := range checks {
		switch c.Level {
		case Warn:
			out.Warnings++
		case Fail:
			out.Failures++
		}
		out.Checks = append(out.Checks, jsonCheck{c.Level.String(), c.Name, c.Detail, c.Hint})
	}
	out.OK = out.Failures == 0
	if err := p.JSON(out); err != nil {
		return apperr.Wrap(apperr.CodeGeneral, "cannot write JSON", "", err)
	}
	return nil
}
