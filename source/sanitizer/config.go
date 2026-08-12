package sanitizer

import (
	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/config/units"
)

// Config defines the configuration for the sanitizers applied to every event.
type Config struct {
	FieldLimits          FieldLimits          `yaml:"field_limits,omitempty"`
	LastAppliedConfig    LastAppliedConfig    `yaml:"last_applied_config,omitempty"`
	MetadataCountLimit   MetadataCountLimit   `yaml:"metadata_count_limit,omitempty"`
	AnnotationValueLimit AnnotationValueLimit `yaml:"annotation_value_limit,omitempty"`
}

// DefaultConfig returns the default sanitizers configuration.
// Every sanitizer is on, each of them restoring a bound the
// modern API applies and the legacy one does not.
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
	return diag.Prefix(c.AnnotationValueLimit.Validate(), "annotation_value_limit")
}

// FieldLimits shortens the event string fields events.k8s.io/v1
// bounds, and the fields of the object references an event carries,
// which no API bounds at all.
type FieldLimits struct {
	Enabled bool `yaml:"enabled,omitempty"`
}

// LastAppliedConfig drops the last-applied-configuration annotation
// kubectl writes, which mirrors the whole object it was applied to.
type LastAppliedConfig struct {
	Enabled bool `yaml:"enabled,omitempty"`
}

// MetadataCountLimit bounds how many labels and annotations an event
// keeps. A maximum of zero leaves its own map unbounded, so one of
// the two can be capped while the other is not.
type MetadataCountLimit struct {
	Enabled        bool `yaml:"enabled,omitempty"`
	MaxLabels      int  `yaml:"max_labels,omitempty"`
	MaxAnnotations int  `yaml:"max_annotations,omitempty"`
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
	Enabled  bool        `yaml:"enabled,omitempty"`
	MaxBytes units.Bytes `yaml:"max_bytes,omitempty"`
}

// Validate validates the configuration.
func (c AnnotationValueLimit) Validate() error {
	if c.MaxBytes < 0 {
		return diag.Pathf("max_bytes", "max_bytes must not be negative")
	}
	return nil
}
