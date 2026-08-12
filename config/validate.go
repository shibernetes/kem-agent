package config

import (
	"fmt"
	"maps"
	"regexp"
	"slices"

	"github.com/goccy/go-yaml/ast"

	"github.com/shibernetes/kem-agent/config/diag"
)

// entryNameRe validates the names of a sink or a pipeline.
var entryNameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// Validator is implemented by every component configuration.
type Validator interface {
	Validate() error
}

// metricsNamer is implemented by a sink configuration that declares
// Prometheus metrics registered to the agent registry.
type metricsNamer interface {
	// MetricNames returns the escaped, fully qualified metric names.
	MetricNames() []string
}

// validateCrossReferences checks the rules spanning several blocks, which
// no component can see on its own.
func (c *Config) validateCrossReferences() error {
	if err := c.validateBindings(); err != nil {
		return err
	}
	return c.validateMetricNamesUniqueness()
}

// validateNames checks the names sinks and pipelines are declared under.
func (c *Config) validateNames() error {
	const msg = "name must start and end with a letter or a digit, and contain " +
		"only letters, digits, dots, dashes and underscores"

	for _, name := range slices.Sorted(maps.Keys(c.Sinks)) {
		if !entryNameRe.MatchString(name) {
			return diag.Atf(c.getSinkNode(name), "sinks[%s]: %s", name, msg)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.Pipelines)) {
		if !entryNameRe.MatchString(name) {
			return diag.Atf(c.pipelineNode(name), "pipelines[%s]: %s", name, msg)
		}
	}
	return nil
}

// validateBindings checks that a pipeline references only declared sinks
// and watches, and that every declared one is referenced by some pipeline.
func (c *Config) validateBindings() error {
	var (
		watches = make(map[string]bool, len(c.Source.Watches))
		sinks   = make(map[string]bool, len(c.Sinks))
	)
	for _, w := range c.Source.Watches {
		watches[w.Namespace] = false
	}
	for name := range c.Sinks {
		sinks[name] = false
	}
	_, hasAllNsWatch := watches[""]

	for _, name := range slices.Sorted(maps.Keys(c.Pipelines)) {
		p := c.Pipelines[name]

		if hasAllNsWatch && len(p.Watches) > 0 {
			return diag.Atf(c.pipelineEntryNode(name, "watches"),
				"pipelines[%s]: watches cannot be narrowed, the source already covers every namespace", name)
		}
		for i, namespace := range p.Watches {
			if _, ok := watches[namespace]; !ok {
				return diag.Atf(c.pipelineEntryNode(name, fmt.Sprintf("watches[%d]", i)),
					"pipelines[%s]: watches: namespace %q is not watched by the source", name, namespace)
			}
			watches[namespace] = true
		}
		// A pipeline with no defined watch reads all of them.
		if len(p.Watches) == 0 {
			for namespace := range watches {
				watches[namespace] = true
			}
		}
		for i, sink := range p.Sinks {
			if _, ok := sinks[sink]; !ok {
				return diag.Atf(c.pipelineEntryNode(name, fmt.Sprintf("sinks[%d]", i)),
					"pipelines[%s]: sinks: %q is not declared", name, sink)
			}
			sinks[sink] = true
		}
	}
	for _, namespace := range slices.Sorted(maps.Keys(watches)) {
		if !watches[namespace] {
			return diag.Atf(c.getWatchNode(namespace),
				"source: watches: namespace %q is referenced by no pipeline", namespace)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(sinks)) {
		if !sinks[name] {
			return diag.Atf(c.getSinkNode(name), "sinks[%s]: is referenced by no pipeline", name)
		}
	}
	return nil
}

// validateMetricNamesUniqueness checks that a metric name is used by
// a single metrics sink at a time.
func (c *Config) validateMetricNamesUniqueness() error {
	seen := make(map[string]string)

	for _, name := range slices.Sorted(maps.Keys(c.Sinks)) {
		component := c.Sinks[name]
		if component == nil {
			continue
		}
		namer, ok := component.Config.(metricsNamer)
		if !ok {
			continue
		}
		for _, metric := range namer.MetricNames() {
			if other, ok := seen[metric]; ok {
				return diag.Atf(c.getSinkNode(name),
					"sinks[%s]: metric %q is already exposed by sink %s", name, metric, other)
			}
			seen[metric] = name
		}
	}
	return nil
}

// pipelineEntryNode returns the node at path inside a pipeline's block, or
// the pipeline's own name when the document does not hold that entry.
func (c *Config) pipelineEntryNode(name, path string) ast.Node {
	if node := nodeAtPath(c.pipelineNodes[name], path); node != nil {
		return node
	}
	return c.pipelineNode(name)
}

func (c *Config) getSinkNode(name string) ast.Node {
	return mappingKey(mappingValue(c.doc, "sinks"), name)
}

func (c *Config) pipelineNode(name string) ast.Node {
	return mappingKey(mappingValue(c.doc, "pipelines"), name)
}

func (c *Config) getWatchNode(namespace string) ast.Node {
	watches := mappingValue(c.doc, "source", "watches")

	for i, w := range c.Source.Watches {
		if w.Namespace == namespace {
			return sequenceItem(watches, i)
		}
	}
	return nil
}
