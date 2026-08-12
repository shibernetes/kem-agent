package source

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/source/sanitizer"
)

const (
	testNamespace  = "team-a"
	testStreamSize = 16
)

// fakeWatch serves the watch calls of one watcher, recording the options
// each was opened with and handing back a stream the test drives.
// It must be built inside the synctest bubble that uses it.
type fakeWatch struct {
	mu      sync.Mutex
	opts    []metav1.ListOptions
	streams []*watch.FakeWatcher
	times   []time.Time
	errs    []error
	frames  []func(*watch.FakeWatcher)
}

// newFakeWatch returns a fake serving one stream per watch call.
// An error at a position is returned instead of that call's stream.
func newFakeWatch(errs ...error) *fakeWatch {
	return &fakeWatch{errs: errs}
}

func (w *fakeWatch) clientset() *fake.Clientset {
	cs := fake.NewClientset()
	cs.PrependWatchReactor("events", w.serve)

	return cs
}

func (w *fakeWatch) serve(action k8stesting.Action) (bool, watch.Interface, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	call := len(w.opts)
	w.opts = append(w.opts, action.(k8stesting.WatchActionImpl).ListOptions)
	w.times = append(w.times, time.Now())

	if call < len(w.errs) && w.errs[call] != nil {
		return true, nil, w.errs[call]
	}
	stream := watch.NewFakeWithChanSize(testStreamSize, false)
	w.streams = append(w.streams, stream)

	if n := len(w.streams) - 1; n < len(w.frames) {
		w.frames[n](stream)
		stream.Stop()
	}
	return true, stream, nil
}

func (w *fakeWatch) withFrames(frames ...func(*watch.FakeWatcher)) *fakeWatch {
	w.frames = frames
	return w
}

// options returns the options of the nth watch call.
func (w *fakeWatch) options(t *testing.T, n int) metav1.ListOptions {
	t.Helper()

	w.mu.Lock()
	defer w.mu.Unlock()

	if n >= len(w.opts) {
		t.Fatalf("the watch was opened %d times, want more than %d", len(w.opts), n)
	}
	return w.opts[n]
}

// calls returns how many times the watch was opened.
func (w *fakeWatch) calls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.opts)
}

// reconnectIntervals returns the durations the watcher waited before
// each reconnection.
func (w *fakeWatch) reconnectIntervals() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()

	intervals := make([]time.Duration, 0, len(w.times))
	for i := 1; i < len(w.times); i++ {
		intervals = append(intervals, w.times[i].Sub(w.times[i-1]))
	}
	return intervals
}

// stream returns the nth stream handed out.
func (w *fakeWatch) stream(t *testing.T, n int) *watch.FakeWatcher {
	t.Helper()

	w.mu.Lock()
	defer w.mu.Unlock()

	if n >= len(w.streams) {
		t.Fatalf("the watch served %d streams, want more than %d", len(w.streams), n)
	}
	return w.streams[n]
}

// eventRecorder collects the events a watcher dispatches.
type eventRecorder struct {
	mu     sync.Mutex
	events []*event.Event
}

// Dispatch implements the [Dispatcher] interface.
func (r *eventRecorder) Dispatch(_ context.Context, ev *event.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.events = append(r.events, ev)
}

// names returns each dispatched event's name, in read order.
func (r *eventRecorder) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	names := make([]string, 0, len(r.events))
	for _, ev := range r.events {
		names = append(names, ev.Name)
	}
	return names
}

// fakeCounter mocks a Prometheus counter, whose value is otherwise
// readable only through a registry.
type fakeCounter struct {
	prometheus.Counter
	mu sync.Mutex
	n  float64
}

func (c *fakeCounter) Inc() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
}

func (c *fakeCounter) get() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// metricsRecorder holds the instruments a watcher records into.
type metricsRecorder struct {
	read    *fakeCounter
	closed  *fakeCounter
	failed  *fakeCounter
	expired *fakeCounter
}

func newMetricsRecorder() *metricsRecorder {
	return &metricsRecorder{
		read:    &fakeCounter{},
		closed:  &fakeCounter{},
		failed:  &fakeCounter{},
		expired: &fakeCounter{},
	}
}

func (r *metricsRecorder) metrics() watchMetrics {
	return watchMetrics{
		read:           r.read,
		restartClosed:  r.closed,
		restartError:   r.failed,
		restartExpired: r.expired,
	}
}

func (r *metricsRecorder) counts() map[string]float64 {
	counts := make(map[string]float64)

	for name, c := range map[string]*fakeCounter{
		"read":    r.read,
		"closed":  r.closed,
		"error":   r.failed,
		"expired": r.expired,
	} {
		if n := c.get(); n > 0 {
			counts[name] = n
		}
	}
	return counts
}

type logRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *logRecorder) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.records = append(h.records, r.Clone())
	return nil
}

func (h *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *logRecorder) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *logRecorder) WithGroup(string) slog.Handler            { return h }

func (h *logRecorder) logger() *slog.Logger {
	return slog.New(h)
}

// count returns the number of records logged.
func (h *logRecorder) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.records)
}

// messages returns the message of each record, in the order
// they were logged.
func (h *logRecorder) messages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	msgs := make([]string, 0, len(h.records))
	for _, r := range h.records {
		msgs = append(msgs, r.Message)
	}
	return msgs
}

// attr returns an attribute value of the nth record, or an empty
// string when it doesn't exist.
func (h *logRecorder) attr(t *testing.T, n int, key string) string {
	t.Helper()

	h.mu.Lock()
	defer h.mu.Unlock()

	if n >= len(h.records) {
		t.Fatalf("%d lines were logged, want more than %d", len(h.records), n)
	}
	var value string
	h.records[n].Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			value = a.Value.String()
			return false
		}
		return true
	})
	return value
}

// noopEnricher attaches nothing, standing in for the enrichment
// a watch does not need.
type noopEnricher struct{}

func (noopEnricher) Enrich(context.Context, string, *event.Event) {}

// stampingEnricher attaches a regarding object to every event and
// records the watch it was asked about.
type stampingEnricher struct {
	watches []string
}

func (e *stampingEnricher) Enrich(_ context.Context, watch string, ev *event.Event) {
	e.watches = append(e.watches, watch)
	ev.RegardingObject = &event.RegardingObject{}
}

// dispatcherFunc adapts a function to the [Dispatcher] interface, for
// a test that inspects an event at the moment it is handed over.
type dispatcherFunc func(context.Context, *event.Event)

func (f dispatcherFunc) Dispatch(ctx context.Context, ev *event.Event) {
	f(ctx, ev)
}

func newTestWatcher(t *testing.T, opts watcherOptions) *namespaceWatcher {
	t.Helper()

	if opts.dispatcher == nil {
		opts.dispatcher = &eventRecorder{}
	}
	if opts.enricher == nil {
		opts.enricher = noopEnricher{}
	}
	if opts.sanitizers == nil {
		opts.sanitizers = sanitizer.New(sanitizer.DefaultConfig())
	}
	if opts.logger == nil {
		opts.logger = slog.New(slog.DiscardHandler)
	}
	if opts.metrics.read == nil {
		opts.metrics = newMetricsRecorder().metrics()
	}
	return newWatcher(opts)
}

// watchedEvent returns an event named after the resource version it carries.
func watchedEvent(rv string) *eventsv1.Event {
	return &eventsv1.Event{
		Name:            "event-" + rv,
		Namespace:       testNamespace,
		ResourceVersion: rv,
		EventTime:       testEventTime,
		Reason:          "OOMKilled",
		Type:            "Warning",
	}
}
