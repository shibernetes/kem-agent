package enricher

import (
	"maps"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shibernetes/kem-agent/source/sanitizer"
)

func TestTransformProjectsObject(t *testing.T) {
	t.Parallel()

	tr := &transformer{labels: Allowlist{Enabled: true}, mapper: testMapper()}

	obj, err := tr.transform(testObject())
	if err != nil {
		t.Fatalf("failed to transform object: %v", err)
	}
	e, ok := obj.(*entry)
	if !ok {
		t.Fatalf("got %T, want an entry", obj)
	}
	// The informer keys an entry by namespace and name, and a
	// lookup differentiates a reused name by the identifier.
	if e.Namespace != "team-a" || e.Name != "web-0" {
		t.Errorf("got %s/%s, want team-a/web-0", e.Namespace, e.Name)
	}
	if e.UID != "3f2b" {
		t.Errorf("got uid %q, want %q", e.UID, "3f2b")
	}
	if e.object == nil {
		t.Error("got no metadata, want it projected")
	}
}

func TestTransformIdempotency(t *testing.T) {
	t.Parallel()

	tr := &transformer{labels: Allowlist{Enabled: true}, mapper: testMapper()}

	first, err := tr.transform(testObject())
	if err != nil {
		t.Fatalf("failed to transform object: %v", err)
	}
	// A replace operation can hand back an entry that is already cached.
	// The delta FIFO queue requires it to be returned unchanged.
	second, err := tr.transform(first)
	if err != nil {
		t.Fatalf("failed to transform entry: %v", err)
	}
	if second != first {
		t.Error("the entry was projected a second time, want it returned as it is")
	}
}

func TestTransformRefusesOtherTypes(t *testing.T) {
	t.Parallel()

	cases := map[string]any{
		"list":   &metav1.PartialObjectMetadataList{},
		"string": "web-0",
		"nil":    nil,
	}
	for name, obj := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tr := &transformer{mapper: testMapper()}

			if _, err := tr.transform(obj); err == nil {
				t.Error("the object was projected, want it refused")
			}
		})
	}
}

func TestProjectAppliesAllowlists(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		labels          Allowlist
		annotations     Allowlist
		wantLabels      map[string]string
		wantAnnotations map[string]string
	}{
		"every label": {
			labels:     Allowlist{Enabled: true},
			wantLabels: map[string]string{"app": "web", "tier": "front", "zone": "eu"},
		},
		"one listed label": {
			labels:     Allowlist{Enabled: true, Keys: []string{"app"}},
			wantLabels: map[string]string{"app": "web"},
		},
		"labels and annotations": {
			labels:          Allowlist{Enabled: true, Keys: []string{"app"}},
			annotations:     Allowlist{Enabled: true},
			wantLabels:      map[string]string{"app": "web"},
			wantAnnotations: map[string]string{"note": "hello"},
		},
		"both empty": {},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tr := &transformer{labels: tc.labels, annotations: tc.annotations, mapper: testMapper()}

			object := tr.project(testObject()).object
			if !maps.Equal(object.Labels, tc.wantLabels) {
				t.Errorf("got labels %v, want %v", object.Labels, tc.wantLabels)
			}
			if !maps.Equal(object.Annotations, tc.wantAnnotations) {
				t.Errorf("got annotations %v, want %v", object.Annotations, tc.wantAnnotations)
			}
		})
	}
}

func TestProjectSanitizesAfterAllowlist(t *testing.T) {
	t.Parallel()

	cfg := sanitizer.DefaultConfig()
	cfg.MetadataCountLimit.MaxLabels = 1

	var (
		tr = &transformer{
			labels:     Allowlist{Enabled: true, Keys: []string{"tier", "zone"}},
			sanitizers: sanitizer.New(cfg).Metadata(),
			mapper:     testMapper(),
		}
		obj = testObject()
	)
	obj.Labels = map[string]string{
		"app":  "web",
		"tier": "front",
		"zone": "eu",
	}
	// The allowlist keeps the labels tier and zone, and the count limit then
	// keeps the first of those two. Running the limit first would keep app
	// instead, which the allowlist would drop, leaving nothing at all.
	want := map[string]string{"tier": "front"}

	if got := tr.project(obj).object.Labels; !maps.Equal(got, want) {
		t.Errorf("got labels %v, want %v", got, want)
	}
}

func TestProjectResolvesOwner(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		owner         metav1.OwnerReference
		wantNamespace string
	}{
		"namespaced owner": {
			owner:         metav1.OwnerReference{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web", UID: "9c1d", Controller: new(true)},
			wantNamespace: "team-a",
		},
		"cluster-scoped owner": {
			owner: metav1.OwnerReference{APIVersion: "v1", Kind: "Node", Name: "node-001", UID: "5ae2", Controller: new(true)},
		},
		"owner unknown to mapper": {
			owner: metav1.OwnerReference{APIVersion: "acme.io/v1", Kind: "Widget", Name: "w-1", UID: "e70b", Controller: new(true)},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tr := &transformer{mapper: testMapper()}

			obj := testObject()
			obj.OwnerReferences = []metav1.OwnerReference{tc.owner}

			owner := tr.project(obj).object.Owner
			if owner == nil {
				t.Fatalf("got no owner, want %s/%s", tc.owner.Kind, tc.owner.Name)
			}
			want := corev1.ObjectReference{
				Kind:       tc.owner.Kind,
				Namespace:  tc.wantNamespace,
				Name:       tc.owner.Name,
				UID:        tc.owner.UID,
				APIVersion: tc.owner.APIVersion,
			}
			if *owner != want {
				t.Errorf("got owner %+v, want %+v", *owner, want)
			}
		})
	}
}

func TestProjectTakesOnlyTheController(t *testing.T) {
	t.Parallel()

	var (
		tr  = &transformer{mapper: testMapper()}
		obj = testObject()
	)
	obj.OwnerReferences = []metav1.OwnerReference{
		{APIVersion: "apps/v1", Kind: "Deployment", Name: "web"},
		{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-7d4", Controller: new(true)},
		{APIVersion: "v1", Kind: "Node", Name: "node-001"},
	}
	owner := tr.project(obj).object.Owner
	if owner == nil {
		t.Fatal("got no owner, want ReplicaSet/web-7d4")
	}
	if owner.Kind != "ReplicaSet" || owner.Name != "web-7d4" {
		t.Errorf("got owner %s/%s, want ReplicaSet/web-7d4", owner.Kind, owner.Name)
	}
}

func TestProjectIgnoresOwnersWithoutController(t *testing.T) {
	t.Parallel()

	var (
		tr  = &transformer{mapper: testMapper()}
		obj = testObject()
	)
	obj.OwnerReferences = []metav1.OwnerReference{
		{APIVersion: "apps/v1", Kind: "Deployment", Name: "web"},
		{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-7d4"},
	}
	if owner := tr.project(obj).object.Owner; owner != nil {
		t.Errorf("got owner %s/%s, want none", owner.Kind, owner.Name)
	}
}

func TestProjectMarksTerminatingObject(t *testing.T) {
	t.Parallel()

	tr := &transformer{mapper: testMapper()}

	obj := testObject()
	if tr.project(obj).object.Terminating {
		t.Error("the object is marked terminating, want it left alone")
	}
	obj.DeletionTimestamp = new(metav1.Now())

	if !tr.project(obj).object.Terminating {
		t.Error("the object is not marked terminating, want it marked")
	}
}

func testObject() *metav1.PartialObjectMetadata {
	return &metav1.PartialObjectMetadata{
		Name:      "web-0",
		Namespace: "team-a",
		UID:       "3f2b",
		Labels: map[string]string{
			"app":  "web",
			"tier": "front",
			"zone": "eu",
		},
		Annotations: map[string]string{
			"note": "hello",
		},
		OwnerReferences: []metav1.OwnerReference{
			{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web", Controller: new(true)},
		},
	}
}
