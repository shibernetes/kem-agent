package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml/ast"

	"github.com/shibernetes/kem-agent/config/diag"
)

// interpolate recursively walks the config node tree and interpolates
// ${VAR} references in place in every string value.
// Mapping keys are not interpolated.
func interpolate(node ast.Node) error {
	switch n := node.(type) {
	case *ast.MappingNode:
		for _, v := range n.Values {
			if err := interpolate(v); err != nil {
				return err
			}
		}
	case *ast.MappingValueNode:
		return interpolate(n.Value)
	case *ast.SequenceNode:
		for _, v := range n.Values {
			if err := interpolate(v); err != nil {
				return err
			}
		}
	case *ast.AnchorNode:
		return interpolate(n.Value)
	case *ast.TagNode:
		return interpolate(n.Value)
	case *ast.LiteralNode:
		return expandNode(n.Value)
	case *ast.StringNode:
		return expandNode(n)
	}
	return nil
}

// expandNode expands a scalar node, writing the result to both the node
// and its token, since a NodeUnmarshaler reads the token while a plain
// field reads the node. The token origin is not modified, so an error
// excerpt still shows the reference rather than the value it expanded to.
func expandNode(node *ast.StringNode) error {
	expanded, err := expandVar(node.Value, envVarResolver)
	if err != nil {
		return diag.Atf(node, "%w", err)
	}
	if expanded == node.Value {
		return nil
	}
	node.Value = expanded
	node.GetToken().Value = expanded

	return nil
}

// expandVar expands all ${VAR} references in s with the values returned
// by the resolver.
//
// $$ is treated as a literal dollar sign, and a malformed reference is
// reported as an error whatever the resolver returns.
func expandVar(s string, resolve resolver) (string, error) {
	if !strings.ContainsRune(s, '$') {
		return s, nil
	}
	var sb strings.Builder
	sb.Grow(len(s))

	for i := 0; i < len(s); {
		if s[i] != '$' || i+1 == len(s) {
			sb.WriteByte(s[i])
			i++
			continue
		}
		switch s[i+1] {
		case '$':
			sb.WriteByte('$')
			i += 2
		case '{':
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				return "", fmt.Errorf("unterminated variable reference in %q", s)
			}
			name := s[i+2 : i+2+end]
			value, err := resolve(name)
			if err != nil {
				return "", err
			}
			sb.WriteString(value)
			i += end + 3
		default:
			sb.WriteByte('$')
			i++
		}
	}
	return sb.String(), nil
}

// A resolver returns the value a ${VAR} reference expands to.
type resolver func(name string) (string, error)

// envVarResolver returns the value of the named environment variable.
func envVarResolver(name string) (string, error) {
	if !isValidVarName(name) {
		return "", fmt.Errorf("invalid variable name %q", name)
	}
	value, ok := os.LookupEnv(name)
	if !ok {
		return "", fmt.Errorf("environment variable %q is unset", name)
	}
	return value, nil
}

func isValidVarName(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		switch c := s[i]; {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '_':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
