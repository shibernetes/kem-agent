package source

import (
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/config/units"
	"github.com/shibernetes/kem-agent/source/enricher"
	"github.com/shibernetes/kem-agent/source/sanitizer"
)

const (
	// allNamespaces is a placeholder value for empty string used to
	// represent a cluster-wide watch over all namespaces.
	allNamespaces = "*"
)

// selectableFields lists the fields the APIServer accepts in a field
// selector over events.k8s.io/v1.
var selectableFields = []string{
	"metadata.name",
	"metadata.namespace",
	"reason",
	"reportingController",
	"type",
	"regarding.apiVersion",
	"regarding.fieldPath",
	"regarding.kind",
	"regarding.name",
	"regarding.namespace",
	"regarding.resourceVersion",
	"regarding.uid",
}

// Config defines the configuration of the Kubernetes events source.
type Config struct {
	// List of namespaces to read events from. A single watch with an
	// empty name reads all events of the cluster
	Watches []WatchConfig `yaml:"watches,omitempty" jsonschema:"minItems=1"`

	// Maximum age of the events a watch's first replay delivers, from
	// their last occurrence. A zero value keeps all events
	BootstrapEventMaxAge units.Duration `yaml:"bootstrap_event_max_age,omitempty"`

	// Configuration of the sanitizers applied to all events
	Sanitizers sanitizer.Config `yaml:"sanitizers,omitempty"`

	// Configuration of the enrichment feature
	Enrichment enricher.Config `yaml:"enrichment,omitempty"`
}

// DefaultConfig returns the default source configuration, which declares
// no watch and replays every event the APIServer still retains.
func DefaultConfig() Config {
	return Config{
		Sanitizers: sanitizer.DefaultConfig(),
		Enrichment: enricher.DefaultConfig(),
	}
}

// Validate validates the configuration.
func (c Config) Validate() error {
	if err := validateWatches(c.Watches); err != nil {
		return err
	}
	if c.BootstrapEventMaxAge < 0 {
		return diag.Pathf("bootstrap_event_max_age", "bootstrap_event_max_age must not be negative")
	}
	if err := c.Sanitizers.Validate(); err != nil {
		return diag.Prefix(err, "sanitizers")
	}
	return diag.Prefix(c.Enrichment.Validate(), "enrichment")
}

// WatchConfig configures a watch over the events of a single namespace
// or over the events of every namespace when no namespace is specified.
//
// Both selectors can be used to narrow the events sent by the APIServer.
type WatchConfig struct {
	// Name of the namespace to watch
	Namespace string `yaml:"namespace,omitempty"`

	// Label selector applied by the APIServer to the served events
	LabelSelector string `yaml:"label_selector,omitempty"`

	// Field selector applied by the APIServer to the served events. Only
	// the selectable fields can be used
	FieldSelector string `yaml:"field_selector,omitempty"`
}

// Validate validates the configuration.
func (c WatchConfig) Validate() error {
	if c.Namespace != "" {
		if errs := validation.IsDNS1123Label(c.Namespace); len(errs) > 0 {
			return diag.Pathf("namespace", "invalid namespace %q: %s", c.Namespace, strings.Join(errs, ", "))
		}
	}
	if _, err := labels.Parse(c.LabelSelector); err != nil {
		return diag.Pathf("label_selector", "invalid label_selector %q: %w", c.LabelSelector, err)
	}
	return c.validateFieldSelector()
}

// validateFieldSelector checks that the field selector parses, and that
// the fields it names are ones the APIServer accepts.
func (c WatchConfig) validateFieldSelector() error {
	selector, err := fields.ParseSelector(c.FieldSelector)
	if err != nil {
		return diag.Pathf("field_selector", "invalid field_selector %q: %w", c.FieldSelector, err)
	}
	for _, req := range selector.Requirements() {
		if !slices.Contains(selectableFields, req.Field) {
			return diag.Pathf("field_selector",
				"field_selector %q selects on %q, which is not one of the selectable event fields: %s",
				c.FieldSelector, req.Field, strings.Join(selectableFields, ", "),
			)
		}
	}
	return nil
}

func watchName(namespace string) string {
	if namespace == "" {
		return allNamespaces
	}
	return namespace
}

func validateWatches(watches []WatchConfig) error {
	if len(watches) == 0 {
		return diag.Pathf("watches", "at least one watch must be declared")
	}
	seen := make(map[string]struct{}, len(watches))

	for i, w := range watches {
		if err := w.Validate(); err != nil {
			return diag.Prefixf(err, "watches[%d]", i)
		}
		// A watch over every namespace already carries the events
		// of the namespaced ones, which would then be read twice.
		if w.Namespace == "" && len(watches) > 1 {
			return diag.Pathf(fmt.Sprintf("watches[%d]", i),
				"a watch over every namespace must be declared alone")
		}
		if _, ok := seen[w.Namespace]; ok {
			return diag.Pathf(fmt.Sprintf("watches[%d]", i),
				"namespace %q is declared twice", w.Namespace)
		}
		seen[w.Namespace] = struct{}{}
	}
	return nil
}
