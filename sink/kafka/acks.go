package kafka

import (
	"fmt"
	"slices"

	"github.com/goccy/go-yaml/ast"
	"github.com/invopop/jsonschema"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/internal/enum"
)

// A RequiredAcks selects which replicas must store a record before it counts
// as delivered. It can be every in-sync replica, the partition leader alone,
// or no replica at all.
//
// The setting can be written in two ways. Beside the names, it accepts the
// numbers used by Kafka's own acks setting, 1 for the leader and 0 for none.
// Both are decoded to the same value. Kafka's -1 for all is not accepted.
type RequiredAcks string

const (
	RequiredAcksAll    RequiredAcks = "all"
	RequiredAcksLeader RequiredAcks = "leader"
	RequiredAcksNone   RequiredAcks = "none"
)

var requiredAcks = []RequiredAcks{
	RequiredAcksAll,
	RequiredAcksLeader,
	RequiredAcksNone,
}

// requiredAcksNumbers maps the numbers Kafka uses for acks to the values they
// stand for.
var requiredAcksNumbers = map[string]RequiredAcks{
	"1": RequiredAcksLeader,
	"0": RequiredAcksNone,
}

// String implements the [fmt.Stringer] interface.
func (a RequiredAcks) String() string {
	return string(a)
}

// Validate validates the required acks.
func (a RequiredAcks) Validate() error {
	if slices.Contains(requiredAcks, a) {
		return nil
	}
	return fmt.Errorf("unknown required acks %q, allowed values are %s, 1 and 0", a, enum.Join(requiredAcks))
}

// UnmarshalYAML implements the [github.com/goccy/go-yaml.NodeUnmarshaler] interface.
// It decodes a number to the name it stands for, and leaves any other value
// to [RequiredAcks.Validate].
func (a *RequiredAcks) UnmarshalYAML(node ast.Node) error {
	tk := node.GetToken()
	if _, ok := node.(ast.ScalarNode); !ok || tk == nil {
		return diag.Atf(node, "expected a string or a number")
	}
	// The token contains the written value, which is the same for 1 and "1",
	// so a number set through an environment variable is read the same way.
	if v, ok := requiredAcksNumbers[tk.Value]; ok {
		*a = v
		return nil
	}
	*a = RequiredAcks(tk.Value)

	return nil
}

// JSONSchema satisfies the [github.com/invopop/jsonschema] reflector.
// It describes both ways of writing the required acks, in the JSON Schema spec.
func (RequiredAcks) JSONSchema() *jsonschema.Schema {
	names := enum.Schema(requiredAcks)

	// The numbers are also accepted as strings, which is the form
	// they take when they are set through an environment variable.
	names.Enum = append(names.Enum, "1", "0")

	return &jsonschema.Schema{
		OneOf: []*jsonschema.Schema{
			names,
			{Type: "integer", Enum: []any{1, 0}},
		},
	}
}
