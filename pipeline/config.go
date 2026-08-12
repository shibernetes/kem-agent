package pipeline

import (
	"fmt"
	"strings"

	"github.com/shibernetes/kem-agent/config/diag"
)

// Config defines the configuration of a pipeline.
type Config struct {
	Watches []string `yaml:"watches,omitempty"`
	Filters []string `yaml:"filters,omitempty"`
	Sinks   []string `yaml:"sinks,omitempty"`
}

// Validate validates the configuration.
func (c Config) Validate() error {
	if len(c.Sinks) == 0 {
		return diag.Pathf("sinks", "sinks must not be empty")
	}
	// An omitted list takes every watch, where an empty one
	// narrows the pipeline down to nothing.
	if c.Watches != nil && len(c.Watches) == 0 {
		return diag.Pathf("watches", "watches must not be empty")
	}
	for i, expr := range c.Filters {
		if strings.TrimSpace(expr) == "" {
			return diag.Pathf(fmt.Sprintf("filters[%d]", i), "filters[%d] expression must not be empty", i)
		}
	}
	if err := validateUnique("watches", c.Watches); err != nil {
		return err
	}
	return validateUnique("sinks", c.Sinks)
}

func validateUnique(field string, names []string) error {
	seen := make(map[string]struct{}, len(names))

	for i, name := range names {
		if _, ok := seen[name]; ok {
			return diag.Pathf(fmt.Sprintf("%s[%d]", field, i),
				"%s: %q is listed twice", field, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}
