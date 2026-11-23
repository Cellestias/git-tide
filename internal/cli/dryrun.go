package cli

import (
	"fmt"
	"io"

	"github.com/cellestias/git-tide/internal/apperr"
)

// finishDry prints what a --dry-run would have done and returns the exit code.
//
// A step that was skipped answers with success and no output, so the use case
// goes on; when a later step needs the result of an earlier one it fails. That
// failure, after at least one skipped step, ends the plan (the exit code is 0 and
// the reason is printed). A failure with nothing skipped is real: a precondition
// or a policy refused the command, and it would be refused the same way.
func (e *env) finishDry(err error, errOut io.Writer) int {
	if e.dryClose != nil {
		defer e.dryClose()
	}
	steps := e.plan.Steps()
	if err != nil && len(steps) == 0 {
		e.p.Fail("%s", err)
		if h := apperr.HintOf(err); h != "" {
			_, _ = fmt.Fprintf(errOut, "  %s\n", e.p.DimErr("hint: "+h))
		}
		_, _ = fmt.Fprintln(errOut, e.p.DimErr("dry run: nothing was changed; the command would be refused the same way"))
		return apperr.CodeOf(err)
	}
	stopped := ""
	if err != nil {
		stopped = err.Error()
	}
	if e.dryJSON {
		if err == nil {
			return apperr.CodeOK // the action already printed its document, with the steps
		}
		doc := actionDoc{OK: true, Action: e.dryName, Details: map[string]any{}, DryRun: &dryInfo{Would: steps, Stopped: stopped}}
		if jerr := e.p.JSON(doc); jerr != nil {
			return apperr.CodeGeneral
		}
		return apperr.CodeOK
	}
	out := e.p.Out
	if len(steps) == 0 {
		_, _ = fmt.Fprintln(out, "Dry run: nothing would change.")
		return apperr.CodeOK
	}
	_, _ = fmt.Fprintln(out, "Dry run: nothing was changed. It would:")
	for i, s := range steps {
		_, _ = fmt.Fprintf(out, "  %d. %s\n", i+1, s)
	}
	if stopped != "" {
		_, _ = fmt.Fprintf(out, "Stopped here: %s\n", stopped)
		_, _ = fmt.Fprintln(out, "The steps after it depend on the ones above, so they cannot be listed.")
	}
	return apperr.CodeOK
}
