package config

import (
	"errors"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
)

// errUndeclared reports a component the configuration does not declare.
var errUndeclared = errors.New("component is not declared")

// Component is a configuration block that specifies the concrete type
// into which the rest of the data is decoded. Sinks and the checkpoint
// store are both declared this way.
//
// The block is decoded only once the type is known, so decode errors
// can point to the original content.
type Component struct {
	Type   string
	Config any
	node   ast.Node
}

// Node returns the original YAML node, so [diag.Atf] can position
// an error about the whole component.
func (c *Component) Node() ast.Node {
	return c.node
}

// UnmarshalYAML implements the [github.com/goccy/go-yaml.NodeUnmarshaler] interface.
// It keeps the block as written and reads the type it names.
func (c *Component) UnmarshalYAML(node ast.Node) error {
	var comp struct {
		Type string `yaml:"type"`
	}
	// A block that is not a mapping fails here, so it is reported
	// with its position rather than as a missing type later.
	//
	// An absent type is left to the caller, which knows the name
	// the component was declared under.
	if err := yaml.NodeToValue(node, &comp); err != nil {
		return err
	}
	c.Type = comp.Type
	c.node = node

	return nil
}

// Decode converts the component's YAML node to the value pointed to by v.
func (c *Component) Decode(v any) error {
	if c.node == nil {
		return errUndeclared
	}
	if err := yaml.NodeToValue(c.node, v, yaml.Strict()); err != nil {
		return err
	}
	c.Config = v

	return nil
}
