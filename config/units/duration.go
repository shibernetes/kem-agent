package units

import (
	"fmt"
	"strings"
	"time"

	"github.com/goccy/go-yaml/ast"
	"github.com/invopop/jsonschema"

	"github.com/shibernetes/kem-agent/config/diag"
)

// durationPattern is the regex validating a time duration.
// It follows the grammar supported by [time.ParseDuration].
const durationPattern = `^[-+]?(0|(([0-9]+(\.[0-9]*)?|\.[0-9]+)(ns|us|µs|μs|ms|s|m|h))+)$`

// Duration represents a length of time, written in Go's duration form.
type Duration time.Duration

// String implements the [fmt.Stringer] interface.
func (d Duration) String() string {
	return time.Duration(d).String()
}

// UnmarshalYAML implements the [github.com/goccy/go-yaml.NodeUnmarshaler] interface.
// It reports a malformed duration at the line it was written on.
func (d *Duration) UnmarshalYAML(node ast.Node) error {
	s, ok := asScalar(node)
	if !ok || s == "" {
		return diag.Atf(node, "expected a Go time.Duration string")
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		cause := strings.TrimPrefix(err.Error(), "time: ")
		// Return the error without the duplicate error prefix.
		if cause == fmt.Sprintf("invalid duration %q", s) {
			return diag.Atf(node, "%s", cause)
		}
		return diag.Atf(node, "invalid duration %q: %s", s, cause)
	}
	*d = Duration(v)

	return nil
}

// JSONSchema satisfies the [github.com/invopop/jsonschema] reflector.
// It describes the written form of a duration, in the JSON Schema spec.
func (Duration) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "string",
		Pattern:     durationPattern,
		Description: "A time duration, written in the format accepted by Go's time.ParseDuration.",
		Examples:    []any{"250ms", "30s", "1m30s"},
	}
}
