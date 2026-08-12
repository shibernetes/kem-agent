package pipeline

import (
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/filter"
)

// TestDispatchDeliversToMatchingPipelines asserts that an event
// is dispatched only to the sinks of the pipelines whose filters
// match it.
func TestDispatchDeliversToMatchingPipelines(t *testing.T) {
	var (
		log      = &producerLog{}
		normals  = testPipeline("normals", log.producer("normals"))
		warnings = testPipeline("warnings", log.producer("warnings"))
	)
	normals.FilterSetIdx = 1

	filters := []filter.Set{
		compileSet(t, "event.type == 'Warning'"),
		compileSet(t, "event.type == 'Normal'"),
	}
	fanouts := newTestFanouts(t, filters, []*Pipeline{warnings, normals}, "")
	fanoutFor(t, fanouts, "").Dispatch(t.Context(), &event.Event{Type: "Warning"})

	if got := log.calls; !slices.Equal(got, []string{"warnings"}) {
		t.Errorf("the event reached sinks %v, want the accepting pipeline's alone", got)
	}
	if got := counterValue(t, warnings.Metrics.Matched); got != 1 {
		t.Errorf("got %v matches on the accepting pipeline, want 1", got)
	}
	if got := counterValue(t, normals.Metrics.Matched); got != 0 {
		t.Errorf("got %v matches on the rejecting pipeline, want 0", got)
	}
}

// TestDispatchFeedsSharedSinkOnce asserts that an event matching two
// pipelines that name one sink reaches it once. Both pipelines still
// count the match, since it is the sink that is skipped.
func TestDispatchFeedsSharedSinkOnce(t *testing.T) {
	var (
		log    = &producerLog{}
		shared = log.producer("shared")
		first  = testPipeline("a", shared)
		second = testPipeline("b", shared)
	)
	fanouts := newTestFanouts(t, matchAll(1), []*Pipeline{first, second}, "")
	fanoutFor(t, fanouts, "").Dispatch(t.Context(), &event.Event{})

	if got := log.count("shared"); got != 1 {
		t.Errorf("the shared sink received the event %d times, want 1", got)
	}
	for _, p := range []*Pipeline{first, second} {
		if got := counterValue(t, p.Metrics.Matched); got != 1 {
			t.Errorf("pipeline %q counted %v matches, want 1", p.Name, got)
		}
	}
}

func TestDispatchClearsBetweenEvents(t *testing.T) {
	var (
		log    = &producerLog{}
		shared = log.producer("shared")
		first  = testPipeline("a", shared)
		second = testPipeline("b", shared)
	)
	f := fanoutFor(t, newTestFanouts(t, matchAll(1), []*Pipeline{first, second}, ""), "")

	f.Dispatch(t.Context(), &event.Event{Name: "one"})
	f.Dispatch(t.Context(), &event.Event{Name: "two"})

	if got := log.count("shared"); got != 2 {
		t.Errorf("the shared sink received %d events, want 2", got)
	}
}

// TestDispatchCountsEvalError asserts that an expression failing at run
// time drops the event for its own pipeline alone, leaving the ones
// after it to run.
func TestDispatchCountsEvalError(t *testing.T) {
	var (
		log  = &producerLog{}
		bad  = testPipeline("a", log.producer("a"))
		good = testPipeline("b", log.producer("b"))
	)
	good.FilterSetIdx = 1

	filters := []filter.Set{
		// An absent key errors in CEL rather than yielding an empty value.
		compileSet(t, "event.labels['unknown'] == 'x'"),
		compileSet(t, "event.type == 'Warning'"),
	}
	fanouts := newTestFanouts(t, filters, []*Pipeline{bad, good}, "")
	fanoutFor(t, fanouts, "").Dispatch(t.Context(), &event.Event{Type: "Warning"})

	if got := counterValue(t, bad.Metrics.EvalError); got != 1 {
		t.Errorf("got %v evaluation errors, want 1", got)
	}
	if got := log.calls; !slices.Equal(got, []string{"b"}) {
		t.Errorf("the event reached sinks %v, want only the good pipeline", got)
	}
}

// TestDispatchStopsOnEvalCost asserts that an event exhausting the cost
// limit is not evaluated against the next pipelines. The limit covers a
// whole event, so each of them would overrun in turn.
func TestDispatchStopsOnEvalCost(t *testing.T) {
	var (
		log   = &producerLog{}
		cheap = testPipeline("a", log.producer("a"))
		spent = testPipeline("b", log.producer("b"))
		after = testPipeline("c", log.producer("c"))
	)
	spent.FilterSetIdx = 1

	filters := []filter.Set{
		compileSet(t, "event.type == ''"),

		// A comprehension over a hundred sixteen-kilobyte values,
		// which no single expression is allowed to finish.
		compileSet(t, "event.labels.all(k, v, !v.contains('zz'))"),
	}
	f, _ := newFanout(t, time.Minute, filters, cheap, spent, after)
	f.Dispatch(t.Context(), eventWithLabels(100, 16<<10))

	if got := counterValue(t, spent.Metrics.EvalCost); got != 1 {
		t.Errorf("got %v cost overruns, want 1", got)
	}
	if got := counterValue(t, after.Metrics.Matched); got != 0 {
		t.Errorf("the pipeline after the overrun matched %v events, want 0", got)
	}
	if got := log.count("a"); got != 1 {
		t.Errorf("the pipeline before the overrun delivered %d events, want 1", got)
	}
}

func TestDispatchStopsOnEvalTimeout(t *testing.T) {
	var (
		slow  = testPipeline("a", noopProducer{})
		after = testPipeline("b", noopProducer{})
	)
	filters := []filter.Set{compileSet(t, "event.labels.all(k, v, v != '')")}

	f, _ := newFanout(t, time.Nanosecond, filters, slow, after)
	f.Dispatch(t.Context(), eventWithLabels(1000, 1))

	if got := counterValue(t, slow.Metrics.EvalTimeout); got != 1 {
		t.Errorf("got %v deadline overruns, want 1", got)
	}
	if got := counterValue(t, after.Metrics.EvalTimeout); got != 0 {
		t.Errorf("the next pipeline recorded %v deadline overruns, want none", got)
	}
}

func TestDispatchLogsFailure(t *testing.T) {
	p := testPipeline("broken", noopProducer{})

	filters := []filter.Set{compileSet(t, "event.labels['unknown'] == 'x'")}
	f, logs := newFanout(t, time.Minute, filters, p)
	f.Dispatch(t.Context(), &event.Event{})

	cases := map[string]string{
		"pipeline": "broken",
		"reason":   string(filter.ReasonEvalError),
		"expr":     "event.labels['unknown'] == 'x'",
	}
	for key, want := range cases {
		t.Run(key, func(t *testing.T) {
			if got := logs.attr(t, 0, key); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// TestDispatchSuppressesRepeatedFailures asserts that a broken filter
// expression is reported a few times and then silenced, since it fails
// for every event and would otherwise log once per event.
func TestDispatchSuppressesRepeatedFailures(t *testing.T) {
	p := testPipeline("broken", noopProducer{})

	filters := []filter.Set{compileSet(t, "event.labels['unknown'] == 'x'")}
	f, logs := newFanout(t, time.Minute, filters, p)

	for range filterFailThreshold * 3 {
		f.Dispatch(t.Context(), &event.Event{})
	}
	if got := logs.count(); got != filterFailThreshold {
		t.Errorf("got %d lines logged, want %d", got, filterFailThreshold)
	}
}

// compileSet compiles the expressions into the filters of one pipeline.
func compileSet(t *testing.T, exprs ...string) filter.Set {
	t.Helper()

	env, err := filter.NewEnv()
	if err != nil {
		t.Fatalf("failed to create env: %v", err)
	}
	set := make(filter.Set, 0, len(exprs))

	for _, expr := range exprs {
		rule, err := env.Compile(expr)
		if err != nil {
			t.Fatalf("failed to compile expression %q: %v", expr, err)
		}
		set = append(set, rule)
	}
	return set
}

// eventWithLabels returns an event carrying count labels of size bytes,
// which is what a comprehension over them is charged for.
func eventWithLabels(count, size int) *event.Event {
	labels := make(map[string]string, count)
	for i := range count {
		labels[strconv.Itoa(i)] = strings.Repeat("v", size)
	}
	return &event.Event{Labels: labels}
}

func newFanout(t *testing.T, timeout time.Duration, fs []filter.Set, pipelines ...*Pipeline) (*Fanout, *logRecorder) {
	t.Helper()

	logs := &logRecorder{}
	filters := new(atomic.Pointer[[]filter.Set])
	filters.Store(&fs)

	f := fanoutFor(t, NewFanouts(FanoutOptions{
		Pipelines: pipelines,
		Watches:   []string{""},
		Filters:   filters,
		Timeout:   timeout,
		Logger:    slog.New(logs),
	}), "")
	t.Cleanup(f.Close)

	return f, logs
}
