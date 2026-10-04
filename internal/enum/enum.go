package enum

import (
	"strings"

	"github.com/invopop/jsonschema"
)

// Join lists the values of an enum, separated by commas, for an error message.
func Join[T ~string](values []T) string {
	names := make([]string, len(values))
	for i, v := range values {
		names[i] = string(v)
	}
	return strings.Join(names, ", ")
}

// Schema describes the values of an enum, in the JSON Schema spec.
// It has no description, since the comment of the field holding the
// enum supplies one.
func Schema[T ~string](values []T) *jsonschema.Schema {
	enum := make([]any, len(values))
	for i, v := range values {
		enum[i] = string(v)
	}
	return &jsonschema.Schema{
		Type: "string",
		Enum: enum,
	}
}
