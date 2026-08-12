package units

import (
	"github.com/goccy/go-yaml/ast"
)

// asScalar returns the text of a scalar YAML node, as it is written in
// the original document, and whether it is one. It reports false for a
// mapping, a sequence, or a node carrying no token.
func asScalar(node ast.Node) (string, bool) {
	if _, ok := node.(ast.ScalarNode); !ok {
		return "", false
	}
	tk := node.GetToken()
	if tk == nil {
		return "", false
	}
	return tk.Value, true
}
