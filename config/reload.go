package config

import (
	"reflect"

	"github.com/shibernetes/kem-agent/pipeline"
)

// ReloadDelta reports the kind of changes between two configurations.
type ReloadDelta int

const (
	DeltaNone ReloadDelta = iota
	DeltaFilters
	DeltaStructural
)

// String implements the [fmt.Stringer] interface.
func (d ReloadDelta) String() string {
	switch d {
	case DeltaFilters:
		return "filters"
	case DeltaStructural:
		return "structural"
	default:
		return "none"
	}
}

// ClassifyDelta reports how next differs from the current configuration,
// and names the top-level blocks that changed when the differences are
// structural.
//
// The comparison ignores the YAML nodes, so a cosmetic or syntactic change
// such as a reindent or a requote is not treated as a change.
func ClassifyDelta(current, next *Config) (ReloadDelta, []string) {
	a, b := comparableConfig(current), comparableConfig(next)
	if reflect.DeepEqual(a, b) {
		return DeltaNone, nil
	}
	stripFilters(&a)
	stripFilters(&b)

	if reflect.DeepEqual(a, b) {
		return DeltaFilters, nil
	}
	return DeltaStructural, changedBlocks(a, b)
}

// changedBlocks returns the names of the top-level blocks that differ.
//
// The filters are already stripped, so a difference under pipelines means
// one was added or removed, or its sinks or watches changed.
func changedBlocks(a, b Config) []string {
	var blocks []string

	if !reflect.DeepEqual(a.Service, b.Service) {
		blocks = append(blocks, "service")
	}
	if !reflect.DeepEqual(a.Source, b.Source) {
		blocks = append(blocks, "source")
	}
	if !reflect.DeepEqual(a.Sinks, b.Sinks) {
		blocks = append(blocks, "sinks")
	}
	if !reflect.DeepEqual(a.Pipelines, b.Pipelines) {
		blocks = append(blocks, "pipelines")
	}
	if !reflect.DeepEqual(a.Checkpoint, b.Checkpoint) {
		blocks = append(blocks, "checkpoint")
	}
	return blocks
}

// comparableConfig returns a copy of the configuration without YAML nodes.
// Two parses of a file produce different node pointers, and comparing them
// would report every reload as a change.
func comparableConfig(cfg *Config) Config {
	out := *cfg
	out.doc = nil
	out.pipelineNodes = nil
	out.warnings = nil
	out.Sinks = make(map[string]*Component, len(cfg.Sinks))

	for name, component := range cfg.Sinks {
		out.Sinks[name] = comparableComponent(component)
	}
	out.Checkpoint.Store.node = nil

	return out
}

// comparableComponent returns a copy of the component without its node.
func comparableComponent(c *Component) *Component {
	if c == nil {
		return nil
	}
	out := *c
	out.node = nil

	return &out
}

// stripFilters clears every pipeline's filters, which is what distinguishes
// a filters-only change from a structural one.
func stripFilters(cfg *Config) {
	pipelines := make(map[string]pipeline.Config, len(cfg.Pipelines))

	for name, p := range cfg.Pipelines {
		p.Filters = nil
		pipelines[name] = p
	}
	cfg.Pipelines = pipelines
}
