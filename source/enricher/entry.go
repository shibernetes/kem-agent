package enricher

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/source/sanitizer"
)

// entry is an informer cached entry for a regarding object. It embeds
// ObjectMeta so the informer can key it by namespace and name.
type entry struct {
	metav1.ObjectMeta
	object *event.RegardingObject
}

// transformer builds the cached form of an object.
type transformer struct {
	labels      Allowlist
	annotations Allowlist
	sanitizers  []sanitizer.MetadataSanitizer
	mapper      meta.RESTMapper
}

// transform implements the cache.TransformFunc an informer applies to
// every object it stores. A replace operation can hand back an object
// already cached, which must be returned untouched.
func (t *transformer) transform(obj any) (any, error) {
	switch o := obj.(type) {
	case *entry:
		return o, nil
	case *metav1.PartialObjectMetadata:
		return t.project(o), nil
	default:
		return nil, fmt.Errorf("cannot enrich from a %T", obj)
	}
}

// project reduces an object to the metadata an event carries. The UID is
// kept so a reused name is not mistaken for the object an event named.
func (t *transformer) project(obj *metav1.PartialObjectMetadata) *entry {
	object := &event.RegardingObject{
		Labels:      t.labels.apply(obj.Labels),
		Annotations: t.annotations.apply(obj.Annotations),
		Terminating: obj.DeletionTimestamp != nil,
	}
	// Running after the allowlist bounds one that keeps every key.
	for _, s := range t.sanitizers {
		s.Sanitize(&object.ObjectMeta)
	}
	if owner := metav1.GetControllerOfNoCopy(obj); owner != nil {
		object.Owner = t.ownerRef(owner, obj.Namespace)
	}
	return &entry{
		Name:      obj.Name,
		Namespace: obj.Namespace,
		UID:       obj.UID,
		object:    object,
	}
}

// ownerRef expresses an owner reference as an object reference, the
// form a CEL filter reads regarding and related in.
//
// An owner reference carries no namespace. An owner is either in the
// object's namespace or cluster-scoped, so the namespace is set for a
// namespaced owner alone.
func (t *transformer) ownerRef(owner *metav1.OwnerReference, namespace string) *corev1.ObjectReference {
	ref := &corev1.ObjectReference{
		Kind:       owner.Kind,
		Name:       owner.Name,
		UID:        owner.UID,
		APIVersion: owner.APIVersion,
	}
	if t.isNamespaced(owner.APIVersion, owner.Kind) {
		ref.Namespace = namespace
	}
	return ref
}

// isNamespaced reports whether a kind is namespace-scoped.
// An unresolvable kind returns false, leaving the owner without
// a namespace it may not be in.
func (t *transformer) isNamespaced(apiVersion, kind string) bool {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return false
	}
	mapping, err := t.mapper.RESTMapping(gv.WithKind(kind).GroupKind(), gv.Version)
	if err != nil {
		return false
	}
	return mapping.Scope.Name() == meta.RESTScopeNameNamespace
}
