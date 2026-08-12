package metrics

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/logsample"
	"github.com/shibernetes/kem-agent/sink"
)

const (
	// keySize is the initial capacity of a series key buffer.
	keySize = 128
)

var (
	_ sink.EventSink    = (*Sink)(nil)
	_ sink.Instrumented = (*Sink)(nil)
)

// A Sink records events as Prometheus metrics. It has no registry of its
// own. The agent collects metrics through [Sink.Collectors] and registers
// them on the registry that serves its metrics endpoint.
type Sink struct {
	name        string
	logger      *slog.Logger
	instruments []*instrument
	maxSeries   int
	series      sync.Map
	admitted    atomic.Int64
	capped      *logsample.Gate[int]
	pool        sync.Pool
}

// scratch is the working state of one Handle call.
type scratch struct {
	values []string
	key    []byte
}

// New returns a sink recording events as metrics.
func New(name string, cfg Config, logger *slog.Logger) *Sink {
	var (
		instruments = make([]*instrument, len(cfg.Metrics))
		maxFields   int
	)
	for i, m := range cfg.Metrics {
		instruments[i] = newInstrument(cfg, m)
		maxFields = max(maxFields, len(instruments[i].fields))
	}
	s := &Sink{
		name:        name,
		logger:      logger,
		instruments: instruments,
		maxSeries:   cfg.MaxSeries,
		capped:      logsample.FirstSeen[int](len(instruments)),
	}
	s.pool.New = func() any {
		return &scratch{
			values: make([]string, 0, maxFields),
			key:    make([]byte, 0, keySize),
		}
	}
	return s
}

// Name implements the [sink.Sink] interface.
func (s *Sink) Name() string {
	return s.name
}

// Handle implements the [sink.EventSink] interface.
// It increments every configured counter once, and rejects the event
// only when no counter recorded it.
func (s *Sink) Handle(ev *event.Event) error {
	sc, _ := s.pool.Get().(*scratch)
	defer s.pool.Put(sc)

	var recorded bool
	for i, in := range s.instruments {
		sc.values = in.resolve(sc.values[:0], ev)
		if !s.admit(sc, i) {
			s.warnRefused(i, in)
			continue
		}
		in.inc(sc.values)
		recorded = true
	}
	if !recorded {
		return sink.ErrRejected
	}
	return nil
}

// Collectors implements the [sink.Instrumented] interface.
func (s *Sink) Collectors() []prometheus.Collector {
	cols := make([]prometheus.Collector, len(s.instruments))
	for i, in := range s.instruments {
		cols[i] = in.vec
	}
	return cols
}

// Shutdown implements the [sink.Sink] interface.
func (s *Sink) Shutdown(context.Context) error {
	return nil
}

// admit reports whether the label values an instrument resolved may be
// recorded, and remembers them when they form a series the sink has not
// admitted yet.
//
// An unlimited sink keeps no set at all, so the common configuration
// pays nothing for a limit it does not use. A limited one admits from
// several dispatch goroutines at once, and takes its slot before it
// stores the series, so the limit holds however they interleave.
func (s *Sink) admit(sc *scratch, index int) bool {
	if s.maxSeries <= 0 {
		return true
	}
	sc.key = appendSeriesKey(sc.key[:0], index, sc.values)

	if _, ok := s.series.Load(string(sc.key)); ok {
		return true
	}
	if !s.reserve() {
		return false
	}
	if _, loaded := s.series.LoadOrStore(string(sc.key), struct{}{}); loaded {
		s.admitted.Add(-1)
	}
	return true
}

// reserve tries to take one of the series the limit allows, and reports
// whether it succeeded.
func (s *Sink) reserve() bool {
	for {
		admitted := s.admitted.Load()
		if admitted >= int64(s.maxSeries) {
			return false
		}
		if s.admitted.CompareAndSwap(admitted, admitted+1) {
			return true
		}
	}
}

// warnRefused warns when the series limit refuses a new combination
// of label values, once per instrument.
func (s *Sink) warnRefused(index int, in *instrument) {
	suppressed, ok := s.capped.Admit(index)
	if !ok {
		return
	}
	s.logger.LogAttrs(context.Background(), slog.LevelWarn,
		"series limit reached, new label combinations are dropped",
		slog.String("metric", in.name),
		slog.Int("max", s.maxSeries),
		suppressed)
}

// appendSeriesKey appends the key of one series to dst, and returns the
// extended buffer. The key is composed of the index of its instrument
// followed by its label values.
//
// The index is part of the key, since two instruments sharing a combination
// of label values would otherwise be counted as a single series.
func appendSeriesKey(dst []byte, index int, values []string) []byte {
	dst = strconv.AppendInt(dst, int64(index), 10)

	for _, value := range values {
		// The separator cannot occur in a valid UTF-8 label value,
		// so a key is unambiguous.
		dst = append(dst, model.SeparatorByte)
		dst = append(dst, value...)
	}
	return dst
}
