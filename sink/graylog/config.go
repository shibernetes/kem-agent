package graylog

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/config/units"
	"github.com/shibernetes/kem-agent/internal/tlsconfig"
	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/sink/internal/netconn"
)

// TypeName is the value of the type key that selects this implementation.
const (
	TypeName = "graylog"
)

const (
	defaultPort        = 12201
	defaultSendTimeout = units.Duration(5 * time.Second)

	// maxSourceHost bounds the GELF host field, which every
	// message of every batch repeats.
	maxSourceHost = 255

	// reservedField is the additional Graylog field name.
	reservedField = "id"
)

var _ sink.Drainable = Config{}

// Config defines the configuration of the Graylog sink.
type Config struct {
	Type             string            `yaml:"type"`
	Host             string            `yaml:"host"`
	Port             int               `yaml:"port,omitempty"`
	SourceHost       string            `yaml:"source_host,omitempty"`
	AdditionalFields map[string]string `yaml:"additional_fields,omitempty"`
	SendTimeout      units.Duration    `yaml:"send_timeout,omitempty"`
	TLS              tlsconfig.Config  `yaml:"tls,omitempty"`
	Batch            sink.BatchConfig  `yaml:"batch,omitempty"`
	Queue            sink.QueueConfig  `yaml:"queue,omitempty"`
	Retry            sink.RetryConfig  `yaml:"retry,omitempty"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{
		Type:        TypeName,
		Port:        defaultPort,
		SendTimeout: defaultSendTimeout,
		Batch:       sink.DefaultBatchConfig(),
		Queue:       sink.DefaultQueueConfig(),
		Retry:       sink.DefaultRetryConfig(),
	}
}

// Validate validates the configuration.
func (c Config) Validate() error {
	if err := diag.Required(c); err != nil {
		return err
	}
	if err := netconn.ValidateAddress(c.Host, c.Port); err != nil {
		return err
	}
	if len(c.SourceHost) > maxSourceHost {
		return diag.Pathf("source_host", "source_host must be at most %d bytes", maxSourceHost)
	}
	// The keys are sorted so that a configuration with several invalid
	// field names always reports the same error.
	for _, name := range slices.Sorted(maps.Keys(c.AdditionalFields)) {
		if err := validateFieldName(name); err != nil {
			return diag.Pathf("additional_fields."+name, "additional_fields: %w", err)
		}
	}
	if c.SendTimeout <= 0 {
		return diag.Pathf("send_timeout", "send_timeout must be positive")
	}
	if err := c.TLS.Validate(); err != nil {
		return diag.Prefix(err, "tls")
	}
	return sink.ValidateSharedConfigs(c.Batch, c.Queue, c.Retry)
}

// DrainerConfig implements the [sink.Drainable] interface.
func (c Config) DrainerConfig() sink.DrainerConfig {
	return sink.DrainerConfig{
		Batch:       c.Batch,
		Queue:       c.Queue,
		Retry:       c.Retry,
		SendTimeout: c.SendTimeout,
	}
}

// validateFieldName checks that name is usable as a GELF additional
// field. The format allows letters, digits, underscores, dots and dashes,
// after the leading underscore.
// https://go2docs.graylog.org/current/getting_in_log_data/gelf_format.html
func validateFieldName(name string) error {
	if name == "" {
		return errors.New("field name is required")
	}
	// A name is checked in its normalized form, since a key is
	// carried over rather than refused when it holds a character
	// a GELF field name does not accept.
	normalized := fieldName(name)

	if strings.HasPrefix(name, "_") {
		return fmt.Errorf("field name %q must not start with an underscore", name)
	}
	if strings.EqualFold(normalized, reservedField) {
		return fmt.Errorf("field name %q is reserved by the GELF spec", name)
	}
	if strings.HasPrefix(normalized, reservedFieldPrefix) {
		return fmt.Errorf("field name %q is invalid (prefix %q is reserved)", name, reservedFieldPrefix)
	}
	if strings.ContainsFunc(normalized, invalidFieldRune) {
		return fmt.Errorf("invalid field name %q, allowed characters are letters, digits, underscore, dot and dash", name)
	}
	return nil
}

// invalidFieldRune reports whether r is outside the GELF
// additional-field charset.
func invalidFieldRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
		return false
	}
	return true
}

// fieldName returns name normalized as a GELF field name.
func fieldName(name string) string {
	return strings.ReplaceAll(name, "/", "_")
}
