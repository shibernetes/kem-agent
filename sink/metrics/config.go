package metrics

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/version"
	"github.com/shibernetes/kem-agent/sink"
)

// TypeName is the value of the type key that selects this implementation.
const (
	TypeName = "metrics"
)

const (
	defaultMetricsNamespace = "kem_events"
	defaultValidationScheme = NameValidationLegacy
)

// reservedPrefixes are the fully qualified name prefixes a configured
// metric may not use. The agent registers its own instrumentation under
// the first, and the client library its Go and process collectors under
// the other two.
var reservedPrefixes = []string{
	version.MetricNamespace + "_",
	"go_",
	"process_",
}

// Config defines the configuration of the metrics sink.
type Config struct {
	Type                       string               `yaml:"type"`
	DefaultMetricsNamespace    string               `yaml:"default_metrics_namespace,omitempty"`
	MaxSeries                  int                  `yaml:"max_series,omitempty"`
	MetricNameValidationScheme NameValidationScheme `yaml:"metric_name_validation_scheme,omitempty"`
	Metrics                    []Metric             `yaml:"metrics"`
}

// Metric defines one instrument the sink registers, incremented once per
// event it records. Counters are the only kind, since an event has no
// value a gauge or a histogram could take.
type Metric struct {
	Name        string            `yaml:"name"`
	Namespace   string            `yaml:"namespace,omitempty"`
	Subsystem   string            `yaml:"subsystem,omitempty"`
	Help        string            `yaml:"help"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	ConstLabels map[string]string `yaml:"const_labels,omitempty"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{
		Type:                       TypeName,
		DefaultMetricsNamespace:    defaultMetricsNamespace,
		MetricNameValidationScheme: defaultValidationScheme,
	}
}

// Validate validates the configuration.
func (c Config) Validate() error {
	if err := c.validate(); err != nil {
		return err
	}
	return nil
}

func (c Config) validate() error {
	if err := diag.Required(c); err != nil {
		return err
	}
	if err := c.MetricNameValidationScheme.Validate(); err != nil {
		return diag.Path("metric_name_validation_scheme", err)
	}
	// Zero is the default and means unlimited, so only a negative
	// value is refused.
	if c.MaxSeries < 0 {
		return diag.Pathf("max_series", "max_series must not be negative")
	}
	seen := make(map[string]string, len(c.Metrics))

	for i, m := range c.Metrics {
		if err := c.validateMetric(m); err != nil {
			return diag.Prefixf(err, "metrics[%d]", i)
		}
		if err := recordName(seen, c.fqName(m)); err != nil {
			return diag.Prefixf(diag.Path("name", err), "metrics[%d]", i)
		}
	}
	return nil
}

func (c Config) validateMetric(m Metric) error {
	name := c.fqName(m)

	if !c.MetricNameValidationScheme.ValidMetricName(name) {
		return diag.Pathf("name", "invalid metric name %q", name)
	}
	// Colons are legal under both schemes, but reserved for the recording
	// rules a Prometheus server defines, so an exporter uses none.
	if strings.ContainsRune(name, ':') {
		return diag.Pathf("name", "invalid metric name %q, colons are reserved for recording rules", name)
	}
	if prefix, ok := reservedPrefix(name); ok {
		return diag.Pathf("name", "metric %q uses a reserved prefix: %q", name, prefix)
	}
	labels := make(map[string]string, len(m.Labels)+len(m.ConstLabels))

	// The keys are sorted so that a configuration with several invalid
	// labels always reports the same error.
	for _, label := range slices.Sorted(maps.Keys(m.Labels)) {
		if err := c.validateLabelName(label); err != nil {
			return diag.Prefix(err, "labels")
		}
		if field := m.Labels[label]; !knownEventField(field) {
			return diag.Pathf("labels."+label, "label %q: unknown event field %q", label, field)
		}
		if err := recordLabel(labels, label); err != nil {
			return err
		}
	}
	for _, label := range slices.Sorted(maps.Keys(m.ConstLabels)) {
		if err := c.validateLabelName(label); err != nil {
			return diag.Prefix(err, "const_labels")
		}
		if _, ok := m.Labels[label]; ok {
			return diag.Pathf("const_labels."+label, "label %q is defined in both labels and const_labels", label)
		}
		if err := recordLabel(labels, label); err != nil {
			return err
		}
	}
	return nil
}

func (c Config) validateLabelName(name string) error {
	if !c.MetricNameValidationScheme.ValidLabelName(name) {
		return diag.Pathf(name, "invalid label name %q", name)
	}
	if reservedLabel(name) {
		return diag.Pathf(name, "label %q uses a reserved prefix: %q", name, model.ReservedLabelPrefix)
	}
	return nil
}

func (c Config) namespace(m Metric) string {
	if m.Namespace != "" {
		return m.Namespace
	}
	return c.DefaultMetricsNamespace
}

// fqName returns the fully qualified name a metric is registered under.
func (c Config) fqName(m Metric) string {
	return prometheus.BuildFQName(c.namespace(m), m.Subsystem, m.Name)
}

// Fields returns the event fields the declared labels read.
func (c Config) Fields() []sink.FieldRef {
	var fields []sink.FieldRef

	for i, m := range c.Metrics {
		for _, label := range slices.Sorted(maps.Keys(m.Labels)) {
			fields = append(fields, sink.FieldRef{
				Field: m.Labels[label],
				Path:  fmt.Sprintf("metrics[%d].labels.%s", i, label),
			})
		}
	}
	return fields
}

// MetricNames returns the escaped, fully qualified name of every metric
// the sink declares, in the wire format served to a scraper.
func (c Config) MetricNames() []string {
	names := make([]string, len(c.Metrics))
	for i, m := range c.Metrics {
		names[i] = escape(c.fqName(m))
	}
	return names
}

func recordName(seen map[string]string, name string) error {
	escaped := escape(name)

	switch other, ok := seen[escaped]; {
	case ok && other == name:
		return fmt.Errorf("duplicate metric name %q", name)
	case ok:
		return fmt.Errorf("metric names %q and %q are both exposed as %q", other, name, escaped)
	}
	seen[escaped] = name

	return nil
}

func recordLabel(seen map[string]string, label string) error {
	exposed := escape(label)

	if other, ok := seen[exposed]; ok {
		return fmt.Errorf("labels %q and %q are both exposed as %q", other, label, exposed)
	}
	seen[exposed] = label

	return nil
}

// reservedLabel reports whether a label name starts with the prefix
// Prometheus reserves, in its escaped or written form.
func reservedLabel(name string) bool {
	return strings.HasPrefix(name, model.ReservedLabelPrefix) ||
		strings.HasPrefix(escape(name), model.ReservedLabelPrefix)
}

// escape returns the form a name is exposed in to a scraper that does
// not accept UTF-8, which replaces every character it refuses with an
// underscore. A name the legacy character set already accepts is
// returned unchanged.
func escape(name string) string {
	return model.EscapeName(name, model.UnderscoreEscaping)
}

// reservedPrefix returns the reserved prefix a name falls under, and
// whether one matched.
//
// The escaped form is checked as well as the name itself. A scraper that
// does not accept UTF-8 is served the name with its invalid characters
// replaced by underscores, so a name outside a reserved prefix can still
// be exposed inside one.
func reservedPrefix(name string) (string, bool) {
	names := [...]string{name, escape(name)}

	for _, n := range names {
		for _, prefix := range reservedPrefixes {
			if strings.HasPrefix(n, prefix) {
				return prefix, true
			}
		}
	}
	return "", false
}

func knownEventField(field string) bool {
	_, ok := (&event.Event{}).Field(field)
	return ok
}
