package enricher

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	discoveryfake "k8s.io/client-go/discovery/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

func TestNewRESTMapper(t *testing.T) {
	t.Parallel()

	mapper, err := NewRESTMapper(context.Background(), testDiscovery())
	if err != nil {
		t.Fatalf("failed to build mapper: %v", err)
	}
	gvk, err := mapper.KindFor(schema.GroupVersionResource{Resource: "pods"})
	if err != nil {
		t.Fatalf("failed to resolve pods: %v", err)
	}
	if want := (schema.GroupVersionKind{Version: "v1", Kind: "Pod"}); gvk != want {
		t.Errorf("got %s, want %s", gvk, want)
	}
}

func TestNewRESTMapperStopsWithContext(t *testing.T) {
	t.Parallel()

	client := blockingDiscovery{done: make(chan struct{})}

	t.Cleanup(func() {
		close(client.done)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := NewRESTMapper(ctx, client); !errors.Is(err, context.Canceled) {
		t.Errorf("got error %v, want it canceled", err)
	}
}

func TestNewRESTMapperReturnsDiscoveryError(t *testing.T) {
	t.Parallel()

	if _, err := NewRESTMapper(context.Background(), failingDiscovery{}); err == nil {
		t.Error("the mapper was built, want the discovery error returned")
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		name           string
		want           schema.GroupVersionResource
		wantKind       string
		wantNamespaced bool
	}{
		"core resource": {
			name:           "pods",
			want:           schema.GroupVersionResource{Version: "v1", Resource: "pods"},
			wantKind:       "Pod",
			wantNamespaced: true,
		},
		"cluster-scoped resource": {
			name:     "nodes",
			want:     schema.GroupVersionResource{Version: "v1", Resource: "nodes"},
			wantKind: "Node",
		},
		"group serving two versions": {
			name:           "deployments.apps",
			want:           schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
			wantKind:       "Deployment",
			wantNamespaced: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r, err := resolve(testMapper(), tc.name)
			if err != nil {
				t.Fatalf("failed to resolve %q: %v", tc.name, err)
			}
			if r.gvr != tc.want {
				t.Errorf("got %s, want %s", r.gvr, tc.want)
			}
			if want := (schema.GroupKind{Group: tc.want.Group, Kind: tc.wantKind}); r.kind != want {
				t.Errorf("got kind %s, want %s", r.kind, want)
			}
			if r.namespaced != tc.wantNamespaced {
				t.Errorf("%q resolved to the wrong scope", tc.name)
			}
		})
	}
}

func TestResolveReportsUnknownResources(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"resource in unknown group": "widgets",
		"unavailable group":         "pods.acme.io",
		"subresource":               "pods/log",
	}
	for name, resource := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := resolve(testMapper(), resource); err == nil {
				t.Errorf("%q resolved, want it reported as unknown", resource)
			}
		})
	}
}

func TestBuildInformers(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		resource string
		watches  []string
		want     []string
	}{
		"namespaced resource, one per watch": {
			resource: "pods",
			watches:  []string{"team-a", "team-b", "platform"},
			want:     []string{"platform", "team-a", "team-b"},
		},
		"namespaced resource, all namespaces": {
			resource: "pods",
			watches:  []string{""},
			want:     []string{""},
		},
		"cluster-scoped resource, one for every watch": {
			resource: "nodes",
			watches:  []string{"team-a", "team-b", "platform"},
			want:     []string{""},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r, err := resolve(testMapper(), tc.resource)
			if err != nil {
				t.Fatalf("failed to resolve %q: %v", tc.resource, err)
			}
			tr := &transformer{mapper: testMapper()}
			if err := r.build(testMetadataClient(t), tc.watches, tr); err != nil {
				t.Fatalf("failed to build informers: %v", err)
			}
			if got := slices.Sorted(maps.Keys(r.informers)); !slices.Equal(got, tc.want) {
				t.Errorf("got informers for %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResourceLookupFindsObject(t *testing.T) {
	t.Parallel()

	r := startResource(t, "pods", []string{"team-a"}, testPod("team-a", "web-0", "3f2b"))

	ref := corev1.ObjectReference{
		Kind:      "Pod",
		Namespace: "team-a",
		Name:      "web-0",
		UID:       "3f2b",
	}
	e := r.lookup("team-a", &ref)
	if e == nil {
		t.Fatal("got nothing, want the cached pod")
	}
	if want := map[string]string{"app": "web"}; !maps.Equal(e.object.Labels, want) {
		t.Errorf("got labels %v, want %v", e.object.Labels, want)
	}
}

func TestResourceLookupFindsClusterScopedObject(t *testing.T) {
	t.Parallel()

	r := startResource(t, "nodes", []string{"team-a", "team-b"}, testNode("node-001"))

	ref := corev1.ObjectReference{
		Kind: "Node",
		Name: "node-001",
	}
	for _, watch := range []string{"team-a", "team-b"} {
		if e := r.lookup(watch, &ref); e == nil {
			t.Errorf("got nothing from watch %q, want the cached node", watch)
		}
	}
}

func TestResourceLookupMisses(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		watch string
		ref   corev1.ObjectReference
	}{
		"unknown object name": {
			watch: "team-a",
			ref:   corev1.ObjectReference{Kind: "Pod", Namespace: "team-a", Name: "web-9"},
		},
		"object not cached by informer": {
			watch: "team-a",
			ref:   corev1.ObjectReference{Kind: "Pod", Namespace: "team-b", Name: "web-0"},
		},
		"watch with no informer": {
			watch: "platform",
			ref:   corev1.ObjectReference{Kind: "Pod", Namespace: "team-a", Name: "web-0"},
		},
		"name reused by another object": {
			watch: "team-a",
			ref:   corev1.ObjectReference{Kind: "Pod", Namespace: "team-a", Name: "web-0", UID: "9c1d"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := startResource(t, "pods", []string{"team-a"}, testPod("team-a", "web-0", "3f2b"))

			if e := r.lookup(tc.watch, &tc.ref); e != nil {
				t.Errorf("got %s/%s, want nothing", e.Namespace, e.Name)
			}
		})
	}
}

func TestResourceLookupIgnoresUnsetUID(t *testing.T) {
	t.Parallel()

	r := startResource(t, "pods", []string{"team-a"}, testPod("team-a", "web-0", "3f2b"))

	// A reference that has no identifier cannot be told apart
	// from the object it refers to, so the name is used alone.
	ref := corev1.ObjectReference{Kind: "Pod", Namespace: "team-a", Name: "web-0"}

	if e := r.lookup("team-a", &ref); e == nil {
		t.Fatal("got nothing, want the cached pod")
	}
}

func TestResourceName(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		resource  string
		namespace string
		want      string
	}{
		"namespaced informer":            {resource: "pods", namespace: "team-a", want: "pods in team-a"},
		"informer over every namespace":  {resource: "pods", want: "pods"},
		"cluster-scoped informer":        {resource: "nodes", want: "nodes"},
		"informer of a grouped resource": {resource: "deployments.apps", namespace: "team-a", want: "deployments.apps in team-a"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r, err := resolve(testMapper(), tc.resource)
			if err != nil {
				t.Fatalf("failed to resolve %q: %v", tc.resource, err)
			}
			if got := r.name(tc.namespace); got != tc.want {
				t.Errorf("name(%q) = %q, want %q", tc.namespace, got, tc.want)
			}
		})
	}
}

func TestSuggestResourceSuggests(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		name string
		want string
	}{
		"typo in resource name":        {name: "pdos", want: "pods"},
		"typo in group name":           {name: "deployments.app", want: "deployments.apps"},
		"singular resource":            {name: "pod", want: "pods"},
		"singular resource with group": {name: "deployment.apps", want: "deployments.apps"},
		"two typos":                    {name: "deploymnet.apps", want: "deployments.apps"},
		"four letters, two edits":      {name: "pdox", want: "pods"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := SuggestResource(tc.name)
			if !ok {
				t.Fatalf("SuggestResource(%q) suggested nothing, want %q", tc.name, tc.want)
			}
			if got != tc.want {
				t.Errorf("SuggestResource(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestSuggestResourceIgnores(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"known resource":               "pods",
		"known group":                  "deployments.apps",
		"custom resource":              "applications.argoproj.io",
		"short name needing two edits": "pdo",
	}
	for name, resource := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got, ok := SuggestResource(resource); ok {
				t.Errorf("SuggestResource(%q) = %q, want no suggestion", resource, got)
			}
		})
	}
}

func TestSkipKindKeeps(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"core kind":    "Pod",
		"grouped kind": "Deployment",
		"custom kind":  "Application",
	}
	for name, kind := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if skipKind(kind) {
				t.Errorf("kind %q was skipped, want it kept", kind)
			}
		})
	}
}

func TestSkipKindSkips(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"list":         "PodList",
		"options type": "ListOptions",
		"watch event":  "WatchEvent",
		"status":       "Status",
	}
	for name, kind := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if !skipKind(kind) {
				t.Errorf("kind %q was kept, want it skipped", kind)
			}
		})
	}
}

func TestKnownResourcesHasBuiltins(t *testing.T) {
	t.Parallel()

	resources := knownResources()

	for _, want := range []string{
		"pods",
		"nodes",
		"endpoints",
		"deployments.apps",
		"networkpolicies.networking.k8s.io",
		"ingresses.networking.k8s.io",
	} {
		if !slices.Contains(resources, want) {
			t.Errorf("got no resource %q, want it derived from the built-in types", want)
		}
	}
	for _, unwanted := range []string{
		"podlists",
		"listoptionses",
		"statuses",
		"watchevents",
	} {
		if slices.Contains(resources, unwanted) {
			t.Errorf("got resource %q, want it filtered out", unwanted)
		}
	}
}

func TestKnownResourcesAreSorted(t *testing.T) {
	t.Parallel()

	// Two candidates can sit at the same distance, so the order
	// they are compared in is what decides which one is suggested.
	if !slices.IsSorted(knownResources()) {
		t.Error("the known resources are unsorted, so a tie between two suggestions is unsettled")
	}
}

func TestDistance(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		a    string
		b    string
		want int
	}{
		"identical":              {a: "pods", b: "pods", want: 0},
		"transposed letters":     {a: "pdos", b: "pods", want: 1},
		"one letter short":       {a: "pod", b: "pods", want: 1},
		"three edits apart":      {a: "kitten", b: "sitting", want: 3},
		"empty against a word":   {a: "", b: "pods", want: 4},
		"word against empty":     {a: "pods", b: "", want: 4},
		"typo group name":        {a: "deployments.app", b: "deployments.apps", want: 1},
		"overlapping transposed": {a: "ca", b: "abc", want: 3},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := distance(tc.a, tc.b); got != tc.want {
				t.Errorf("distance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// testDiscovery returns a discovery client serving one namespaced
// and one cluster-scoped resource of the core group.
func testDiscovery() discovery.DiscoveryInterfaceWithContext {
	return &discoveryfake.FakeDiscovery{
		Fake: &k8stesting.Fake{
			Resources: []*metav1.APIResourceList{
				{
					GroupVersion: "v1",
					APIResources: []metav1.APIResource{
						{Name: "pods", SingularName: "pod", Namespaced: true, Kind: "Pod"},
						{Name: "nodes", SingularName: "node", Kind: "Node"},
					},
				},
			},
		},
	}
}

// startResource builds the informers of a resource using the given
// objects and waits for their caches to fill.
func startResource(t *testing.T, name string, watches []string, objects ...runtime.Object) *resource {
	t.Helper()

	r, err := resolve(testMapper(), name)
	if err != nil {
		t.Fatalf("failed to resolve %q: %v", name, err)
	}
	tr := &transformer{
		labels: Allowlist{Enabled: true},
		mapper: testMapper(),
	}
	if err := r.build(testMetadataClient(t, objects...), watches, tr); err != nil {
		t.Fatalf("failed to build informers: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	for _, informer := range r.informers {
		go informer.RunWithContext(ctx)

		if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
			t.Fatal("the informer cache did not fill")
		}
	}
	return r
}
