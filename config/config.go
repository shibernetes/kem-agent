package config

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/ast"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/pipeline"
	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/source"
	"github.com/shibernetes/kem-agent/source/enricher"
)

// Config is the root agent configuration.
type Config struct {
	Service    Service                    `yaml:"service,omitempty"`
	Source     source.Config              `yaml:"source"`
	Sinks      map[string]*Component      `yaml:"sinks"`
	Pipelines  map[string]pipeline.Config `yaml:"pipelines"`
	Checkpoint Checkpoint                 `yaml:"checkpoint"`

	doc           ast.Node
	pipelineNodes map[string]ast.Node
	warnings      []Warning
}

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{
		Service:    DefaultServiceConfig(),
		Source:     source.DefaultConfig(),
		Checkpoint: DefaultCheckpointConfig(),
	}
}

// Validate validates every block and every declared component.
// The components must be decoded first, since they hold only their
// type and raw config prior to that.
func (c *Config) Validate() error {
	if err := c.Service.Validate(); err != nil {
		return c.blockError("service", err)
	}
	if err := c.Source.Validate(); err != nil {
		return c.blockError("source", err)
	}
	if err := c.Checkpoint.Validate(); err != nil {
		return c.blockError("checkpoint", err)
	}
	storeKey := mappingKey(mappingValue(c.doc, "checkpoint"), "store")
	if err := c.validateComponent(&c.Checkpoint.Store, storeKey, "checkpoint: store"); err != nil {
		return err
	}
	if err := c.validateSinks(); err != nil {
		return err
	}
	if err := c.validatePipelines(); err != nil {
		return err
	}
	if err := c.validateNames(); err != nil {
		return err
	}
	return c.validateCrossReferences()
}

func (c *Config) blockError(block string, err error) error {
	node := fieldNode(mappingValue(c.doc, block), mappingKey(c.doc, block), err)
	return diag.Atf(node, "%s: %w", block, err)
}

func (c *Config) validateSinks() error {
	if len(c.Sinks) == 0 {
		return diag.At(mappingKey(c.doc, "sinks"), errors.New("at least one sink must be declared"))
	}
	for _, name := range slices.Sorted(maps.Keys(c.Sinks)) {
		if err := c.validateComponent(c.Sinks[name], c.getSinkNode(name), fmt.Sprintf("sinks[%s]", name)); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) validatePipelines() error {
	if len(c.Pipelines) == 0 {
		return diag.At(mappingKey(c.doc, "pipelines"), errors.New("at least one pipeline must be declared"))
	}
	for _, name := range slices.Sorted(maps.Keys(c.Pipelines)) {
		if err := c.Pipelines[name].Validate(); err != nil {
			node := fieldNode(c.pipelineNodes[name], c.pipelineNode(name), err)
			return diag.Atf(node, "pipelines[%s]: %w", name, err)
		}
	}
	return nil
}

// validateComponent validates a component's own configuration.
// Any validation error reports the origin of the issue with the
// position of the line in the configuration file.
func (c *Config) validateComponent(component *Component, key ast.Node, path string) error {
	if component == nil {
		// A key with no value decodes to a nil component
		// because the unmarshaler never runs for a null node.
		return fmt.Errorf("%s: %w", path, errUndeclared)
	}
	if err := component.Config.(Validator).Validate(); err != nil {
		return diag.Atf(fieldNode(component.Node(), key, err), "%s: %w", path, err)
	}
	return nil
}

// resolve decodes every component into its own config type, and
// records the position of each pipeline item in the file.
func (c *Config) resolve(doc ast.Node, factories sink.Factories) error {
	c.doc = doc
	c.pipelineNodes = collectPipelineNodes(doc)

	if err := c.Checkpoint.resolveStore(mappingKey(doc, "checkpoint")); err != nil {
		return err
	}
	return c.resolveSinks(factories)
}

// resolveSinks decodes each sink into its own config type.
func (c *Config) resolveSinks(factories sink.Factories) error {
	for _, name := range slices.Sorted(maps.Keys(c.Sinks)) {
		component := c.Sinks[name]
		if component == nil {
			// A key with no value decodes to a nil component, which
			// leaves the name the only thing left to report against.
			return diag.Atf(c.getSinkNode(name), "sinks[%s]: %w", name, errUndeclared)
		}
		if component.Type == "" {
			return diag.Atf(c.getSinkNode(name), "sinks[%s]: type is required", name)
		}
		factory, ok := factories[component.Type]
		if !ok {
			return diag.Atf(component.Node(), "sinks[%s]: unknown type %q, allowed values are %s",
				name, component.Type, strings.Join(slices.Sorted(maps.Keys(factories)), ", "))
		}
		cfg := factory.CreateDefaultConfig()
		if _, ok := cfg.(Validator); !ok {
			return fmt.Errorf("sinks[%s]: type %q declares no Validate method", name, component.Type)
		}
		if err := component.Decode(cfg); err != nil {
			return err
		}
	}
	return nil
}

// suggestResources reports a declared enrichment resource that resembles
// to one of the default Kubernetes API resources. It is only reported as
// a warning since it can be a false positive, because a custom resource
// existence cannot be asserted whether or not it is spelled correctly.
func (c *Config) suggestResources(doc ast.Node) {
	for i, name := range c.Source.Enrichment.Resources {
		suggestion, ok := enricher.SuggestResource(name)
		if !ok {
			continue
		}
		c.warnings = append(c.warnings, Warning{
			Node:    nodeAt(doc, fmt.Sprintf("$.source.enrichment.resources[%d]", i)),
			Message: fmt.Sprintf("source: enrichment: unknown resource %q, did you mean %q?", name, suggestion),
		})
	}
}

// collectPipelineNodes returns the block each pipeline was written in,
// keyed by name, so a validation failure can report its line.
func collectPipelineNodes(doc ast.Node) map[string]ast.Node {
	mapping, ok := doc.(*ast.MappingNode)
	if !ok {
		return nil
	}
	for _, v := range mapping.Values {
		if v.Key.GetToken().Value != "pipelines" {
			continue
		}
		pipelines, ok := v.Value.(*ast.MappingNode)
		if !ok {
			return nil
		}
		nodes := make(map[string]ast.Node, len(pipelines.Values))
		for _, p := range pipelines.Values {
			nodes[p.Key.GetToken().Value] = p.Value
		}
		return nodes
	}
	return nil
}
