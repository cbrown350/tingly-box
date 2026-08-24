// Package errkind defines the sentinel error kinds that storage and
// service layers attach to their errors so HTTP handlers can classify
// failures with errors.Is instead of matching message substrings —
// substring matching misfires when user input (names, ids) is
// interpolated into the message.
//
// The package is dependency-free on purpose: internal/db marks its
// errors with these kinds and internal/apierr maps them to HTTP
// responses, without either importing the other.
package errkind

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound marks a lookup miss. Maps to HTTP 404.
	ErrNotFound = errors.New("not found")
	// ErrConflict marks a uniqueness or state conflict (duplicate name,
	// still-referenced resource, disabled target). Maps to HTTP 409.
	ErrConflict = errors.New("conflict")
	// ErrInvalid marks a rejected input (empty name, over-long value)
	// whose message is safe to show the caller. Maps to HTTP 400.
	ErrInvalid = errors.New("invalid")
)

// marked carries a kind alongside the real error. Error() shows only the
// real error's message — the kind never leaks into the text — while the
// multi-target Unwrap makes errors.Is match both the kind and the cause.
type marked struct {
	kind error
	err  error
}

func (m *marked) Error() string   { return m.err.Error() }
func (m *marked) Unwrap() []error { return []error{m.kind, m.err} }

// Mark attaches kind to err without changing its message.
func Mark(kind, err error) error {
	return &marked{kind: kind, err: err}
}

// Newf builds an error with the given message, marked with kind.
func Newf(kind error, format string, args ...any) error {
	return Mark(kind, fmt.Errorf(format, args...))
}
