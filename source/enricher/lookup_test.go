package enricher

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestRefIsIncomplete(t *testing.T) {
	t.Parallel()

	cases := map[string]corev1.ObjectReference{
		"no kind":          {Name: "web-0"},
		"no name":          {Kind: "Pod"},
		"neither":          {},
		"namespace alone":  {Namespace: "team-a"},
		"version and kind": {APIVersion: "v1", Kind: "Pod"},
	}
	for name, ref := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if !incompleteRef(&ref) {
				t.Error("the reference was taken, want it reported as incomplete")
			}
		})
	}
}

func TestRefIsComplete(t *testing.T) {
	t.Parallel()

	cases := map[string]corev1.ObjectReference{
		"kind and name":  {Kind: "Pod", Name: "web-0"},
		"no api version": {Kind: "Pod", Name: "web-0", Namespace: "team-a"},
		"everything set": {APIVersion: "v1", Kind: "Pod", Name: "web-0", Namespace: "team-a", UID: "3f2b"},
	}
	for name, ref := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if incompleteRef(&ref) {
				t.Error("the reference was reported as incomplete, want it taken")
			}
		})
	}
}

func TestStoreKey(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		ref        corev1.ObjectReference
		namespaced bool
		want       string
	}{
		"namespaced object": {
			ref:        corev1.ObjectReference{Namespace: "team-a", Name: "web-0"},
			namespaced: true,
			want:       "team-a/web-0",
		},
		"cluster-scoped object": {
			ref:  corev1.ObjectReference{Name: "node-001"},
			want: "node-001",
		},
		"namespace on a cluster-scoped object": {
			ref:  corev1.ObjectReference{Namespace: "team-a", Name: "node-001"},
			want: "node-001",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := storeKey(&tc.ref, tc.namespaced); got != tc.want {
				t.Errorf("storeKey() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGroupKindOf(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		ref  corev1.ObjectReference
		want schema.GroupKind
	}{
		"core group": {
			ref:  corev1.ObjectReference{APIVersion: "v1", Kind: "Pod"},
			want: schema.GroupKind{Kind: "Pod"},
		},
		"no api version": {
			ref:  corev1.ObjectReference{Kind: "Pod"},
			want: schema.GroupKind{Kind: "Pod"},
		},
		"bare slash": {
			ref:  corev1.ObjectReference{APIVersion: "/", Kind: "Pod"},
			want: schema.GroupKind{Kind: "Pod"},
		},
		"named group": {
			ref:  corev1.ObjectReference{APIVersion: "apps/v1", Kind: "Deployment"},
			want: schema.GroupKind{Group: "apps", Kind: "Deployment"},
		},
		"older version of group": {
			ref:  corev1.ObjectReference{APIVersion: "apps/v1beta1", Kind: "Deployment"},
			want: schema.GroupKind{Group: "apps", Kind: "Deployment"},
		},
		"unparsable api version": {
			ref:  corev1.ObjectReference{APIVersion: "a/b/c", Kind: "Secret"},
			want: schema.GroupKind{Group: "a/b/c", Kind: "Secret"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := groupKindOf(&tc.ref); got != tc.want {
				t.Errorf("groupKindOf() = %s, want %s", got, tc.want)
			}
		})
	}
}
