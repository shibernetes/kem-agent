package event

import (
	"testing"
)

func TestField(t *testing.T) {
	ev := fullEvent()

	cases := map[string]string{
		"uid":                                    "8f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f",
		"resourceVersion":                        "48291",
		"namespace":                              "team-a",
		"name":                                   "web-7d9f.17c8e2a1b4f5",
		"eventTime":                              "2009-01-03T10:29:15.123456Z",
		"reportingController":                    "kubelet",
		"reportingInstance":                      "kubelet-node-1",
		"action":                                 "Pulling",
		"reason":                                 "BackOff",
		"type":                                   "Warning",
		"labels.app":                             "web",
		"annotations.kubernetes.io/change-cause": "rollout",
		"series.count":                           "7",
		"series.lastObservedTime":                "2009-01-03T10:30:15.123456Z",
		"regarding.kind":                         "Pod",
		"regarding.namespace":                    "team-a",
		"regarding.name":                         "web-7d9f",
		"regarding.uid":                          "1a2b3c4d-5e6f-7a8b-9c0d-1e2f3a4b5c6d",
		"regarding.apiVersion":                   "v1",
		"regarding.resourceVersion":              "48277",
		"regarding.fieldPath":                    "spec.containers{web}",
		"related.name":                           "web-config",
		// enrichment
		"regardingObject.labels.app":                              "web",
		"regardingObject.annotations.cni.projectcalico.org/podIP": "10.1.2.3/32",
		"regardingObject.owner.kind":                              "ReplicaSet",
		"regardingObject.terminating":                             "true",
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := ev.Field(name)
			if !ok {
				t.Fatalf("%q is not addressable", name)
			}
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestFieldAbsent(t *testing.T) {
	var ev Event

	// Absent data reads as the empty string rather than as an
	// unknown name, so a metrics label or a template tag renders
	// empty instead of failing.
	for _, name := range []string{
		"note",
		"eventTime",
		"labels.app",
		"series.count",
		"series.lastObservedTime",
		"regarding.kind",
		"related.name",
		"regardingObject.labels.app",
		"regardingObject.owner.kind",
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := ev.Field(name)
			if !ok {
				t.Fatalf("%q is not addressable", name)
			}
			if got != "" {
				t.Errorf("got %q, want an empty string", got)
			}
		})
	}
}

func TestRefFieldAbsentReference(t *testing.T) {
	// An absent reference resolves every subfield rather than
	// reporting the name unknown, so related and owner read as
	// empty on an event that carries neither.
	for _, name := range []string{
		FieldKind,
		FieldNamespace,
		FieldName,
		FieldUID,
		FieldAPIVersion,
		FieldResourceVersion,
		FieldFieldPath,
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := refField(nil, name)
			if !ok {
				t.Fatalf("%q is not addressable", name)
			}
			if got != "" {
				t.Errorf("got %q, want an empty string", got)
			}
		})
	}
}

func TestFieldEmptyMapKey(t *testing.T) {
	ev := fullEvent()

	// A map prefix with no key is not a name, and the guard
	// that says so is one condition per branch. Without it,
	// the `labels.` prefix resolves to the empty key.
	for _, name := range []string{
		"labels.",
		"annotations.",
		"regardingObject.labels.",
		"regardingObject.annotations.",
	} {
		t.Run(name, func(t *testing.T) {
			if got, ok := ev.Field(name); ok {
				t.Errorf("got %q, want %q to be unaddressable", got, name)
			}
		})
	}
}

func TestFieldPrefixesDoNotOverlap(t *testing.T) {
	ev := fullEvent()

	var (
		regarding, _ = ev.Field("regarding.name")
		enriched, _  = ev.Field("regardingObject.owner.name")
	)
	// The enrichment prefix is longer than the regarding one and
	// shares it, so the two must not be confused for each other.
	if regarding == enriched {
		t.Errorf("got %q for both, want the two prefixes to resolve apart", regarding)
	}
}
