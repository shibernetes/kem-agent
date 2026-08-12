package source

import (
	"context"
	"log/slog"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNewRequiresDispatcher(t *testing.T) {
	opts := testOptions()
	opts.Dispatchers = nil

	_, err := New(opts)
	if err == nil {
		t.Fatal("a watch with no dispatcher was accepted, want it rejected")
	}
	if !strings.Contains(err.Error(), "team-a") {
		t.Errorf("got %q, want the error to name the watch", err)
	}
}

func TestNewRequiresEnricher(t *testing.T) {
	opts := testOptions()
	opts.Enricher = nil

	if _, err := New(opts); err == nil {
		t.Error("a source with no enricher was accepted, want it rejected")
	}
}

// TestNewBuildsOneWatcherPerWatch asserts that a watcher is created for
// every declared watch, holding the dispatcher and the resume position
// that belong to its namespace.
func TestNewBuildsOneWatcherPerWatch(t *testing.T) {
	var (
		teamA = &eventRecorder{}
		teamB = &eventRecorder{}
		opts  = testOptions()
	)
	opts.Config.Watches = []WatchConfig{
		{Namespace: "team-a"},
		{Namespace: "team-b"},
	}
	opts.Dispatchers = map[string]Dispatcher{"team-a": teamA, "team-b": teamB}
	opts.Resume = map[string]string{"team-a": "42"}

	src, err := New(opts)
	if err != nil {
		t.Fatalf("failed to build source: %v", err)
	}
	if got := len(src.watchers); got != 2 {
		t.Fatalf("got %d watchers, want 2", got)
	}
	cases := map[string]struct {
		watcher    *namespaceWatcher
		dispatcher Dispatcher
		position   string
	}{
		"team-a": {src.watchers[0], teamA, "42"},
		"team-b": {src.watchers[1], teamB, ""},
	}
	for ns, tc := range cases {
		t.Run(ns, func(t *testing.T) {
			if got := tc.watcher.config.Namespace; got != ns {
				t.Errorf("got namespace %q, want %q", got, ns)
			}
			if tc.watcher.dispatcher != tc.dispatcher {
				t.Error("the watcher holds another watch's dispatcher")
			}
			if got := tc.watcher.position(); got != tc.position {
				t.Errorf("got position %q, want %q", got, tc.position)
			}
		})
	}
}

// TestRunStopsWithContext asserts that a canceled context ends
// every watch and that Run returns once they have.
func TestRunStopsWithContext(t *testing.T) {
	src := newTestSource(t, "team-a", "team-b")
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() {
		done <- src.Run(ctx)
	}()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run errored with %v, want a clean stop", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after its context was canceled")
	}
}

// TestRunReportsFatalAndStopsTheRest asserts that a watch the APIServer
// refuses to open ends the whole source, since the agent cannot serve
// the namespaces it was configured for.
func TestRunReportsFatalAndStopsTheRest(t *testing.T) {
	opts := testOptions()
	opts.Client = newFakeWatch(invalidError(metav1.CauseTypeForbidden, "sendInitialEvents")).clientset()
	opts.Config.Watches = []WatchConfig{
		{Namespace: "team-a"},
		{Namespace: "team-b"},
	}
	opts.Dispatchers = map[string]Dispatcher{
		"team-a": &eventRecorder{},
		"team-b": &eventRecorder{},
	}
	src, err := New(opts)
	if err != nil {
		t.Fatalf("failed to build source: %v", err)
	}
	// Run returns only once every watch has, so the rejection reaching
	// the caller is also what proves the others were stopped.
	err = src.Run(t.Context())
	if err == nil {
		t.Fatal("Run reported a clean stop, want an error")
	}
	if !strings.Contains(err.Error(), "initial events") {
		t.Errorf("got error %q, want it to name the refused option", err)
	}
}

func TestPositionsReportsEachWatch(t *testing.T) {
	src := newTestSource(t, "team-a", "team-b")
	src.watchers[0].advance("42")
	src.watchers[1].advance("17")

	want := map[string]string{
		"team-a": "42",
		"team-b": "17",
	}
	if got := src.Positions(); !maps.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestPositionsSkipsUnreadWatch asserts that a watch holding no position
// has no entry, rather than an empty one the checkpointer would store.
func TestPositionsSkipsUnreadWatch(t *testing.T) {
	src := newTestSource(t, "team-a", "team-b")
	src.watchers[0].advance("42")

	want := map[string]string{"team-a": "42"}
	if got := src.Positions(); !maps.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMetricsVecResolvesPerWatch(t *testing.T) {
	vec := testMetricsVec()
	m := vec.forWatch("team-a")

	cases := map[string]struct {
		got  prometheus.Counter
		want prometheus.Counter
	}{
		"read":    {m.read, vec.Read.WithLabelValues("team-a")},
		"closed":  {m.restartClosed, vec.Restarts.WithLabelValues("team-a", "closed")},
		"error":   {m.restartError, vec.Restarts.WithLabelValues("team-a", "error")},
		"expired": {m.restartExpired, vec.Restarts.WithLabelValues("team-a", "expired")},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Error("the resolved child is not the one carrying those labels")
			}
		})
	}
}

// testMetricsVec returns the vectors a source resolves its
// per-watch children from.
func testMetricsVec() MetricsVec {
	return MetricsVec{
		Read:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "read"}, []string{"namespace"}),
		Restarts: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "restarts"}, []string{"namespace", "reason"}),
	}
}

// testOptions returns the options of a source over one watch,
// which a test adjusts before building.
func testOptions() Options {
	return Options{
		Client:      fake.NewClientset(),
		Config:      testConfig(),
		Dispatchers: map[string]Dispatcher{"team-a": &eventRecorder{}},
		Enricher:    noopEnricher{},
		Metrics:     testMetricsVec(),
		Logger:      slog.New(slog.DiscardHandler),
	}
}

// newTestSource returns a source over the named watches,
// each with a distinct dispatcher.
func newTestSource(t *testing.T, watches ...string) *Source {
	t.Helper()

	opts := testOptions()
	opts.Config.Watches = nil
	opts.Dispatchers = make(map[string]Dispatcher, len(watches))

	for _, ns := range watches {
		opts.Config.Watches = append(opts.Config.Watches, WatchConfig{Namespace: ns})
		opts.Dispatchers[ns] = &eventRecorder{}
	}
	src, err := New(opts)
	if err != nil {
		t.Fatalf("failed to build source: %v", err)
	}
	return src
}
