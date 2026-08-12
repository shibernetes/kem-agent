package enricher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	metadatafake "k8s.io/client-go/metadata/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/shibernetes/kem-agent/event"
)

func TestNewSkipsUnresolvedResources(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{"pods", "widgets.acme.io"}, []string{"team-a"})

	if n := len(h.cache.resources); n != 1 {
		t.Errorf("got %d resources, want only the one that resolved", n)
	}
	if msgs := h.logs.messages(); len(msgs) != 1 {
		t.Fatalf("got %d log records, want one for the resource skipped", len(msgs))
	}
	if got := h.logs.attr(0, "resource"); got != "widgets.acme.io" {
		t.Errorf("got resource %q, want %q", got, "widgets.acme.io")
	}
}

func TestNewReportsTwoNamesForOneKind(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Resources = []string{"pods", "pod"}

	_, err := New(context.Background(), cfg, Options{
		Client:  testMetadataClient(t),
		Mapper:  testMapper(),
		Watches: []string{"team-a"},
		Logger:  slog.New(slog.DiscardHandler),
	})
	if err == nil {
		t.Error("the cache was built, want the second declaration reported")
	}
}

func TestStartFillsCaches(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{"pods"}, []string{"team-a"}, testPod("team-a", "web-0", "3f2b"))
	h.start(t)

	// The number of objects is logged for each informer, so the size
	// of a cache is known at startup rather than through an OOMKill.
	if msgs := h.logs.messages(); len(msgs) != 1 {
		t.Fatalf("got %d log records, want one for each informer", len(msgs))
	}
	if got := h.logs.attr(0, "objects"); got != "1" {
		t.Errorf("got %q objects, want 1", got)
	}
}

func TestStartWithNothingDeclared(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil, []string{"team-a"})
	h.start(t)

	if msgs := h.logs.messages(); len(msgs) != 0 {
		t.Errorf("got %v logged, want nothing", msgs)
	}
}

func TestStartStopsWithContext(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{"pods"}, []string{"team-a"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := h.cache.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("got error %v, want context.Canceled", err)
	}
}

func TestStartReportsCachesThatDoNotFill(t *testing.T) {
	t.Parallel()

	client := testMetadataClient(t)
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("unavailable")
	})
	c := newCache(t, []string{"pods"}, []string{"team-a"}, client, newMetricsRecorder(), &logRecorder{})
	c.syncTimeout = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	err := c.Start(ctx)
	if err == nil {
		t.Fatal("the caches filled, want the failure reported")
	}
	// The error names the resource and namespace a permission is granted for.
	if want := "pods in team-a"; !strings.Contains(err.Error(), want) {
		t.Errorf("got error %v, want it to name %q", err, want)
	}
}

func TestEnrichAttachesMetadata(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{"pods"}, []string{"team-a"}, testPod("team-a", "web-0", "3f2b"))
	h.start(t)

	ev := testEvent("team-a", corev1.ObjectReference{
		APIVersion: "v1",
		Kind:       "Pod",
		Namespace:  "team-a",
		Name:       "web-0",
		UID:        "3f2b",
	})
	h.cache.Enrich(context.Background(), "team-a", ev)

	if ev.RegardingObject == nil {
		t.Fatal("got no metadata, want the pod attached")
	}
	if want := map[string]string{"app": "web"}; !maps.Equal(ev.RegardingObject.Labels, want) {
		t.Errorf("got labels %v, want %v", ev.RegardingObject.Labels, want)
	}
}

func TestEnrichAttachesClusterScopedMetadata(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{"nodes"}, []string{""}, testNode("node-001"))
	h.start(t)

	ev := testEvent("default", corev1.ObjectReference{APIVersion: "v1", Kind: "Node", Name: "node-001"})
	h.cache.Enrich(context.Background(), "", ev)

	if ev.RegardingObject == nil {
		t.Fatal("got no metadata, want the node attached")
	}
	if want := map[string]string{"zone": "eu"}; !maps.Equal(ev.RegardingObject.Labels, want) {
		t.Errorf("got labels %v, want %v", ev.RegardingObject.Labels, want)
	}
}

func TestEnrichCountsEveryOutcome(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		event *event.Event
		want  map[string]float64
	}{
		"the object is cached": {
			event: testEvent("team-a", corev1.ObjectReference{APIVersion: "v1", Kind: "Pod", Namespace: "team-a", Name: "web-0"}),
			want:  map[string]float64{"hit": 1},
		},
		"no object carries the name": {
			event: testEvent("team-a", corev1.ObjectReference{APIVersion: "v1", Kind: "Pod", Namespace: "team-a", Name: "web-9"}),
			want:  map[string]float64{"miss": 1},
		},
		"the kind is not declared": {
			event: testEvent("team-a", corev1.ObjectReference{APIVersion: "v1", Kind: "Secret", Namespace: "team-a", Name: "db"}),
			want:  map[string]float64{"undeclared": 1},
		},
		"the object is in another namespace": {
			event: testEvent("team-a", corev1.ObjectReference{APIVersion: "v1", Kind: "Pod", Namespace: "team-b", Name: "web-0"}),
			want:  map[string]float64{"crossNamespace": 1},
		},
		"the reference names no object": {
			event: testEvent("team-a", corev1.ObjectReference{APIVersion: "v1", Kind: "Pod", Namespace: "team-a"}),
			want:  map[string]float64{"skipped": 1},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, []string{"pods"}, []string{"team-a"}, testPod("team-a", "web-0", "3f2b"))
			h.start(t)
			h.cache.Enrich(context.Background(), "team-a", tc.event)

			if diff := cmp.Diff(tc.want, h.metrics.counts()); diff != "" {
				t.Errorf("the outcomes differ (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEnrichCountsNothingWhenOff(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil, []string{"team-a"})
	h.start(t)

	ev := testEvent("team-a", corev1.ObjectReference{APIVersion: "v1", Kind: "Pod", Namespace: "team-a", Name: "web-0"})
	h.cache.Enrich(context.Background(), "team-a", ev)

	if got := h.metrics.counts(); len(got) != 0 {
		t.Errorf("got %v, want nothing counted", got)
	}
}

func TestEnrichRejectsAnotherNamespace(t *testing.T) {
	t.Parallel()

	// One informer holds every namespace, so the pod is cached and only
	// the namespace check keeps it from being read.
	h := newHarness(t, []string{"pods"}, []string{""}, testPod("team-b", "web-0", "3f2b"))
	h.start(t)
	h.logs.reset()

	ev := testEvent("team-a", corev1.ObjectReference{
		APIVersion: "v1",
		Kind:       "Pod",
		Namespace:  "team-b",
		Name:       "web-0",
		UID:        "3f2b",
	})
	h.cache.Enrich(context.Background(), "", ev)

	if ev.RegardingObject != nil {
		t.Error("the event was enriched, want the lookup refused")
	}
	if msgs := h.logs.messages(); len(msgs) != 1 {
		t.Fatalf("got %d log records, want one for the refusal", len(msgs))
	}
	if got := h.logs.attr(0, "namespace"); got != "team-a" {
		t.Errorf("got namespace %q, want %q", got, "team-a")
	}
	if got := h.logs.attr(0, "regardingNamespace"); got != "team-b" {
		t.Errorf("got regarding namespace %q, want %q", got, "team-b")
	}
}

func TestEnrichWarnsOncePerKind(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{"pods"}, []string{"team-a"})
	h.start(t)
	h.logs.reset()

	for range 3 {
		h.cache.Enrich(context.Background(), "team-a", undeclaredEvent("Secret"))
	}
	h.cache.Enrich(context.Background(), "team-a", undeclaredEvent("ConfigMap"))

	// The log is bounded and the metric is not, so a repeating
	// kind stays visible without filling the log it repeats in.
	if msgs := h.logs.messages(); len(msgs) != 2 {
		t.Errorf("got %d log records, want one for each kind", len(msgs))
	}
	if got, want := h.metrics.counts()["undeclared"], float64(4); got != want {
		t.Errorf("got %v undeclared, want %v", got, want)
	}
}

func TestEnrichWarnsUpToTheCap(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{"pods"}, []string{"team-a"})
	h.start(t)
	h.logs.reset()

	for i := range maxWarnedKeys + 10 {
		h.cache.Enrich(context.Background(), "team-a", undeclaredEvent(fmt.Sprintf("Widget%d", i)))
	}
	if got := len(h.logs.messages()); got != maxWarnedKeys {
		t.Errorf("got %d log records, want %d", got, maxWarnedKeys)
	}
}

func TestEnrichBoundsWhatTheWarnRemembers(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{"pods"}, []string{"team-a"})

	h.start(t)
	h.logs.reset()
	h.cache.Enrich(context.Background(), "team-a", undeclaredEvent(strings.Repeat("W", maxKeyBytes+100)))

	if got := len(h.logs.attr(0, "kind")); got != maxKeyBytes {
		t.Errorf("got a kind of %d bytes, want %d", got, maxKeyBytes)
	}
}

func TestTruncateKeepsShortValues(t *testing.T) {
	t.Parallel()

	s := strings.Repeat("a", maxKeyBytes)

	if got := truncate(s); got != s {
		t.Errorf("got %d bytes, want the value kept whole", len(got))
	}
}

func TestTruncateCopies(t *testing.T) {
	t.Parallel()

	s := strings.Repeat("a", maxKeyBytes+1)

	if got := truncate(s); unsafe.StringData(got) == unsafe.StringData(s) {
		t.Error("the value shares the backing array, want a copy")
	}
}

func TestTruncateCutsOnRuneBoundary(t *testing.T) {
	t.Parallel()

	s := strings.Repeat("a", maxKeyBytes-1) + "é" + strings.Repeat("b", 10)

	// The limit falls inside the two bytes of the last rune,
	// which must be dropped rather than halved.
	got := truncate(s)
	if len(got) > maxKeyBytes {
		t.Errorf("got %d bytes, want at most %d", len(got), maxKeyBytes)
	}
	if !utf8.ValidString(got) {
		t.Errorf("got %q, want valid UTF-8", got)
	}
}

func newCache(t *testing.T, resources, watches []string, client *metadatafake.FakeMetadataClient, metrics *metricsRecorder, logs *logRecorder) *Cache {
	t.Helper()

	cfg := DefaultConfig()
	cfg.Resources = resources

	c, err := New(context.Background(), cfg, Options{
		Client:  client,
		Mapper:  testMapper(),
		Watches: watches,
		Metrics: metrics.metrics(),
		Logger:  logs.logger(),
	})
	if err != nil {
		t.Fatalf("failed to build cache: %v", err)
	}
	return c
}

type harness struct {
	cache   *Cache
	metrics *metricsRecorder
	logs    *logRecorder
}

func newHarness(t *testing.T, resources, watches []string, objects ...runtime.Object) *harness {
	t.Helper()

	var (
		metrics = newMetricsRecorder()
		logs    = &logRecorder{}
	)
	return &harness{
		cache:   newCache(t, resources, watches, testMetadataClient(t, objects...), metrics, logs),
		metrics: metrics,
		logs:    logs,
	}
}

func (h *harness) start(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := h.cache.Start(ctx); err != nil {
		t.Fatalf("failed to start cache: %v", err)
	}
}

func testEvent(namespace string, ref corev1.ObjectReference) *event.Event {
	return &event.Event{Namespace: namespace, Regarding: ref}
}

func undeclaredEvent(kind string) *event.Event {
	return testEvent("team-a", corev1.ObjectReference{
		APIVersion: "v1",
		Kind:       kind,
		Namespace:  "team-a",
		Name:       "one",
	})
}
