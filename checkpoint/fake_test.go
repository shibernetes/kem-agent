package checkpoint

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// fakeStore records the states a checkpointer saved.
type fakeStore struct {
	mu      sync.Mutex
	state   State
	loadErr error
	saveErr error
	states  []State
	onSave  func()
}

func (s *fakeStore) Load(context.Context) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.loadErr
}

func (s *fakeStore) Save(_ context.Context, state State) error {
	s.mu.Lock()
	state.Watches = maps.Clone(state.Watches)
	s.states = append(s.states, state)
	err, onSave := s.saveErr, s.onSave
	s.mu.Unlock()

	// The callback may block, so it must not hold the lock.
	if onSave != nil {
		onSave()
	}
	return err
}

func (s *fakeStore) attempts() []State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.states)
}

// fakeReporter reports the positions a test sets.
type fakeReporter struct {
	mu        sync.Mutex
	positions map[string]string
	noCopy    bool
}

func (r *fakeReporter) Positions() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.noCopy {
		return r.positions
	}
	return maps.Clone(r.positions)
}

func (r *fakeReporter) set(positions map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.positions = positions
}

// advance moves a watch position.
func (r *fakeReporter) advance(watch, position string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.positions[watch] = position
}

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

// metricsRecorder holds the instruments a checkpointer records into.
type metricsRecorder struct {
	success *fakeCounter
	failure *fakeCounter
	skipped *fakeCounter
}

func newMetricsRecorder() *metricsRecorder {
	return &metricsRecorder{
		success: &fakeCounter{},
		failure: &fakeCounter{},
		skipped: &fakeCounter{},
	}
}

func (r *metricsRecorder) metrics() Metrics {
	return Metrics{
		Success: r.success,
		Failure: r.failure,
		Skipped: r.skipped,
	}
}

// logRecorder captures what a checkpointer logs.
type logRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *logRecorder) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *logRecorder) WithGroup(string) slog.Handler            { return h }

func (h *logRecorder) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.records = append(h.records, r.Clone())
	return nil
}

func (h *logRecorder) logger() *slog.Logger {
	return slog.New(h)
}

// levels returns the level each record was logged at, in order.
func (h *logRecorder) levels() []slog.Level {
	h.mu.Lock()
	defer h.mu.Unlock()

	levels := make([]slog.Level, 0, len(h.records))
	for _, r := range h.records {
		levels = append(levels, r.Level)
	}
	return levels
}
