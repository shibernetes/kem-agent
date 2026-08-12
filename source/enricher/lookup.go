package enricher

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// incompleteRef reports whether a reference is incomplete.
func incompleteRef(ref *corev1.ObjectReference) bool {
	return ref.Kind == "" || ref.Name == ""
}

// storeKey returns the key an informer stores an object under.
func storeKey(ref *corev1.ObjectReference, namespaced bool) string {
	if !namespaced {
		return ref.Name
	}
	return ref.Namespace + "/" + ref.Name
}

// groupKindOf returns the group and kind of an object reference.
// An empty apiVersion and a bare v1 both represent the core group.
//
// The version is dropped, because an event can have one that is older than
// the one the informer is running at, while both of them describe the same
// object. An apiVersion that cannot be parsed is kept as the group.
// No declared resource can match a group in that form, so the reference is
// reported as undeclared rather than resolved to the core group.
func groupKindOf(ref *corev1.ObjectReference) schema.GroupKind {
	gv, err := schema.ParseGroupVersion(ref.APIVersion)
	if err != nil {
		return schema.GroupKind{Group: ref.APIVersion, Kind: ref.Kind}
	}
	return schema.GroupKind{Group: gv.Group, Kind: ref.Kind}
}
