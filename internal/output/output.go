// Package output renders human-readable progress and results.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"unicode/utf8"
)

// Printer writes status lines. Progress goes to Out; problems go to Err.
// ColorOut and ColorErr say whether each stream takes ANSI colours; the zero
// value prints plain text, which is what tests and pipes get.
type Printer struct {
	Out io.Writer
	Err io.Writer

	ColorOut bool
	ColorErr bool
}

// ColorEnabled reports whether w should receive colours: it must be a
// terminal, NO_COLOR must be unset, and TERM must not be "dumb".
// CLICOLOR_FORCE=1 forces colours on for non-terminals (not over NO_COLOR).
func ColorEnabled(w io.Writer, getenv func(string) string) bool {
	if getenv("NO_COLOR") != "" || runtime.GOOS == "windows" {
		return false
	}
	if f := getenv("CLICOLOR_FORCE"); f != "" && f != "0" {
		return true
	}
	if getenv("TERM") == "dumb" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func paint(on bool, code, s string) string {
	if !on || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Styles for text written to Out. They return s unchanged when colour is off.
func (p Printer) Green(s string) string   { return paint(p.ColorOut, "32", s) }
func (p Printer) Red(s string) string     { return paint(p.ColorOut, "31", s) }
func (p Printer) Yellow(s string) string  { return paint(p.ColorOut, "33", s) }
func (p Printer) Magenta(s string) string { return paint(p.ColorOut, "35", s) }
func (p Printer) Bold(s string) string    { return paint(p.ColorOut, "1", s) }
func (p Printer) Dim(s string) string     { return paint(p.ColorOut, "2", s) }

// Dim for the error stream, used by hints.
func (p Printer) DimErr(s string) string { return paint(p.ColorErr, "2", s) }

func (p Printer) Step(format string, args ...any) {
	p.line(p.Out, paint(p.ColorOut, "36", "→"), format, args)
}

func (p Printer) Info(format string, args ...any) {
	p.line(p.Out, " ", format, args)
}

func (p Printer) Success(format string, args ...any) {
	p.line(p.Out, paint(p.ColorOut, "32", "✓"), format, args)
}

func (p Printer) Warn(format string, args ...any) {
	p.line(p.Err, paint(p.ColorErr, "33", "!"), format, args)
}

func (p Printer) Fail(format string, args ...any) {
	p.line(p.Err, paint(p.ColorErr, "31", "✗"), format, args)
}

func (p Printer) Line(format string, args ...any) {
	_, _ = fmt.Fprintf(p.Out, format+"\n", args...)
}

// Note writes a progress line to Err so that stdout stays clean for data.
func (p Printer) Note(format string, args ...any) {
	p.line(p.Err, paint(p.ColorErr, "36", "→"), format, args)
}

func (p Printer) line(w io.Writer, symbol, format string, args []any) {
	_, _ = fmt.Fprintf(w, "%s "+format+"\n", append([]any{symbol}, args...)...)
}

// Table writes rows as aligned columns; the first row is the header and is
// bold. style may restyle a cell, and runs after padding is computed from the
// plain text so colours never break alignment. style may be nil.
func (p Printer) Table(rows [][]string, style func(row, col int, cell string) string) {
	var widths []int
	for _, r := range rows {
		for i, c := range r {
			if n := utf8.RuneCountInString(c); i >= len(widths) {
				widths = append(widths, n)
			} else if n > widths[i] {
				widths[i] = n
			}
		}
	}
	for ri, r := range rows {
		var b strings.Builder
		for ci, c := range r {
			text := c
			switch {
			case ri == 0:
				text = p.Bold(c)
			case style != nil:
				text = style(ri, ci, c)
			}
			b.WriteString(text)
			if ci < len(r)-1 {
				b.WriteString(strings.Repeat(" ", widths[ci]-utf8.RuneCountInString(c)+2))
			}
		}
		_, _ = fmt.Fprintln(p.Out, b.String())
	}
}

// JSON writes v as indented JSON on Out, for programs that read the result.
func (p Printer) JSON(v any) error {
	enc := json.NewEncoder(p.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
