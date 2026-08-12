package webhook

import (
	"fmt"
	"slices"
	"strings"

	"github.com/invopop/jsonschema"
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
	return fmt.Errorf("unknown format %q, allowed values are %s", f, strings.Join(formatNames(), ", "))
}

// JSONSchema satisfies the [github.com/invopop/jsonschema] reflector.
// It describes the supported formats, in the JSON Schema spec.
func (Format) JSONSchema() *jsonschema.Schema {
	enum := make([]any, len(formats))
	for i, name := range formatNames() {
		enum[i] = name
	}
	return &jsonschema.Schema{
		Type:        "string",
		Enum:        enum,
		Description: "The format the request body is composed in.",
	}
}

func formatNames() []string {
	names := make([]string, len(formats))
	for i, f := range formats {
		names[i] = string(f)
	}
	return names
}
