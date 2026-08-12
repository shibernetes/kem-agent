package agent

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shibernetes/kem-agent/config"
	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/filter"
	"github.com/shibernetes/kem-agent/sink"
)

const (
	neverSetMsg = "but source.enrichment declares no resource, so it is never set"
)

type fieldReader interface {
	Fields() []sink.FieldRef
}

// ConfigWarnings returns every warning a parsed config raises, the
// cross-reference warnings included.
func ConfigWarnings(res *config.Result) []config.Warning {
	return slices.Concat(res.Warnings, EnrichmentWarnings(res))
}

// EnrichmentWarnings reports the filters and sinks that read the enriched
// object while the enrichment configuration declares no resource, which leaves
// it nil for every event.
//
// Nothing fails in that case. The filter never matches and the label or tag
// renders empty, so the only effect is missing data.
func EnrichmentWarnings(res *config.Result) []config.Warning {
	if len(res.Config.Source.Enrichment.Resources) > 0 {
		return nil
	}
	return slices.Concat(filterWarnings(res), sinkWarnings(res))
}

// filterWarnings reports the filters that read the enriched object.
func filterWarnings(parsed *config.Result) []config.Warning {
	var (
		env      *filter.Env
		warnings []config.Warning
	)
	for _, name := range slices.Sorted(maps.Keys(parsed.Config.Pipelines)) {
		for i, expr := range parsed.Config.Pipelines[name].Filters {
			// The raw expressions is checked first, so a config that reads no
			// enriched field skips a compilation.
			// A quoted literal such as `event.note.contains('regardingObject')"
			// is a false-positive, which the walk below rules out.
			if !strings.Contains(expr, event.FieldRegardingObject) {
				continue
			}
			if env == nil {
				compiled, err := filter.NewEnv()
				if err != nil {
					return warnings
				}
				env = compiled
			}
			fields, err := env.Fields(expr)
			if err != nil {
				continue
			}
			for _, field := range fields {
				if !fieldReadsRegarding(field) {
					continue
				}
				warnings = append(warnings, config.Warning{
					Node: parsed.FilterNode(name, i),
					Message: fmt.Sprintf("pipelines[%s]: filters[%d] reads %s, %s",
						name, i, field, neverSetMsg,
					),
				})
			}
		}
	}
	return warnings
}

// sinkWarnings reports the sinks that read the enriched object.
func sinkWarnings(parsed *config.Result) []config.Warning {
	var warnings []config.Warning

	for _, name := range slices.Sorted(maps.Keys(parsed.Config.Sinks)) {
		component, ok := parsed.Config.Sinks[name]
		if !ok {
			continue
		}
		reader, ok := component.Config.(fieldReader)
		if !ok {
			continue
		}
		for _, ref := range reader.Fields() {
			if !fieldReadsRegarding(ref.Field) {
				continue
			}
			node := parsed.SinkFieldNode(name, ref.Path)
			if node == nil {
				node = component.Node()
			}
			warnings = append(warnings, config.Warning{
				Node: node,
				Message: fmt.Sprintf("sinks[%s]: %s reads %s, %s",
					name, ref.Path, ref.Field, neverSetMsg,
				),
			})
		}
	}
	return warnings
}

// fieldReadsRegarding reports whether a field path reads the enriched object.
func fieldReadsRegarding(field string) bool {
	root, _, _ := strings.Cut(field, ".")
	return root == event.FieldRegardingObject
}
