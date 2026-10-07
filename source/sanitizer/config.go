package sanitizer

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/config/units"
	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/enum"
)

// restrictedFields lists the event fields that an allowlist can
// constrain. They are the string fields that events.k8s.io/v1
// validates and the legacy core/v1 API does not.
var restrictedFields = []string{
	event.FieldType,
	event.FieldReason,
	event.FieldAction,
	event.FieldReportingController,
	event.FieldReportingInstance,
}

// Config defines the configuration for the sanitizers applied to every event.
type Config struct {
	// Configuration of the sanitizer that shortens long event fields and
	// object references
	FieldLimits FieldLimits `yaml:"field_limits,omitempty"`

	// Configuration of the sanitizer that drops the
	// last-applied-configuration annotation written by kubectl
	LastAppliedConfig LastAppliedConfig `yaml:"last_applied_config,omitempty"`

	// Configuration of the sanitizer that limits the number of labels and
	// annotations of an event
	MetadataCountLimit MetadataCountLimit `yaml:"metadata_count_limit,omitempty"`

	// Configuration of the sanitizer that shortens long annotation values
	AnnotationValueLimit AnnotationValueLimit `yaml:"annotation_value_limit,omitempty"`

	// Configuration of the sanitizer that clears an event field whose value
	// is not explicitly allowed
	FieldValues FieldValues `yaml:"field_values,omitempty"`
}

// DefaultConfig returns the default sanitizers configuration.
// Every sanitizer is enabled by default except field_values, which
// has no default allowlist.
func DefaultConfig() Config {
	return Config{
		FieldLimits:       FieldLimits{Enabled: true},
		LastAppliedConfig: LastAppliedConfig{Enabled: true},
		MetadataCountLimit: MetadataCountLimit{
			Enabled:        true,
			MaxLabels:      100,
			MaxAnnotations: 100,
		},
		AnnotationValueLimit: AnnotationValueLimit{
			Enabled:  true,
			MaxBytes: 4 << 10, // 4 KiB
		},
	}
}

// Validate validates the configuration.
func (c Config) Validate() error {
	if err := c.MetadataCountLimit.Validate(); err != nil {
		return diag.Prefix(err, "metadata_count_limit")
	}
	if err := c.AnnotationValueLimit.Validate(); err != nil {
		return diag.Prefix(err, "annotation_value_limit")
	}
	return diag.Prefix(c.FieldValues.Validate(), "field_values")
}

// FieldLimits shortens an event's string fields to the lengths that
// events.k8s.io/v1 allows. It also shortens the fields of the object
// references an event carries, which no API limits.
type FieldLimits struct {
	// Enable the sanitizer
	Enabled bool `yaml:"enabled,omitempty"`
}

// LastAppliedConfig drops the last-applied-configuration annotation
// kubectl writes, which mirrors the whole object it was applied to.
type LastAppliedConfig struct {
	// Enable the sanitizer
	Enabled bool `yaml:"enabled,omitempty"`
}

// MetadataCountLimit bounds how many labels and annotations an event
// keeps. A maximum of zero leaves its own map unbounded, so one of
// the two can be capped while the other is not.
type MetadataCountLimit struct {
	// Enable the sanitizer
	Enabled bool `yaml:"enabled,omitempty"`

	// Maximum count of labels to keep. A zero value preserves all labels
	MaxLabels int `yaml:"max_labels,omitempty"`

	// Maximum count of annotations to keep. A zero value preserves all annotations
	MaxAnnotations int `yaml:"max_annotations,omitempty"`
}

// Validate validates the configuration.
func (c MetadataCountLimit) Validate() error {
	switch {
	case c.MaxLabels < 0:
		return diag.Pathf("max_labels", "max_labels must not be negative")
	case c.MaxAnnotations < 0:
		return diag.Pathf("max_annotations", "max_annotations must not be negative")
	}
	return nil
}

// AnnotationValueLimit bounds the length of a single annotation value.
// A maximum of zero leaves every value intact. It is the only sanitizer
// that can truncate entirely legitimate data.
type AnnotationValueLimit struct {
	// Enable the sanitizer
	Enabled bool `yaml:"enabled,omitempty"`

	// Maximum size of an annotation value in bytes, beyond which the value
	// is truncated. A zero value disables the limit
	MaxBytes units.Bytes `yaml:"max_bytes,omitempty"`
}

// Validate validates the configuration.
func (c AnnotationValueLimit) Validate() error {
	if c.MaxBytes < 0 {
		return diag.Pathf("max_bytes", "max_bytes must not be negative")
	}
	return nil
}

// FieldValues clears an event field whose value isn't in the allowlist.
// By default, it constrains no field and is disabled.
type FieldValues struct {
	// Enable the sanitizer
	Enabled bool `yaml:"enabled,omitempty"`

	// Allowed values of each constrained field, keyed by field name. A field
	// with an empty list is cleared on every event
	Fields FieldAllowlists `yaml:"fields,omitempty"`
}

// Validate validates the configuration.
// An enabled sanitizer must constrain at least one field.
func (c FieldValues) Validate() error {
	if c.Enabled && len(c.Fields) == 0 {
		return diag.Pathf("fields", "fields must list at least one field when the sanitizer is enabled")
	}
	return diag.Prefix(c.Fields.Validate(), "fields")
}

// FieldAllowlists maps the name of an event field to its allowed values.
type FieldAllowlists map[string][]string

// Validate validates the allowlists.
// Values are compared exactly, so a value with leading or trailing
// whitespace is rejected.
func (f FieldAllowlists) Validate() error {
	// The names are sorted, so the same configuration always reports
	// the same error first.
	for _, name := range slices.Sorted(maps.Keys(f)) {
		if !slices.Contains(restrictedFields, name) {
			return diag.Pathf(name, "unknown field %q, allowed fields are %s", name, enum.Join(restrictedFields))
		}
		for i, v := range f[name] {
			path := fmt.Sprintf("%s[%d]", name, i)

			switch {
			case strings.TrimSpace(v) == "":
				return diag.Pathf(path, "%s: values must not be blank", name)
			case v != strings.TrimSpace(v):
				return diag.Pathf(path, "%s: value %q must not start or end with whitespace", name, v)
			case slices.Contains(f[name][:i], v):
				return diag.Pathf(path, "%s: value %q is listed more than once", name, v)
			}
		}
	}
	return nil
}

// JSONSchema satisfies the [github.com/invopop/jsonschema] reflector.
// It limits the keys to the event field names that can be constrained,
// and rejects empty or repeated values.
func (FieldAllowlists) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:          "object",
		PropertyNames: enum.Schema(restrictedFields),
		AdditionalProperties: &jsonschema.Schema{
			Type:        "array",
			UniqueItems: true,
			Items:       &jsonschema.Schema{Type: "string", MinLength: new(uint64(1))},
		},
	}
}
