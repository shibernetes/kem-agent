package units

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/invopop/jsonschema"
)

func reflectSchema(t *testing.T, v any) *jsonschema.Schema {
	t.Helper()

	r := jsonschema.Reflector{FieldNameTag: "yaml", DoNotReference: true, ExpandedStruct: true}
	return r.Reflect(v)
}

func mustEncodeSchema(t *testing.T, schema *jsonschema.Schema) []byte {
	t.Helper()

	b, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("failed to encode schema: %v", err)
	}
	return b
}

func causeOf(t *testing.T, err error) string {
	t.Helper()

	cause := errors.Unwrap(err)
	if cause == nil {
		t.Fatalf("got %v, want a positioned error wrapping a message", err)
	}
	return cause.Error()
}
