package config

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/shibernetes/kem-agent/config/diag"
)

// Logging configures the logger output format and level.
type Logging struct {
	Format LogFormat `yaml:"format,omitempty"`
	Level  LogLevel  `yaml:"level,omitempty"`
}

// Validate validates the configuration.
func (c Logging) Validate() error {
	if err := c.Format.Validate(); err != nil {
		return diag.Path("format", err)
	}
	if err := c.Level.Validate(); err != nil {
		return diag.Path("level", err)
	}
	return nil
}

// LogFormat selects the logging format.
// The client-go package logs through klog, whose output is redirected
// to slog, so the setting covers those records as well.
type LogFormat string

const (
	LogFormatJSON LogFormat = "json"
	LogFormatText LogFormat = "text"
)

var logFormats = []LogFormat{
	LogFormatJSON,
	LogFormatText,
}

// String implements the [fmt.Stringer] interface.
func (f LogFormat) String() string {
	return string(f)
}

// Validate validates the log format.
func (f LogFormat) Validate() error {
	if slices.Contains(logFormats, f) {
		return nil
	}
	return fmt.Errorf("unknown format %q, allowed values are %s", f, strings.Join(logFormatNames(), ", "))
}

// JSONSchema satisfies the [github.com/invopop/jsonschema] reflector.
// It describes the supported formats, in the JSON Schema spec.
func (LogFormat) JSONSchema() *jsonschema.Schema {
	enum := make([]any, len(logFormats))
	for i, name := range logFormatNames() {
		enum[i] = name
	}
	return &jsonschema.Schema{
		Type:        "string",
		Enum:        enum,
		Description: "The log output format.",
	}
}

// LogLevel selects the logging level.
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

var logLevels = []LogLevel{
	LogLevelDebug,
	LogLevelInfo,
	LogLevelWarn,
	LogLevelError,
}

// String implements the [fmt.Stringer] interface.
func (l LogLevel) String() string {
	return string(l)
}

// Validate validates the log level.
func (l LogLevel) Validate() error {
	if slices.Contains(logLevels, l) {
		return nil
	}
	return fmt.Errorf("unknown level %q, allowed values are %s", l, strings.Join(logLevelNames(), ", "))
}

// Level implements the [slog.Leveler] interface.
func (l LogLevel) Level() slog.Level {
	switch l {
	case LogLevelDebug:
		return slog.LevelDebug
	case LogLevelWarn:
		return slog.LevelWarn
	case LogLevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// JSONSchema satisfies the [github.com/invopop/jsonschema] reflector.
// It describes the supported levels, in the JSON Schema spec.
func (LogLevel) JSONSchema() *jsonschema.Schema {
	enum := make([]any, len(logLevels))
	for i, name := range logLevelNames() {
		enum[i] = name
	}
	return &jsonschema.Schema{
		Type:        "string",
		Enum:        enum,
		Description: "The log level.",
	}
}

func logFormatNames() []string {
	names := make([]string, len(logFormats))
	for i, f := range logFormats {
		names[i] = f.String()
	}
	return names
}

func logLevelNames() []string {
	names := make([]string, len(logLevels))
	for i, l := range logLevels {
		names[i] = l.String()
	}
	return names
}
