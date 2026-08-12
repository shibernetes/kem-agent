package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/sink"
)

// Result is the result of a configuration parsing.
type Result struct {
	Config   *Config
	Warnings []Warning
	doc      ast.Node
}

// FilterNode returns the node holding a pipeline's filter expression
// at index, or nil when it doesn't exists.
func (r *Result) FilterNode(pipeline string, index int) ast.Node {
	return sequenceItem(mappingValue(r.doc, "pipelines", pipeline, "filters"), index)
}

// SinkFieldNode returns the node holding a sink field, addresses by
// its path under its parent block, or nil when it doesn't exists.
func (r *Result) SinkFieldNode(sink, path string) ast.Node {
	return nodeAtPath(mappingValue(r.doc, "sinks", sink), path)
}

// FilterLine returns the line number in the document holding line n of a
// pipeline's filter expression, or zero when the configuration declares
// no such filter. A number below one stands for the first line.
//
// Only a literal block scalar keeps every line of its expression, so it
// is the only style mapped line for line. A folded or flow scalar is
// reported at the line its content starts on.
func (r *Result) FilterLine(pipeline string, index, n int) int {
	node := r.FilterNode(pipeline, index)
	if node == nil {
		return 0
	}
	tk := node.GetToken()
	if tk == nil {
		return 0
	}
	// A block scalar's token is its indicator, and its content starts
	// on the line below it.
	switch tk.Type {
	case token.LiteralType:
		return tk.Position.Line + max(n, 1)
	case token.FoldedType:
		return tk.Position.Line + 1
	default:
		return tk.Position.Line
	}
}

// A Warning represents one configuration issue.
type Warning struct {
	Node    ast.Node
	Message string
}

// Position returns the line and column a warning was raised at,
// or zeroes when it has no position.
func (w Warning) Position() (int, int) {
	return tokenPosition(w.Node)
}

// Load reads and parses the file at path.
func Load(path string, factories sink.Factories) (*Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", path, err)
	}
	return Parse(data, factories)
}

// Parse parses a YAML config document. It expands ${VAR} references,
// decodes the document onto the defaults, resolves each component against
// the factory its type references, and validates the result. Any failure
// rejects the whole config.
//
// A config that decodes but fails validation is returned beside the error,
// so its warnings can still be reported. Every other failure returns no
// result.
//
// Interpolation rewrites the parsed nodes rather than the raw bytes, so
// an expanded value can never corrupt the surrounding document.
func Parse(data []byte, factories sink.Factories) (*Result, error) {
	doc, err := parseDocument(data)
	if err != nil {
		return nil, err
	}
	cfg := DefaultConfig()

	if err := interpolate(doc); err != nil {
		return nil, err
	}
	if err := yaml.NodeToValue(doc, &cfg, yaml.Strict()); err != nil {
		return nil, err
	}

	if err := cfg.resolve(doc, factories); err != nil {
		return nil, err
	}
	// The warnings are gathered first, since a config failing validation
	// is still returned for them.
	cfg.suggestResources(doc)
	res := &Result{Config: &cfg, Warnings: cfg.warnings, doc: doc}

	if err := cfg.Validate(); err != nil {
		return res, err
	}
	return res, nil
}

// Diagnose returns the message of a configuration error, and the line and
// column it was raised at, or zeroes if it has no position.
//
// The message does not contain the position, so they can be reported separately.
func Diagnose(err error) (string, int, int) {
	if de, ok := errors.AsType[*diag.Error](err); ok {
		message := err.Error()
		if cause := errors.Unwrap(de); cause != nil {
			message = cause.Error()
		}
		line, column := tokenPosition(de.Node())
		return message, line, column
	}
	if ye, ok := errors.AsType[yaml.Error](err); ok {
		line, column := 0, 0
		if token := ye.GetToken(); token != nil {
			line, column = token.Position.Line, token.Position.Column
		}
		return ye.GetMessage(), line, column
	}
	return err.Error(), 0, 0
}

// Span returns the width of the key an error was raised at, or zero when
// it was raised for anything else.
//
// It used to position a caret under the error source. Only a key gets one,
// since a value can run to any length.
func Span(err error) int {
	if de, ok := errors.AsType[*diag.Error](err); ok {
		if node := de.Node(); node != nil {
			return tokenWidth(node.GetToken())
		}
		return 0
	}
	if ye, ok := errors.AsType[yaml.Error](err); ok {
		return tokenWidth(ye.GetToken())
	}
	return 0
}

func parseDocument(data []byte) (ast.Node, error) {
	file, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	if len(file.Docs) != 1 {
		return nil, fmt.Errorf("the configuration must be a single YAML document, found %d", len(file.Docs))
	}
	switch body := file.Docs[0].Body.(type) {
	case *ast.MappingNode:
		return body, nil
	case nil, *ast.CommentGroupNode:
		return nil, errors.New("the configuration is empty")
	default:
		return nil, diag.Atf(body, "the configuration must be an object")
	}
}

// fieldNode returns the AST node positioning the given error.
//
// The key is the fallback rather than parent because of how the YAML tree
// records positions. A block mapping takes its position from its first
// entry, and an entry is positioned at the colon between its key and its
// value, so parent points inside the section. The key node is positioned
// at the name itself.
func fieldNode(parent, key ast.Node, err error) ast.Node {
	if path, ok := errors.AsType[*diag.PathError](err); ok {
		if node := nodeAtPath(parent, path.Path()); node != nil {
			return node
		}
	}
	return key
}

// nodeAt returns the node corresponding to a field path, or nil when
// it cannot be found in the document.
func nodeAt(doc ast.Node, path string) ast.Node {
	p, err := yaml.PathString(path)
	if err != nil {
		return nil
	}
	node, err := p.FilterNode(doc)
	if err != nil {
		return nil
	}
	return node
}

// nodeAtPath returns the node a path points to under node, or nil when
// the path points to a non-existing field.
// A path segment can index a sequence using brackets notation.
func nodeAtPath(node ast.Node, path string) ast.Node {
	for segment := range strings.SplitSeq(path, ".") {
		name, index, indexed := cutIndex(segment)
		if name != "" {
			node = mappingValue(node, name)
		}
		if indexed {
			node = sequenceItem(node, index)
		}
		if node == nil {
			return nil
		}
	}
	return node
}

// cutIndex splits a path segment into its name and sequence index,
// and returns whether it found one.
func cutIndex(segment string) (string, int, bool) {
	name, rest, found := strings.Cut(segment, "[")
	if !found {
		return segment, 0, false
	}
	digits, ok := strings.CutSuffix(rest, "]")
	if !ok {
		return segment, 0, false
	}
	index, err := strconv.Atoi(digits)
	if err != nil {
		return segment, 0, false
	}
	return name, index, true
}

// mappingKey returns the node for a key in a mapping.
func mappingKey(node ast.Node, key string) ast.Node {
	mapping, ok := node.(*ast.MappingNode)
	if !ok {
		return nil
	}
	for _, value := range mapping.Values {
		if value.Key.GetToken().Value == key {
			return value.Key
		}
	}
	return nil
}

// mappingValue follows keys through nested mappings and returns the value
// of the node it reached. It returns nil when a key is missing or one of the
// traversed node is not a mapping.
func mappingValue(node ast.Node, keys ...string) ast.Node {
	for _, key := range keys {
		mapping, ok := node.(*ast.MappingNode)
		if !ok {
			return nil
		}
		node = nil

		for _, entry := range mapping.Values {
			if entry.Key.GetToken().Value == key {
				node = entry.Value
				break
			}
		}
	}
	return node
}

func sequenceItem(node ast.Node, i int) ast.Node {
	sequence, ok := node.(*ast.SequenceNode)
	if !ok || i < 0 || i >= len(sequence.Values) {
		return nil
	}
	return sequence.Values[i]
}

func tokenPosition(node ast.Node) (int, int) {
	if node == nil || node.GetToken() == nil {
		return 0, 0
	}
	position := node.GetToken().Position
	return position.Line, position.Column
}

// tokenWidth returns how many columns a mapping key takes on its line,
// and zero for any other token.
func tokenWidth(tk *token.Token) int {
	if tk == nil || tk.Next == nil || tk.Next.Type != token.MappingValueType {
		return 0
	}
	return len([]rune(strings.TrimSpace(tk.Origin)))
}
