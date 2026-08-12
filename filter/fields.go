package filter

import (
	"slices"
	"strings"

	celast "github.com/google/cel-go/common/ast"
)

// Fields returns the field names of the [event.Event] type read by this
// expression, as dotted paths without the root object name.
func (e *Env) Fields(expr string) ([]string, error) {
	ast, iss := e.env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, newCompileError(expr, iss)
	}
	return eventPaths(ast.NativeRep().Expr()), nil
}

func eventPaths(root celast.Expr) []string {
	var paths []string

	celast.PostOrderVisit(root, celast.NewExprVisitor(func(e celast.Expr) {
		if e.Kind() != celast.SelectKind {
			return
		}
		if path, ok := selectPath(e); ok {
			paths = append(paths, path)
		}
	}))
	return leaves(paths)
}

// selectPath returns the path a CEL field selection expression reads,
// and whether it reads from the event root.
func selectPath(e celast.Expr) (string, bool) {
	var segments []string

	for e.Kind() == celast.SelectKind {
		sel := e.AsSelect()
		segments = append(segments, sel.FieldName())
		e = sel.Operand()
	}
	if e.Kind() != celast.IdentKind || e.AsIdent() != variableName {
		return "", false
	}
	slices.Reverse(segments)

	return strings.Join(segments, "."), true
}

// leaves returns the paths that no other path extends. The walk records
// every step of a chain, so regarding sits beside regarding.kind while
// only the second is a field the expression reads. The result is sorted
// and contains no duplicate.
func leaves(paths []string) []string {
	var out []string

	for _, path := range paths {
		extended := slices.ContainsFunc(paths, func(other string) bool {
			return strings.HasPrefix(other, path+".")
		})
		if extended || slices.Contains(out, path) {
			continue
		}
		out = append(out, path)
	}
	slices.Sort(out)

	return out
}
