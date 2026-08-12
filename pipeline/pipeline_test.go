package pipeline

import (
	"slices"
	"testing"

	"github.com/shibernetes/kem-agent/event"
)

func TestNewFanoutsBindsWatches(t *testing.T) {
	var (
		log = &producerLog{}
		all = testPipeline("all", log.producer("all"))
		one = testPipeline("foo", log.producer("foo"))
	)
	one.Watches = []string{"team-a"}
	fanouts := newTestFanouts(t, matchAll(1), []*Pipeline{all, one}, "team-a", "team-b")

	fanoutFor(t, fanouts, "team-a").Dispatch(t.Context(), &event.Event{})
	fanoutFor(t, fanouts, "team-b").Dispatch(t.Context(), &event.Event{})

	want := []string{"all", "foo", "all"}
	if got := log.calls; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNewFanoutsSortsPipelines(t *testing.T) {
	log := &producerLog{}

	pipelines := []*Pipeline{
		testPipeline("c", log.producer("c")),
		testPipeline("a", log.producer("a")),
		testPipeline("b", log.producer("b")),
	}
	fanouts := newTestFanouts(t, matchAll(1), pipelines, "")
	fanoutFor(t, fanouts, "").Dispatch(t.Context(), &event.Event{})

	want := []string{"a", "b", "c"}
	if got := log.calls; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestNewFanoutsIndexesSinksOnce asserts that a sink shared by two
// pipelines is given a single index. The fan-out marks that index to
// skip a sink the event has already reached.
func TestNewFanoutsIndexesSinksOnce(t *testing.T) {
	var (
		log    = &producerLog{}
		shared = log.producer("shared")
		first  = testPipeline("a", shared)
		second = testPipeline("b", shared, log.producer("own"))
	)
	newTestFanouts(t, matchAll(1), []*Pipeline{first, second}, "")

	want := first.targets[0].sinkIdx
	if got := second.targets[0].sinkIdx; got != want {
		t.Errorf("got index %d for the sink shared with the first pipeline, want %d", got, want)
	}
	if second.targets[0].sinkIdx == second.targets[1].sinkIdx {
		t.Error("two distinct sinks share an index")
	}
}

// TestNewFanoutsIndexesFromSortedOrder asserts that a configuration
// indexes its sinks in a consistent way, whatever the order of the pipelines.
func TestNewFanoutsIndexesFromSortedOrder(t *testing.T) {
	var (
		log = &producerLog{}
		x   = log.producer("x")
		y   = log.producer("y")
	)
	forward := []*Pipeline{testPipeline("a", x), testPipeline("b", y)}
	reverse := []*Pipeline{testPipeline("b", y), testPipeline("a", x)}

	newTestFanouts(t, matchAll(1), forward, "")
	newTestFanouts(t, matchAll(1), reverse, "")

	if got, want := reverse[1].targets[0].sinkIdx, forward[0].targets[0].sinkIdx; got != want {
		t.Errorf("pipeline A's sink index is %d, want %d, whatever the order", got, want)
	}
}

func TestNewFanoutsResolvesTargets(t *testing.T) {
	var (
		log = &producerLog{}
		p   = testPipeline("a", log.producer("x"), log.producer("y"))
	)
	newTestFanouts(t, matchAll(1), []*Pipeline{p}, "")

	if got := len(p.targets); got != len(p.Targets) {
		t.Errorf("got %d resolved targets, want %d", got, len(p.Targets))
	}
}
