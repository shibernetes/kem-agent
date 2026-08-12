package filter

import (
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
)

// CompileError reports why an expression was rejected. It carries
// every issue raised rather than the first, so a caller can point
// at each one in the expression it came from.
type CompileError struct {
	Expr   string
	Issues []CompileIssue
}

// A CompileIssue represents a problem in an expression. Its position
// line is offset by 1, and the column is a 0-based index counted
// within that line.
//
// A zero line means the issue has no position, which happens for a
// problem found after the expression was parsed. A zero length means
// it points at no symbol.
type CompileIssue struct {
	Line    int
	Column  int
	Length  int
	Message string
}

// Error implements the error interface.
func (e *CompileError) Error() string {
	messages := make([]string, len(e.Issues))
	for i, issue := range e.Issues {
		messages[i] = issue.Message
	}
	return fmt.Sprintf("failed to compile expression %q: %s", e.Expr, strings.Join(messages, ", "))
}

// SourceLine returns line n of the expression, counted from one, or an
// empty string when there is no such line. A number below one stands for
// the first line, which is where an issue with no position is shown.
func (e *CompileError) SourceLine(n int) string {
	return lineOf(e.Expr, n)
}

func newCompileError(expr string, iss *cel.Issues) *CompileError {
	var (
		errs   = iss.Errors()
		issues = make([]CompileIssue, len(errs))
	)
	for i, issue := range errs {
		issues[i] = CompileIssue{Message: issue.Message}
		if issue.Location != nil {
			issues[i].Line = issue.Location.Line()
			issues[i].Column, issues[i].Length = symbolSpan(expr, issue.Location.Line(), issue.Location.Column())
		}
	}
	return &CompileError{Expr: expr, Issues: issues}
}

func newCompileErrorf(expr, format string, args ...any) *CompileError {
	return &CompileError{
		Expr:   expr,
		Issues: []CompileIssue{{Message: fmt.Sprintf(format, args...)}},
	}
}

// symbolSpan returns the column an issue should point at, and the width
// of the symbol it represents.
//
// cel-go reports a field selection at the dot symbol, so the column is
// shifted by one to the right to start at the first rune of the identifier.
// A position that doesn't point at one, such as a call reported at its open
// parenthesis, gets a column but no width.
func symbolSpan(expr string, line, column int) (int, int) {
	// cel-go counts a column from the start of the issue's own line, so the
	// span is measured within that line rather than the whole expression.
	runes := []rune(lineOf(expr, line))
	if column < 0 || column >= len(runes) {
		return column, 0
	}
	if runes[column] == '.' {
		column++
	}
	return column, identifierLen(runes[column:])
}

// lineOf returns line n of an expression, counted from one. The first line
// is returned for any number below one. It returns an empty string when the
// expression has no such line.
func lineOf(expr string, n int) string {
	lines := strings.Split(expr, "\n")
	if n = max(n, 1); n > len(lines) {
		return ""
	}
	return lines[n-1]
}

// identifierLen returns the length of the valid CEL identifier immediately
// following a dot symbol.
func identifierLen(runes []rune) int {
	if len(runes) == 0 {
		return 0
	}
	// The first character after the dot MUST be a letter or underscore.
	// See the CEL grammar: https://github.com/cel-expr/cel-spec/blob/master/doc/langdef.md#syntax
	if !isLetterOrUnderscore(runes[0]) {
		return 0
	}
	for i := 1; i < len(runes); i++ {
		if !isValidIdentifierCharset(runes[i]) {
			return i
		}
	}
	return len(runes)
}

func isValidIdentifierCharset(r rune) bool {
	return isLetterOrUnderscore(r) || ('0' <= r && r <= '9')
}

func isLetterOrUnderscore(r rune) bool {
	return r == '_' ||
		('a' <= r && r <= 'z') ||
		('A' <= r && r <= 'Z')
}
