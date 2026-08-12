package metrics

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shibernetes/kem-agent/event"
)

// logRecorder captures what a sink logs.
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

// messages returns the message of each record, in the order they were logged.
func (h *logRecorder) messages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	msgs := make([]string, 0, len(h.records))
	for _, r := range h.records {
		msgs = append(msgs, r.Message)
	}
	return msgs
}

// attr returns the value of an attribute for the record at index i.
func (h *logRecorder) attr(i int, key string) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	var value string
	h.records[i].Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			value = a.Value.String()
			return false
		}
		return true
	})
	return value
}

// testMetric returns a metric counting events by namespace and reason.
func testMetric() Metric {
	return Metric{
		Name:   "warnings",
		Help:   "Warning events by namespace and reason.",
		Labels: map[string]string{"reason": "reason", "ns": "namespace"},
	}
}

// testConfig returns a configuration declaring one metric, adjusted by
// opts. It is not validated, so a case may make it invalid on purpose.
func testConfig(opts func(*Config)) Config {
	cfg := DefaultConfig()

	cfg.Type = "metrics"
	cfg.Metrics = []Metric{testMetric()}

	if opts != nil {
		opts(&cfg)
	}
	return cfg
}

// newTestSink returns a sink built from a configuration that validates.
func newTestSink(t *testing.T, cfg Config) *Sink {
	t.Helper()

	s, _ := newRecordingSink(t, cfg)
	return s
}

// newRecordingSink returns a sink and the recorder holding what it logs.
func newRecordingSink(t *testing.T, cfg Config) (*Sink, *logRecorder) {
	t.Helper()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("configuration did not validate: %v", err)
	}
	rec := &logRecorder{}

	return New("warn-count", cfg, rec.logger()), rec
}

func testEvent() *event.Event {
	return &event.Event{
		UID:       "d1f5c2a0-1111-2222-3333-444455556666",
		Namespace: "team-a",
		Name:      "web-1.17f0a2b3c4d5e6f7",
		EventTime: metav1.NewMicroTime(time.Date(2009, 1, 3, 18, 15, 5, 0, time.UTC)),
		Reason:    "OOMKilled",
		Regarding: corev1.ObjectReference{
			Kind:       "Pod",
			Namespace:  "team-a",
			Name:       "web-1",
			APIVersion: "v1",
		},
		Note: "Container web was OOM killed",
		Type: "Warning",
	}
}

// scrape returns the series a sink exposes, one sample per line, in
// the Prometheus text format.
func scrape(t *testing.T, s *Sink) []string {
	t.Helper()

	reg := prometheus.NewRegistry()

	for _, collector := range s.Collectors() {
		if err := reg.Register(collector); err != nil {
			t.Fatalf("failed to register collector: %v", err)
		}
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather: %v", err)
	}
	var count int

	for _, family := range families {
		count += len(family.GetMetric())
	}
	series := make([]string, 0, count)

	for _, family := range families {
		for _, metric := range family.GetMetric() {
			series = append(series, family.GetName()+renderLabels(metric)+" "+renderValue(metric))
		}
	}
	return series
}

// renderLabels returns the labels of one series, or an empty string when
// there are none. The client library sorts them by name, so a constant
// label sits among the variable ones.
func renderLabels(m *dto.Metric) string {
	if len(m.GetLabel()) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(m.GetLabel()))
	for _, l := range m.GetLabel() {
		pairs = append(pairs, l.GetName()+"="+strconv.Quote(l.GetValue()))
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func renderValue(m *dto.Metric) string {
	return strconv.FormatFloat(m.GetCounter().GetValue(), 'g', -1, 64)
}
