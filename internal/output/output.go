// Package output renders human-readable progress and results.
package output

import (
	"fmt"
	"io"
)

// Printer writes status lines. Progress goes to Out; problems go to Err.
type Printer struct {
	Out io.Writer
	Err io.Writer
}

func (p Printer) Step(format string, args ...any) {
	fmt.Fprintf(p.Out, "→ "+format+"\n", args...)
}

func (p Printer) Info(format string, args ...any) {
	fmt.Fprintf(p.Out, "  "+format+"\n", args...)
}

func (p Printer) Success(format string, args ...any) {
	fmt.Fprintf(p.Out, "✓ "+format+"\n", args...)
}

func (p Printer) Warn(format string, args ...any) {
	fmt.Fprintf(p.Err, "! "+format+"\n", args...)
}

func (p Printer) Fail(format string, args ...any) {
	fmt.Fprintf(p.Err, "✗ "+format+"\n", args...)
}

func (p Printer) Line(format string, args ...any) {
	fmt.Fprintf(p.Out, format+"\n", args...)
}
