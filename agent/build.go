package agent

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"k8s.io/client-go/kubernetes"

	"github.com/shibernetes/kem-agent/checkpoint"
	"github.com/shibernetes/kem-agent/checkpoint/configmap"
	"github.com/shibernetes/kem-agent/checkpoint/file"
	"github.com/shibernetes/kem-agent/config"
	"github.com/shibernetes/kem-agent/filter"
	"github.com/shibernetes/kem-agent/internal/identity"
	"github.com/shibernetes/kem-agent/pipeline"
	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/source"
)

// A FilterError reports one filter expression that failed to compile.
type FilterError struct {
	Pipeline string
	Index    int
	Err      error
}

// Error implements the error interface.
func (e *FilterError) Error() string {
	return fmt.Sprintf("pipelines[%s]: filters[%d]: %s", e.Pipeline, e.Index, e.Err)
}

// Unwrap returns the compile failure the expression was rejected with.
func (e *FilterError) Unwrap() error {
	return e.Err
}

// A sinkEntry holds a sink and the delivery path components feeding it.
// A sink is a single instance however many pipelines name it, so they
// all produce into the same entry and its queue.
type sinkEntry struct {
	name       string
	sink       sink.Sink
	drainer    *pipeline.Drainer
	producer   pipeline.Producer
	opener     sink.Opener
	queueLimit int
	started    bool
}

func buildSinks(c map[string]*config.Component, f sink.Factories, m identity.AgentMetadata, in *instruments, l *slog.Logger) ([]*sinkEntry, error) {
	entries := make([]*sinkEntry, 0, len(c))

	for _, name := range slices.Sorted(maps.Keys(c)) {
		component, ok := c[name]
		if !ok {
			continue
		}

		factory, ok := f[component.Type]
		if !ok {
			return nil, fmt.Errorf("sinks[%s]: no factory for type %q", name, component.Type)
		}
		sinkLogger := l.With(
			slog.String("component", "sink"),
			slog.String("sink", name),
			slog.String("type", component.Type),
		)
		opts := sink.Options{
			Name:     name,
			Logger:   sinkLogger,
			Identity: m,
		}
		s, err := factory.Create(opts, component.Config)
		if err != nil {
			return nil, fmt.Errorf("sinks[%s]: failed to build: %w", name, err)
		}
		entry, err := newSinkEntry(name, s, component.Config, in, sinkLogger)
		if err != nil {
			return nil, fmt.Errorf("sinks[%s]: %w", name, err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// newSinkEntry builds the entry one sink is delivered through. Which
// delivery interface it implements is settled here rather than per event,
// so dispatch is a single call per sink.
func newSinkEntry(name string, s sink.Sink, cfg sink.Config, in *instruments, l *slog.Logger) (*sinkEntry, error) {
	entry := &sinkEntry{name: name, sink: s}
	entry.opener, _ = s.(sink.Opener)

	// The batch interface is asserted first, being the harder of
	// the two to satisfy by accident. A batch sink that added a
	// Handle method later would otherwise lose its queue, its batching
	// and its retry, silently.
	switch typed := s.(type) {
	case sink.BatchSink:
		drainable, ok := cfg.(sink.Drainable)
		if !ok {
			return nil, errors.New("batch sink configuration has no delivery settings")
		}
		settings := drainable.DrainerConfig()

		entry.drainer = pipeline.NewDrainer(typed, settings, in.forBatchSink(name), l)
		entry.producer = entry.drainer.Producer()
		entry.queueLimit = int(settings.Queue.MaxBytes)
	case sink.EventSink:
		entry.producer = pipeline.NewDirectProducer(typed, in.forEventSink(name))
	default:
		return nil, errors.New("sink implements neither a batch nor an event delivery interface")
	}
	return entry, nil
}

// buildPipelines builds every declared pipeline, resolving the sinks it
// references to their producers. They are sorted by name, and each takes
// its position as the index of the filter set it evaluates.
func buildPipelines(declared map[string]pipeline.Config, sinks []*sinkEntry, in *instruments) ([]*pipeline.Pipeline, error) {
	producers := make(map[string]pipeline.Producer, len(sinks))
	for _, entry := range sinks {
		producers[entry.name] = entry.producer
	}
	var (
		names = slices.Sorted(maps.Keys(declared))
		built = make([]*pipeline.Pipeline, 0, len(names))
	)
	for i, name := range names {
		cfg := declared[name]

		targets := make([]pipeline.Producer, 0, len(cfg.Sinks))
		for _, sinkName := range cfg.Sinks {
			producer, ok := producers[sinkName]
			if !ok {
				return nil, fmt.Errorf("pipelines[%s]: sinks: %q is not declared", name, sinkName)
			}
			targets = append(targets, producer)
		}
		built = append(built, &pipeline.Pipeline{
			Name:         name,
			Watches:      cfg.Watches,
			Targets:      targets,
			FilterSetIdx: i,
			Metrics:      in.forPipeline(name),
		})
	}
	return built, nil
}

// compileFilters compiles the filters of every declared pipeline, keyed by name.
func compileFilters(env *filter.Env, declared map[string]pipeline.Config) (map[string]filter.Set, error) {
	sets := make(map[string]filter.Set, len(declared))

	for _, name := range slices.Sorted(maps.Keys(declared)) {
		filters := declared[name].Filters
		set := make(filter.Set, 0, len(filters))

		for i, expr := range filters {
			rule, err := env.Compile(expr)
			if err != nil {
				return nil, &FilterError{Pipeline: name, Index: i, Err: err}
			}
			set = append(set, rule)
		}
		// A pipeline with no filter is still added. Omitting it would
		// be interpreted as a pipeline that was removed from the config.
		sets[name] = set
	}
	return sets, nil
}

// buildStore builds the checkpoint store the configuration declares.
func buildStore(client kubernetes.Interface, c *config.Component) (checkpoint.Store, error) {
	switch cfg := c.Config.(type) {
	case *configmap.Config:
		return configmap.New(client, *cfg), nil
	case *file.Config:
		return file.New(*cfg), nil
	default:
		return nil, fmt.Errorf("checkpoint.store: no store for type %q", c.Type)
	}
}

// orderFilterSets orders each new set at the index of its pipeline,
// so a single store swaps the filters of every pipeline at once.
//
// oldSets is used to find a pipeline's filter set when newSets doesn't
// contain it, which happens when the config no longer declares that
// pipeline after a reload.
func orderFilterSets(pipelines []*pipeline.Pipeline, newSets map[string]filter.Set, oldSets []filter.Set) []filter.Set {
	next := make([]filter.Set, len(pipelines))

	for _, p := range pipelines {
		set, ok := newSets[p.Name]
		if !ok && p.FilterSetIdx < len(oldSets) {
			// A pipeline the new config no longer declares keeps the
			// set it runs until a full restart. The zero value is an
			// empty set, which matches everything, so the pipeline
			// just removed would start delivering every event.
			set = oldSets[p.FilterSetIdx]
		}
		next[p.FilterSetIdx] = set
	}
	return next
}

// watchKeys returns the key of every configured watch, which is the
// namespace it reads or an empty string for the all-namespaces watch.
func watchKeys(watches []source.WatchConfig) []string {
	keys := make([]string, 0, len(watches))
	for _, w := range watches {
		keys = append(keys, w.Namespace)
	}
	return keys
}
