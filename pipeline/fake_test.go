package pipeline

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/filter"
	"github.com/shibernetes/kem-agent/sink"
)

// fakeCounter mocks a Prometheus counter, which reports
// what was added to it only through a registry.
type fakeCounter struct {
	prometheus.Counter
	mu sync.Mutex
	n  float64
}

func (c *fakeCounter) Inc() {
	c.Add(1)
}

func (c *fakeCounter) Add(v float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n += v
}

func (c *fakeCounter) get() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func counterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()

	fake, ok := c.(*fakeCounter)
	if !ok {
		t.Fatalf("the counter is a %T, want a fake", c)
	}
	return fake.get()
}

// fakeObserverVec counts the observations made against each result.
type fakeObserverVec struct {
	prometheus.ObserverVec
	mu      sync.Mutex
	results map[string]int
}

func (v *fakeObserverVec) WithLabelValues(lvs ...string) prometheus.Observer {
	return observeFunc(func(float64) {
		v.mu.Lock()
		defer v.mu.Unlock()
		v.results[lvs[0]]++
	})
}

func (v *fakeObserverVec) get(result sink.Result) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.results[result.String()]
}

// observeFunc adapts a function to the [prometheus.Observer] interface.
type observeFunc func(float64)

func (f observeFunc) Observe(v float64) {
	f(v)
}

// recorder holds the instruments a producer or a drainer records into,
// so that a test reads each of them by name.
type recorder struct {
	delivered *fakeCounter
	overflow  *fakeCounter
	tooLarge  *fakeCounter
	exhausted *fakeCounter
	permanent *fakeCounter
	rejected  *fakeCounter
	sends     *fakeObserverVec
}

func newRecorder() *recorder {
	return &recorder{
		delivered: &fakeCounter{},
		overflow:  &fakeCounter{},
		tooLarge:  &fakeCounter{},
		exhausted: &fakeCounter{},
		permanent: &fakeCounter{},
		rejected:  &fakeCounter{},
		sends:     &fakeObserverVec{results: make(map[string]int)},
	}
}

func (r *recorder) metrics() Metrics {
	return Metrics{
		Delivered:      r.delivered,
		Overflow:       r.overflow,
		TooLarge:       r.tooLarge,
		RetryExhausted: r.exhausted,
		PermanentError: r.permanent,
		Rejected:       r.rejected,
		SendDuration:   r.sends,
	}
}

// fakeEncoder writes an event's name as its frame, so a test
// sizes a frame by naming the event it encodes.
type fakeEncoder struct{}

func (fakeEncoder) AppendEvent(dst []byte, ev *event.Event) []byte {
	return append(dst, ev.Name...)
}

// fakeFramer wraps frames in a prefix and a suffix and joins
// them with a separator, so that a batch takes more space than
// the frames it holds.
type fakeFramer struct {
	sep    []byte
	prefix []byte
	suffix []byte
	mu     sync.Mutex
	counts []int
}

func (f *fakeFramer) Separator() []byte {
	return f.sep
}

func (f *fakeFramer) Fixed() int {
	return len(f.prefix) + len(f.suffix)
}

func (f *fakeFramer) Compose(dst, frames []byte, n int) []byte {
	f.mu.Lock()
	f.counts = append(f.counts, n)
	f.mu.Unlock()

	dst = append(dst, f.prefix...)
	dst = append(dst, frames...)

	return append(dst, f.suffix...)
}

// composed returns the frame count of each batch composed so far.
func (f *fakeFramer) composed() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.counts...)
}

// fakeEventSink records the events handed to it, and refuses
// every one of them once err is set. Handle runs on every
// dispatch goroutine feeding a sink, so it takes a lock.
type fakeEventSink struct {
	err    error
	mu     sync.Mutex
	events []*event.Event
}

func (*fakeEventSink) Name() string {
	return "fake"
}

func (*fakeEventSink) Shutdown(context.Context) error {
	return nil
}

func (s *fakeEventSink) Handle(ev *event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)

	return s.err
}

// handled returns the events the sink was given so far.
func (s *fakeEventSink) handled() []*event.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*event.Event(nil), s.events...)
}

// response is the reply a fake batch sink answers one send with.
type response struct {
	err     error
	refused int
}

// fakeBatchSink records the payloads sent to it and answers
// each send with the next entry in responses, repeating the
// last one once they run out.
type fakeBatchSink struct {
	framer    *fakeFramer
	responses []response
	mu        sync.Mutex
	sent      [][]byte
	ids       []sink.BatchID
	ctxErrs   []error
	block     chan struct{}
}

// newFakeBatchSink returns a batch sink accepting every send.
func newFakeBatchSink() *fakeBatchSink {
	return &fakeBatchSink{framer: &fakeFramer{}}
}

func (*fakeBatchSink) Name() string {
	return "fake"
}

func (*fakeBatchSink) Shutdown(context.Context) error {
	return nil
}

func (*fakeBatchSink) Encoder() sink.Encoder {
	return fakeEncoder{}
}

func (s *fakeBatchSink) Framer() sink.Framer {
	return s.framer
}

func (s *fakeBatchSink) Send(ctx context.Context, payload []byte, id sink.BatchID) (int, error) {
	s.mu.Lock()
	attempt := len(s.sent)
	s.sent = append(s.sent, append([]byte(nil), payload...))
	s.ids = append(s.ids, id)
	s.ctxErrs = append(s.ctxErrs, ctx.Err())
	block := s.block
	s.mu.Unlock()

	// A blocking sink holds its drainer until the test releases it,
	// or until the attempt's own deadline expires.
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return 0, context.Cause(ctx)
		}
	}
	if len(s.responses) == 0 {
		return 0, nil
	}
	return s.response(attempt).refused, s.response(attempt).err
}

func (s *fakeBatchSink) contextErrors() []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.ctxErrs)
}

// response returns the response entry for the given attempt.
func (s *fakeBatchSink) response(attempt int) response {
	return s.responses[min(attempt, len(s.responses)-1)]
}

// payloads returns the payloads the sink has been sent so far.
func (s *fakeBatchSink) payloads() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.sent...)
}

// batchIDs returns the identifier carried by each attempt so far.
func (s *fakeBatchSink) batchIDs() []sink.BatchID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sink.BatchID(nil), s.ids...)
}

// producerLog records the sinks an event reached, in order.
type producerLog struct {
	calls []string
}

// producer returns a producer logging under the given name.
func (l *producerLog) producer(name string) Producer {
	return &loggingProducer{log: l, name: name}
}

// count returns how many events reached the named sink.
func (l *producerLog) count(name string) int {
	n := 0
	for _, call := range l.calls {
		if call == name {
			n++
		}
	}
	return n
}

type loggingProducer struct {
	log  *producerLog
	name string
}

func (p *loggingProducer) Produce(scratch []byte, _ *event.Event) []byte {
	p.log.calls = append(p.log.calls, p.name)
	return scratch
}

// noopProducer keeps the buffer it is given and records nothing,
// so a measurement sees the fan-out alone.
type noopProducer struct{}

func (noopProducer) Produce(scratch []byte, _ *event.Event) []byte {
	return scratch
}

// growingProducer fills the buffer it is given, as an encoder
// writing a frame does, and returns the grown slice.
type growingProducer struct {
	frame int
}

func (p *growingProducer) Produce(scratch []byte, _ *event.Event) []byte {
	scratch = scratch[:0]
	for range p.frame {
		scratch = append(scratch, 'x')
	}
	return scratch
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

func (h *logRecorder) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.records)
}

// attr returns an attribute value of the nth record, or an
// empty string when it doesn't exist.
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

// testEvent returns an event encoding to an n byte frame, the
// fake encoder writing the name of the event as the frame.
func testEvent(n int) *event.Event {
	return &event.Event{Name: strings.Repeat("x", n)}
}

// testPipeline returns a pipeline delivering to the given sinks,
// with counters a test can read.
func testPipeline(name string, targets ...Producer) *Pipeline {
	return &Pipeline{
		Name:    name,
		Targets: targets,
		Metrics: FilterMetrics{
			Matched:     &fakeCounter{},
			EvalError:   &fakeCounter{},
			EvalCost:    &fakeCounter{},
			EvalTimeout: &fakeCounter{},
		},
	}
}

// matchAll returns n filter sets, none of which rejects anything.
func matchAll(n int) []filter.Set {
	return make([]filter.Set, n)
}

func newTestFanouts(t *testing.T, fs []filter.Set, pipelines []*Pipeline, watches ...string) map[string]*Fanout {
	t.Helper()

	filters := new(atomic.Pointer[[]filter.Set])
	filters.Store(&fs)

	fanouts := NewFanouts(FanoutOptions{
		Pipelines: pipelines,
		Watches:   watches,
		Filters:   filters,
		Timeout:   time.Minute,
		Logger:    slog.New(slog.DiscardHandler),
	})
	t.Cleanup(func() {
		for _, f := range fanouts {
			f.Close()
		}
	})
	return fanouts
}

// fanoutFor returns the fan-out bound to a watch.
func fanoutFor(tb testing.TB, fanouts map[string]*Fanout, watch string) *Fanout {
	tb.Helper()

	f := fanouts[watch]
	if f == nil {
		tb.Fatalf("no fan-out is bound to watch %q", watch)
	}
	return f
}
