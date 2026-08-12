package enricher

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	metadatafake "k8s.io/client-go/metadata/fake"
	"k8s.io/client-go/restmapper"
)

type blockingDiscovery struct {
	discovery.DiscoveryInterfaceWithContext
	done chan struct{}
}

func (d blockingDiscovery) ServerGroupsAndResourcesWithContext(ctx context.Context) ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-d.done:
		return nil, nil, nil
	}
}

type failingDiscovery struct {
	discovery.DiscoveryInterfaceWithContext
}

func (failingDiscovery) ServerGroupsAndResourcesWithContext(context.Context) ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return nil, nil, errors.New("unavailable")
}

// fakeCounter mocks a Prometheus counter, which reports what
// was added to it only through a registry.
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

// metricsRecorder holds the instruments a cache records into.
type metricsRecorder struct {
	hit            *fakeCounter
	miss           *fakeCounter
	undeclared     *fakeCounter
	crossNamespace *fakeCounter
	skipped        *fakeCounter
}

func newMetricsRecorder() *metricsRecorder {
	return &metricsRecorder{
		hit:            &fakeCounter{},
		miss:           &fakeCounter{},
		undeclared:     &fakeCounter{},
		crossNamespace: &fakeCounter{},
		skipped:        &fakeCounter{},
	}
}

func (r *metricsRecorder) metrics() Metrics {
	return Metrics{
		Hit:            r.hit,
		Miss:           r.miss,
		Undeclared:     r.undeclared,
		CrossNamespace: r.crossNamespace,
		Skipped:        r.skipped,
	}
}

func (r *metricsRecorder) counts() map[string]float64 {
	counts := make(map[string]float64)

	for name, c := range map[string]*fakeCounter{
		"hit":            r.hit,
		"miss":           r.miss,
		"undeclared":     r.undeclared,
		"crossNamespace": r.crossNamespace,
		"skipped":        r.skipped,
	} {
		if n := c.get(); n > 0 {
			counts[name] = n
		}
	}
	return counts
}

// logRecorder captures what a cache logs.
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

// attr returns the value of an attribute of the record at index i.
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

func (h *logRecorder) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.records = nil
}

func testMetadataClient(t *testing.T, objects ...runtime.Object) *metadatafake.FakeMetadataClient {
	t.Helper()

	scheme := metadatafake.NewTestScheme()
	if err := metav1.AddMetaToScheme(scheme); err != nil {
		t.Fatalf("failed to build scheme: %v", err)
	}
	return metadatafake.NewSimpleMetadataClient(scheme, objects...)
}

// testMapper returns a mapper over a fixed set of API groups.
// It contains a namespaced and a cluster-scoped resource in the
// core group, and a group serving two versions.
func testMapper() meta.RESTMapper {
	return restmapper.NewDiscoveryRESTMapper([]*restmapper.APIGroupResources{
		{
			Group: metav1.APIGroup{
				Versions:         []metav1.GroupVersionForDiscovery{{Version: "v1"}},
				PreferredVersion: metav1.GroupVersionForDiscovery{Version: "v1"},
			},
			VersionedResources: map[string][]metav1.APIResource{
				"v1": {
					{Name: "pods", SingularName: "pod", Namespaced: true, Kind: "Pod"},
					{Name: "nodes", SingularName: "node", Kind: "Node"},
				},
			},
		},
		{
			Group: metav1.APIGroup{
				Name:             "apps",
				Versions:         []metav1.GroupVersionForDiscovery{{Version: "v1"}, {Version: "v1beta1"}},
				PreferredVersion: metav1.GroupVersionForDiscovery{Version: "v1"},
			},
			VersionedResources: map[string][]metav1.APIResource{
				"v1": {
					{Name: "deployments", SingularName: "deployment", Namespaced: true, Kind: "Deployment"},
					{Name: "replicasets", SingularName: "replicaset", Namespaced: true, Kind: "ReplicaSet"},
				},
				"v1beta1": {
					{Name: "deployments", SingularName: "deployment", Namespaced: true, Kind: "Deployment"},
				},
			},
		},
	})
}

func testPod(namespace, name string, uid types.UID) *metav1.PartialObjectMetadata {
	return &metav1.PartialObjectMetadata{
		APIVersion: "v1",
		Kind:       "Pod",
		Name:       name,
		Namespace:  namespace,
		UID:        uid,
		Labels:     map[string]string{"app": "web"},
	}
}

func testNode(name string) *metav1.PartialObjectMetadata {
	return &metav1.PartialObjectMetadata{
		APIVersion: "v1",
		Kind:       "Node",
		Name:       name,
		Labels:     map[string]string{"zone": "eu"},
	}
}
