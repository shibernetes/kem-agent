package diag

import (
	"errors"
	"fmt"

	"github.com/goccy/go-yaml/ast"
)

// Error is a configuration error positioned at a document node.
type Error struct {
	node ast.Node
	err  error
}

// At returns err positioned at the given node.
func At(node ast.Node, err error) error {
	return &Error{node: node, err: err}
}

// Atf returns an error positioned at the given node.
// The format takes the same verbs as [fmt.Errorf], %w included.
func Atf(node ast.Node, format string, args ...any) error {
	return At(node, fmt.Errorf(format, args...))
}

// Error implements the error interface.
// It prefixes the message with the position at which the error
// occurred in the decoded document.
func (e *Error) Error() string {
	if e.node == nil {
		return e.err.Error()
	}
	tk := e.node.GetToken()
	if tk == nil {
		return e.err.Error()
	}
	return fmt.Sprintf("[%d:%d] %s", tk.Position.Line, tk.Position.Column, e.err)
}

// Unwrap implements the interface [errors.Is] and [errors.As] walk,
// so a cause wrapped with %w stays reachable through the position.
func (e *Error) Unwrap() error {
	return e.err
}

// Node returns the document node the error was raised at.
func (e *Error) Node() ast.Node {
	return e.node
}

// A PathError wraps an error with the relative path of the field for
// which it was raised.
//
// A component is aware of its own fields, but not of the surrounding
// document, so it cites only the field name and leaves to the caller
// the context prefix.
type PathError struct {
	path string
	err  error
}

// Path returns err reported at the given path, relative to its parent.
func Path(path string, err error) error {
	return &PathError{path: path, err: err}
}

// Pathf returns an error for the given path, relative to its parent.
// The format takes the same verbs as [fmt.Errorf].
func Pathf(path, format string, args ...any) error {
	return Path(path, fmt.Errorf(format, args...))
}

// Prefix reports err under a parent block, in the message and in the path.
// An error that references no specific field of its own is reported at the
// block itself. A nil error is returned as-is.
func Prefix(err error, segment string) error {
	if err == nil {
		return nil
	}
	path := segment

	if inner, ok := errors.AsType[*PathError](err); ok {
		path += "." + inner.path
	}
	return &PathError{
		path: path,
		err:  fmt.Errorf("%s: %w", segment, err),
	}
}

// Prefixf adds a parent segment to the path of a [PathError].
// The format builds the segment, not a message.
func Prefixf(err error, format string, args ...any) error {
	return Prefix(err, fmt.Sprintf(format, args...))
}

// Error implements the error interface.
func (e *PathError) Error() string {
	return e.err.Error()
}

// Unwrap implements the interface [errors.Is] and [errors.As] walk.
func (e *PathError) Unwrap() error {
	return e.err
}

// Path returns the path the error was raised at.
func (e *PathError) Path() string {
	return e.path
}
