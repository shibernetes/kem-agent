package metrics

import (
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/common/model"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/sink"
)

func TestHandleIncrementsEveryInstrument(t *testing.T) {
	s := newTestSink(t, similarMetrics(0))

	if err := s.Handle(testEvent()); err != nil {
		t.Fatalf("the event was not recorded: %v", err)
	}
	want := []string{
		`kem_events_a{ns="team-a"} 1`,
		`kem_events_b{ns="team-a"} 1`,
	}
	if diff := cmp.Diff(want, scrape(t, s)); diff != "" {
		t.Errorf("the series differ (-want +got):\n%s", diff)
	}
}

func TestHandleDeliversPartialRefusal(t *testing.T) {
	s := newTestSink(t, similarMetrics(1))

	if err := s.Handle(testEvent()); err != nil {
		t.Fatalf("the event was rejected: %v", err)
	}
	want := []string{`kem_events_a{ns="team-a"} 1`}
	if diff := cmp.Diff(want, scrape(t, s)); diff != "" {
		t.Errorf("the series differ (-want +got):\n%s", diff)
	}
}

func TestHandleRejectsWhenNothingRecorded(t *testing.T) {
	s := newTestSink(t, testConfig(func(c *Config) {
		c.MaxSeries = 1
	}))
	if err := s.Handle(testEvent()); err != nil {
		t.Fatalf("the first event was rejected: %v", err)
	}
	if err := s.Handle(eventFrom("team-b")); !errors.Is(err, sink.ErrRejected) {
		t.Errorf("got %v, want the event rejected", err)
	}
}

func TestMaxSeriesRefusesNewCombinations(t *testing.T) {
	const limit = 3

	s := newTestSink(t, testConfig(func(c *Config) {
		c.MaxSeries = limit
	}))
	for i := range 10 {
		_ = s.Handle(eventFrom("team-" + strconv.Itoa(i)))
	}
	if got := len(scrape(t, s)); got != limit {
		t.Errorf("got %d series, want %d", got, limit)
	}
}

// TestMaxSeriesAdmitsKnownCombination asserts that a series the sink
// already holds keeps being recorded once the limit is reached, so a
// full sink stops growing rather than stops counting.
func TestMaxSeriesAdmitsKnownCombination(t *testing.T) {
	s := newTestSink(t, testConfig(func(c *Config) {
		c.MaxSeries = 1
	}))
	for range 3 {
		_ = s.Handle(testEvent())
	}
	_ = s.Handle(eventFrom("team-b"))

	want := []string{`kem_events_warnings{ns="team-a",reason="OOMKilled"} 3`}
	if diff := cmp.Diff(want, scrape(t, s)); diff != "" {
		t.Errorf("the series differ (-want +got):\n%s", diff)
	}
}

// TestMaxSeriesCountsPerInstrument asserts that two metrics with the
// same label values are differentiated by the metric they belong to.
func TestMaxSeriesCountsPerInstrument(t *testing.T) {
	s := newTestSink(t, similarMetrics(2))

	_ = s.Handle(testEvent())
	_ = s.Handle(eventFrom("team-b"))

	want := []string{
		`kem_events_a{ns="team-a"} 1`,
		`kem_events_b{ns="team-a"} 1`,
	}
	if diff := cmp.Diff(want, scrape(t, s)); diff != "" {
		t.Errorf("the series differ (-want +got):\n%s", diff)
	}
}

// TestMaxSeriesHoldsUnderConcurrency asserts that the series limit is
// respected when several goroutines admit series concurrently.
//
// The window where two goroutines can both pass the check is tiny, and
// a round offers one chance to hit it. Running many rounds is what makes
// the test catch a limit that does not hold.
func TestMaxSeriesHoldsUnderConcurrency(t *testing.T) {
	const (
		limit      = 1
		goroutines = 16
		rounds     = 100
	)
	for range rounds {
		s := newTestSink(t, testConfig(func(c *Config) {
			c.MaxSeries = limit
		}))
		var (
			wg    sync.WaitGroup
			start = make(chan struct{})
		)
		for g := range goroutines {
			wg.Go(func() {
				<-start
				_ = s.Handle(eventFrom("team-" + strconv.Itoa(g)))
			})
		}
		close(start)
		wg.Wait()

		if got := len(scrape(t, s)); got != limit {
			t.Fatalf("got %d series, want %d", got, limit)
		}
	}
}

func TestUnlimitedAdmitsEveryCombination(t *testing.T) {
	const count = 50

	s := newTestSink(t, testConfig(nil))
	for i := range count {
		_ = s.Handle(eventFrom("team-" + strconv.Itoa(i)))
	}
	if got := len(scrape(t, s)); got != count {
		t.Errorf("got %d series, want %d", got, count)
	}
}

// TestWarnsOncePerInstrument asserts that the sink warns once for
// each metric, so a starved one is never hidden by another.
func TestWarnsOncePerInstrument(t *testing.T) {
	s, rec := newRecordingSink(t, similarMetrics(2))
	_ = s.Handle(testEvent())

	for i := range 3 {
		_ = s.Handle(eventFrom("team-" + strconv.Itoa(i)))
	}
	if got := rec.messages(); len(got) != 2 {
		t.Fatalf("got %d log lines, want one per metric: %v", len(got), got)
	}
	var (
		want = []string{
			"kem_events_a",
			"kem_events_b",
		}
		got = []string{
			rec.attr(0, "metric"),
			rec.attr(1, "metric"),
		}
	)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the metrics differ (-want +got):\n%s", diff)
	}
}

func TestCollectors(t *testing.T) {
	s := newTestSink(t, similarMetrics(0))

	if got := len(s.Collectors()); got != 2 {
		t.Errorf("got %d collectors, want one per metric", got)
	}
}

func TestAppendSeriesKey(t *testing.T) {
	// Converting the byte directly would encode it as a rune.
	separator := string([]byte{model.SeparatorByte})

	cases := map[string]struct {
		index  int
		values []string
		want   string
	}{
		"one value":   {0, []string{"team-a"}, "0" + separator + "team-a"},
		"two values":  {3, []string{"team-a", "OOMKilled"}, "3" + separator + "team-a" + separator + "OOMKilled"},
		"no value":    {1, nil, "1"},
		"empty value": {0, []string{""}, "0" + separator},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := string(appendSeriesKey(nil, tc.index, tc.values)); got != tc.want {
				t.Errorf("got key %q, want %q", got, tc.want)
			}
		})
	}
}

// similarMetrics returns a configuration whose metrics resolve to the
// same combination of label values, so that one event reaches both.
func similarMetrics(maxSeries int) Config {
	return testConfig(func(c *Config) {
		c.MaxSeries = maxSeries
		c.Metrics = []Metric{
			{Name: "a", Help: "Events by namespace.", Labels: map[string]string{"ns": "namespace"}},
			{Name: "b", Help: "Events by namespace.", Labels: map[string]string{"ns": "namespace"}},
		}
	})
}

// eventFrom returns an event for the given namespace.
func eventFrom(namespace string) *event.Event {
	ev := testEvent()
	ev.Namespace = namespace

	return ev
}
