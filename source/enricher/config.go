package enricher

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/validate/content"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/config/units"
)

const (
	defaultSyncTimeout = time.Minute
)

// Config defines the configuration for the enrichment of the object
// an event regards.
type Config struct {
	// List of resources whose objects are cached to enrich events,
	// as resource.group, such as pods or deployments.apps
	Resources []string `yaml:"resources,omitempty"`

	// Maximum time for the initial sync of the caches. The agent stops
	// once it elapses
	SyncTimeout units.Duration `yaml:"sync_timeout,omitempty"`

	// Configuration of the labels copied from the enriched object
	Labels Allowlist `yaml:"labels,omitempty"`

	// Configuration of the annotations copied from the enriched object
	Annotations Allowlist `yaml:"annotations,omitempty"`
}

// Allowlist lists the metadata keys copied onto an enriched object.
// An empty list keeps every key. To reject all, set Enabled to false.
type Allowlist struct {
	// Enable the allowlist
	Enabled bool `yaml:"enabled,omitempty"`

	// List of keys to keep. An empty list preserves all keys
	Keys []string `yaml:"keys,omitempty"`
}

// DefaultConfig returns the default enrichment configuration.
func DefaultConfig() Config {
	return Config{
		SyncTimeout: units.Duration(defaultSyncTimeout),
		Labels:      Allowlist{Enabled: true},
	}
}

// Validate validates the configuration.
func (c Config) Validate() error {
	if c.SyncTimeout <= 0 {
		return diag.Pathf("sync_timeout", "sync_timeout must be positive")
	}
	seen := make(map[schema.GroupResource]struct{}, len(c.Resources))

	for i, name := range c.Resources {
		if err := validateResourceName(name); err != nil {
			return diag.Path(fmt.Sprintf("resources[%d]", i), err)
		}
		// Two spellings can name one resource, so the parsed
		// form is used to recognize a duplicate, rather than
		// the original written form.
		gr := schema.ParseGroupResource(name)
		if _, ok := seen[gr]; ok {
			return diag.Pathf(fmt.Sprintf("resources[%d]", i),
				"resource %q is declared twice", name)
		}
		seen[gr] = struct{}{}
	}
	if err := c.Labels.Validate(); err != nil {
		return diag.Prefix(err, "labels")
	}
	if err := c.Annotations.Validate(); err != nil {
		return diag.Prefix(err, "annotations")
	}
	return nil
}

// Validate validates an allowlist.
func (a Allowlist) Validate() error {
	if !a.Enabled && len(a.Keys) > 0 {
		return diag.Pathf("keys", "keys have no effect unless enabled is true")
	}
	for i, key := range a.Keys {
		if errors := content.IsLabelKey(key); len(errors) > 0 {
			return diag.Pathf(fmt.Sprintf("keys[%d]", i), "invalid key %q: %s", key, strings.Join(errors, ", "))
		}
	}
	return nil
}

// apply returns the entries of m the allowlist admits.
// It returns the original map when the list is empty, and nil when
// it is disabled or nothing matches.
func (a Allowlist) apply(m map[string]string) map[string]string {
	if !a.Enabled || len(m) == 0 {
		return nil
	}
	if len(a.Keys) == 0 {
		return m
	}
	var allowed map[string]string

	for _, key := range a.Keys {
		if v, ok := m[key]; ok {
			if allowed == nil {
				allowed = make(map[string]string, len(a.Keys))
			}
			allowed[key] = v
		}
	}
	return allowed
}

// validateResourceName checks that a declaration names a resource
// rather than a kind.
func validateResourceName(name string) error {
	gr := schema.ParseGroupResource(name)

	if len(validation.IsDNS1035Label(gr.Resource)) > 0 {
		return fmt.Errorf("resource %q must be a lowercase plural name", name)
	}
	if gr.Group != "" && len(validation.IsDNS1123Subdomain(gr.Group)) > 0 {
		return fmt.Errorf("resource %q must specify its API group as a DNS subdomain", name)
	}
	return nil
}
