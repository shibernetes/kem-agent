package enricher

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/cache"
)

const (
	shortResourceName = 4
	maxDistance       = 2
	maxShortDistance  = 1
)

var knownResources = sync.OnceValue(buildKnownResources)

// resource is a declared resource resolved to its preferred version and
// scope. Its informers are keyed by the namespace they list in, which
// is empty for an all-namespaces watch and for a cluster-scoped resource.
type resource struct {
	kind       schema.GroupKind
	gvr        schema.GroupVersionResource
	namespaced bool
	informers  map[string]cache.SharedIndexInformer
}

// NewRESTMapper returns a mapper that resolves a resource to its kind,
// its preferred version and its scope. Discovery is read once, so a
// resource that is installed later is not picked up until the agent is
// restarted.
//
// The mapper itself reads nothing further, so only the discovery call
// observes the context.
func NewRESTMapper(ctx context.Context, client discovery.DiscoveryInterfaceWithContext) (meta.RESTMapper, error) {
	grs, err := restmapper.GetAPIGroupResourcesWithContext(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to get API resources: %w", err)
	}
	return restmapper.NewDiscoveryRESTMapper(grs), nil
}

// resolve reads a declared name as a resource and a group, and resolves
// the kind, group-version-resource and scope through the mapper.
// The name carries no version, so the group's preferred one is taken.
func resolve(mapper meta.RESTMapper, name string) (*resource, error) {
	gvk, err := mapper.KindFor(schema.ParseGroupResource(name).WithVersion(""))
	if err != nil {
		return nil, err
	}
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, err
	}
	return &resource{
		kind:       gvk.GroupKind(),
		gvr:        mapping.Resource,
		namespaced: mapping.Scope.Name() == meta.RESTScopeNameNamespace,
	}, nil
}

// build creates the informers for the resource, one per watch for a
// namespaced one, or a single informer that serves every watch for a
// cluster-scoped resource.
func (r *resource) build(client metadata.Interface, watches []string, t *transformer) error {
	namespaces := watches
	if !r.namespaced {
		namespaces = []string{metav1.NamespaceAll}
	}
	r.informers = make(map[string]cache.SharedIndexInformer, len(namespaces))

	for _, ns := range namespaces {
		informer := metadatainformer.NewFilteredMetadataInformer(
			client, r.gvr, ns, 0, cache.Indexers{}, nil,
		).Informer()

		if err := informer.SetTransform(t.transform); err != nil {
			return fmt.Errorf("failed to set the transform func for %s: %w", r.gvr, err)
		}
		r.informers[ns] = informer
	}
	return nil
}

// lookup returns what a cache holds for the object a reference names,
// or nil when no cache holds a matching one.
func (r *resource) lookup(watch string, ref *corev1.ObjectReference) *entry {
	key := watch
	if !r.namespaced {
		key = metav1.NamespaceAll
	}
	informer, ok := r.informers[key]
	if !ok {
		return nil
	}
	obj, found, err := informer.GetIndexer().GetByKey(storeKey(ref, r.namespaced))
	if err != nil || !found {
		return nil
	}
	e, ok := obj.(*entry)
	if !ok {
		return nil
	}
	// A name can be reused after an object is deleted.
	// Enriching from the object that reused the name would report the
	// metadata of a different object.
	if ref.UID != "" && e.UID != ref.UID {
		return nil
	}
	return e
}

func (r *resource) name(namespace string) string {
	if namespace == metav1.NamespaceAll {
		return r.gvr.GroupResource().String()
	}
	return fmt.Sprintf("%s in %s", r.gvr.GroupResource(), namespace)
}

// SuggestResource returns the resource a declared name most likely meant, out
// of the ones the compiled-in API types describe. It reports nothing when the
// name is already one of them, and nothing when it resembles none.
// A custom resource is unknown, whether it is correct or not.
//
// The name is expected to have been validated by [Config.Validate], so the
// resource is a valid DNS-1035 label and its group a DNS-1123 subdomain.
func SuggestResource(name string) (string, bool) {
	resources := knownResources()
	if slices.Contains(resources, name) {
		return "", false
	}
	limit := maxDistance
	if len(name) < shortResourceName {
		limit = maxShortDistance
	}
	var (
		best    string
		nearest = limit + 1
	)
	for _, candidate := range resources {
		if d := distance(name, candidate); d < nearest {
			best, nearest = candidate, d
		}
	}
	return best, best != ""
}

// buildKnownResources guesses the resource each registered kind lives under.
func buildKnownResources() []string {
	var (
		all   = scheme.Scheme.AllKnownTypes()
		names = make(map[string]struct{}, len(all))
	)
	for gvk := range all {
		if skipKind(gvk.Kind) {
			continue
		}
		plural, _ := meta.UnsafeGuessKindToResource(gvk)
		names[plural.GroupResource().String()] = struct{}{}
	}
	return slices.Sorted(maps.Keys(names))
}

// skipKind reports whether a registered kind describes something
// other than a resource.
func skipKind(kind string) bool {
	return strings.HasSuffix(kind, "List") || strings.HasSuffix(kind, "Options") || kind == "WatchEvent" || kind == "Status"
}

// distance returns the Damerau-Levenshtein distance between a and b,
// counting a transposition as one edit rather than two.
func distance(a, b string) int {
	if a == "" {
		return len(b)
	}
	if b == "" {
		return len(a)
	}
	// A transposition reaches two rows back, so three rows
	// are kept and rotated rather than the whole matrix.
	var (
		prev2 = make([]int, len(b)+1)
		prev  = make([]int, len(b)+1)
		curr  = make([]int, len(b)+1)
	)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)

			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				curr[j] = min(curr[j], prev2[j-2]+1)
			}
		}
		prev2, prev, curr = prev, curr, prev2
	}
	return prev[len(b)]
}
