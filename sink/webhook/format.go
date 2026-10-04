package webhook

import (
	"fmt"
	"slices"

	"github.com/invopop/jsonschema"

	"github.com/shibernetes/kem-agent/internal/enum"
)

// Format selects how the events of a batch compose into a request body.
// It defines the structure of the body and the content type it is sent
// under, and whether an event is rendered by a template rather than
// encoded as canonical JSON.
type Format string

const (
	FormatJSONList    Format = "json-list"
	FormatNDJSON      Format = "ndjson"
	FormatCloudEvents Format = "cloudevents"
	FormatTemplate    Format = "template"
)

var formats = []Format{
	FormatJSONList,
	FormatNDJSON,
	FormatCloudEvents,
	FormatTemplate,
}

// String implements the [fmt.Stringer] interface.
func (f Format) String() string {
	return string(f)
}

// Validate validates the format.
func (f Format) Validate() error {
	if slices.Contains(formats, f) {
		return nil
	}
	return fmt.Errorf("unknown format %q, allowed values are %s", f, enum.Join(formats))
}

// JSONSchema satisfies the [github.com/invopop/jsonschema] reflector.
// It describes the supported formats, in the JSON Schema spec.
func (Format) JSONSchema() *jsonschema.Schema {
	return enum.Schema(formats)
}
