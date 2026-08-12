package metrics

import (
	"fmt"
	"slices"
	"strings"

	"github.com/invopop/jsonschema"
	"github.com/prometheus/common/model"
)

// NameValidationScheme determines how a metric name and its label names are
// validated. Legacy holds them to the original Prometheus character set, and
// utf8 only requires that they are valid UTF-8 strings.
//
// It follows [model.ValidationScheme], but defaults to legacy rather than utf8,
// since the agent is an exporter rather than a scraper. A name accepted under
// utf8 is exposed as written or escaped, depending on the scraper's own
// escaping scheme.
type NameValidationScheme string

const (
	NameValidationLegacy NameValidationScheme = "legacy"
	NameValidationUTF8   NameValidationScheme = "utf8"
)

var nameValidationSchemes = []NameValidationScheme{
	NameValidationLegacy,
	NameValidationUTF8,
}

// String implements the [fmt.Stringer] interface.
func (s NameValidationScheme) String() string {
	return string(s)
}

// Validate validates the scheme.
func (s NameValidationScheme) Validate() error {
	if slices.Contains(nameValidationSchemes, s) {
		return nil
	}
	return fmt.Errorf("unknown metric name validation scheme %q, allowed values are %s",
		s, strings.Join(schemeNames(), ", "))
}

// ValidMetricName reports whether name is a metric name the scheme accepts.
func (s NameValidationScheme) ValidMetricName(name string) bool {
	return s.scheme().IsValidMetricName(name)
}

// ValidLabelName reports whether name is a label name the scheme accepts.
func (s NameValidationScheme) ValidLabelName(name string) bool {
	return s.scheme().IsValidLabelName(name)
}

// scheme returns the corresponding Prometheus scheme.
func (s NameValidationScheme) scheme() model.ValidationScheme {
	// Anything but utf8 resolves to the stricter scheme, so an unvalidated
	// value reaches neither model.UnsetValidation nor the panic it raises.
	if s == NameValidationUTF8 {
		return model.UTF8Validation
	}
	return model.LegacyValidation
}

// JSONSchema satisfies the [github.com/invopop/jsonschema] reflector.
// It describes the supported schemes, in the JSON Schema spec.
func (NameValidationScheme) JSONSchema() *jsonschema.Schema {
	enum := make([]any, len(nameValidationSchemes))
	for i, name := range schemeNames() {
		enum[i] = name
	}
	return &jsonschema.Schema{
		Type:        "string",
		Enum:        enum,
		Description: "The character set allowed in metric and label names.",
	}
}

func schemeNames() []string {
	names := make([]string, len(nameValidationSchemes))
	for i, s := range nameValidationSchemes {
		names[i] = s.String()
	}
	return names
}
