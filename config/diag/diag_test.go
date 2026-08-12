package diag

import (
	"errors"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

const (
	shutdownTimeoutNodePath = "$.service.shutdown_timeout"
)

var errCause = errors.New("cause")

func TestErrorMessage(t *testing.T) {
	cases := map[string]struct {
		node   ast.Node
		format string
		args   []any
		want   string
	}{
		"the position prefixes the message": {
			node:   valueNode(t, shutdownTimeoutNodePath),
			format: "invalid duration %q",
			args:   []any{"30x"},
			want:   `[2:21] invalid duration "30x"`,
		},
		"the line follows the node": {
			node:   valueNode(t, "$.service.cel_eval_timeout"),
			format: "invalid duration %q",
			args:   []any{"250q"},
			want:   `[3:21] invalid duration "250q"`,
		},
		"the column follows the node, from the opening quote": {
			node:   valueNode(t, "$.service.http_server.addr"),
			format: "invalid address %q",
			args:   []any{"8080"},
			want:   `[5:11] invalid address "8080"`,
		},
		"a wrapped cause is part of the message": {
			node:   valueNode(t, shutdownTimeoutNodePath),
			format: "invalid duration: %w",
			args:   []any{errCause},
			want:   "[2:21] invalid duration: cause",
		},
		"a node without a token drops the position": {
			node:   &ast.StringNode{BaseNode: &ast.BaseNode{}},
			format: "invalid duration %q",
			args:   []any{"30x"},
			want:   `invalid duration "30x"`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Atf(tc.node, tc.format, tc.args...).Error(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestErrorUnwrapsItsCause(t *testing.T) {
	err := Atf(valueNode(t, shutdownTimeoutNodePath), "invalid duration: %w", errCause)

	if !errors.Is(err, errCause) {
		t.Errorf("got %v, expected it to wrap %v", err, errCause)
	}
	if _, ok := errors.AsType[*Error](err); !ok {
		t.Errorf("got %T, want a *Error", err)
	}
}

func TestErrorKeepsItsNode(t *testing.T) {
	err, ok := errors.AsType[*Error](Atf(valueNode(t, shutdownTimeoutNodePath), "oops"))
	if !ok {
		t.Fatalf("got %T, want a *Error", err)
	}
	if got := err.Node().GetPath(); got != shutdownTimeoutNodePath {
		t.Errorf("got path %q, want %q", got, shutdownTimeoutNodePath)
	}
}

func valueNode(t *testing.T, nodePath string) ast.Node {
	t.Helper()

	file, err := parser.ParseFile("testdata/config.yaml", 0)
	if err != nil {
		t.Fatalf("failed to parse document: %v", err)
	}
	path, err := yaml.PathString(nodePath)
	if err != nil {
		t.Fatalf("failed to build path: %v", err)
	}
	node, err := path.FilterFile(file)
	if err != nil {
		t.Fatalf("failed to filter document: %v", err)
	}
	return node
}
