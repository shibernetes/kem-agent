package pipeline

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/filter"
	"github.com/shibernetes/kem-agent/internal/logsample"
)

const (
	filterFailInterval  = time.Minute
	filterFailThreshold = 3
)

// Pipeline is the main event-processing unit. It applies its filters
// to each event of the watches it is bound to, and delivers the ones
// that pass to each of its target sinks.
type Pipeline struct {
	Name         string
	Watches      []string
	Targets      []Producer
	Metrics      FilterMetrics
	FilterSetIdx int
	targets      []target
}

// target is one sink a pipeline delivers to, and its fan-out index.
type target struct {
	sinkIdx  int
	producer Producer
}

// Fanout delivers the events of one watch to its bound pipelines.
type Fanout struct {
	pipelines []*Pipeline
	filters   *atomic.Pointer[[]filter.Set]
	state     *filter.EvalState
	sinks     sinkSet
	buf       []byte
	failures  *logsample.Gate[filter.Reason]
	logger    *slog.Logger
}

// FanoutOptions configures the fan-outs of a set of watches.
type FanoutOptions struct {
	Pipelines []*Pipeline
	Watches   []string
	Filters   *atomic.Pointer[[]filter.Set]
	Timeout   time.Duration
	Logger    *slog.Logger
}

// NewFanouts returns the fan-out of each given watch, and resolves the
// sinks of every pipeline it is given. Each distinct sink is numbered
// once across all of them, which is how an event that matches two
// pipelines sharing one still reaches it once.
//
// The timeout bounds the evaluation of a whole event rather than of a
// single expression.
func NewFanouts(opts FanoutOptions) map[string]*Fanout {
	// Sorted once, so that two watches sharing a pipeline evaluate the
	// ones they have in common in the same order.
	sorted := slices.SortedFunc(slices.Values(opts.Pipelines),
		func(a, b *Pipeline) int {
			return cmp.Compare(a.Name, b.Name)
		},
	)
	indices := indexSinks(sorted)

	for _, p := range sorted {
		p.targets = make([]target, 0, len(p.Targets))
		for _, producer := range p.Targets {
			p.targets = append(p.targets, target{sinkIdx: indices[producer], producer: producer})
		}
	}
	fanouts := make(map[string]*Fanout, len(opts.Watches))

	for _, watch := range opts.Watches {
		fanouts[watch] = &Fanout{
			pipelines: bind(sorted, watch),
			filters:   opts.Filters,
			state:     filter.NewEvalState(opts.Timeout),
			sinks:     newSinkSet(len(indices)),
			failures:  logsample.PerInterval[filter.Reason](filterFailInterval, filterFailThreshold),
			logger:    opts.Logger,
		}
	}
	return fanouts
}

// Dispatch evaluates an event against every bound pipeline and hands it
// to the sinks of the ones it matches.
//
// It runs on the goroutine of the watch that read the event, and reuses
// the state it holds from one event to the next, so a fan-out is never
// shared between watches.
func (f *Fanout) Dispatch(ctx context.Context, ev *event.Event) {
	sets := *f.filters.Load()

	f.state.Reset(ev)
	f.sinks.clear()

	for _, p := range f.pipelines {
		matched, reason := f.state.Match(sets[p.FilterSetIdx])
		if reason != "" {
			f.drop(ctx, p, reason)

			// The cost limit and the deadline bound the event rather
			// than one expression, so the sets left would fail alike.
			if reason == filter.ReasonEvalCost || reason == filter.ReasonEvalTimeout {
				return
			}
			continue
		}
		if !matched {
			continue
		}
		p.Metrics.Matched.Inc()

		for _, t := range p.targets {
			if !f.sinks.mark(t.sinkIdx) {
				continue
			}
			f.buf = t.producer.Produce(f.buf, ev)
		}
	}
}

// Close releases the evaluation deadline. It must be called once the
// watch the fan-out serves has stopped.
func (f *Fanout) Close() {
	f.state.Close()
}

// drop counts an event a pipeline could not evaluate.
// It also reports what went wrong at a bounded rate, since a broken
// expression fails for every event.
func (f *Fanout) drop(ctx context.Context, p *Pipeline, reason filter.Reason) {
	switch reason {
	case filter.ReasonEvalError:
		p.Metrics.EvalError.Inc()
	case filter.ReasonEvalCost:
		p.Metrics.EvalCost.Inc()
	case filter.ReasonEvalTimeout:
		p.Metrics.EvalTimeout.Inc()
	}
	suppressed, ok := f.failures.Admit(reason)
	if !ok {
		return
	}
	expr, err := f.state.Failure()

	f.logger.LogAttrs(ctx, slog.LevelWarn, "filter evaluation failed",
		slog.String("pipeline", p.Name),
		slog.String("reason", reason.String()),
		slog.String("expr", expr),
		slog.Any("error", err),
		suppressed,
	)
}

// indexSinks indexes each distinct sink the pipelines deliver to,
// in the order they are given, so that one configuration always
// numbers them the same way.
func indexSinks(pipelines []*Pipeline) map[Producer]int {
	indices := make(map[Producer]int)

	for _, p := range pipelines {
		for _, producer := range p.Targets {
			if _, ok := indices[producer]; !ok {
				indices[producer] = len(indices)
			}
		}
	}
	return indices
}

// bind returns the pipelines a watch delivers to, in the given order.
// A pipeline bound to no specific watches takes the events of all.
func bind(pipelines []*Pipeline, watch string) []*Pipeline {
	bound := make([]*Pipeline, 0, len(pipelines))

	for _, p := range pipelines {
		if len(p.Watches) == 0 || slices.Contains(p.Watches, watch) {
			bound = append(bound, p)
		}
	}
	return bound
}
