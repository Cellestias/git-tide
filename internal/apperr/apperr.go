// Package apperr defines the error type that carries a CLI exit code.
package apperr

import (
	"errors"
	"fmt"
)

// Exit codes returned by the tide binary.
const (
	CodeOK         = 0
	CodeGeneral    = 1
	CodeUsage      = 2
	CodeConfig     = 3
	CodePolicy     = 4
	CodeExternal   = 5
	CodeUnsafeRepo = 6
)

// Error is an application error with an exit code and an optional hint that
// tells the user what to do next.
type Error struct {
	Code int
	Msg  string
	Hint string
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

func New(code int, msg, hint string) *Error {
	return &Error{Code: code, Msg: msg, Hint: hint}
}

func Wrap(code int, msg, hint string, err error) *Error {
	return &Error{Code: code, Msg: msg, Hint: hint, Err: err}
}

func Usage(format string, args ...any) *Error {
	return &Error{Code: CodeUsage, Msg: fmt.Sprintf(format, args...)}
}

// CodeOf returns the exit code for err, defaulting to CodeGeneral.
func CodeOf(err error) int {
	if err == nil {
		return CodeOK
	}
	var ae *Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return CodeGeneral
}

// HintOf returns the hint attached to err, if any.
func HintOf(err error) string {
	var ae *Error
	if errors.As(err, &ae) {
		return ae.Hint
	}
	return ""
}
