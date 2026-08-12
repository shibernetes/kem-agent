package filter

import (
	"testing"
	"time"

	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shibernetes/kem-agent/event"
)

func TestObjectRefValGetsFields(t *testing.T) {
	rv := &objectRefVal{ref: fullRef()}

	assertStringGets(t, rv.Get, map[string]string{
		"kind":            "Pod",
		"namespace":       "default",
		"name":            "nginx",
		"uid":             "ref-uid",
		"apiVersion":      "v1",
		"resourceVersion": "69",
		"fieldPath":       "spec",
	})
}

func TestObjectRefValPresence(t *testing.T) {
	var (
		full  = &objectRefVal{ref: fullRef()}
		empty = &objectRefVal{ref: &corev1.ObjectReference{}}
	)
	assertPresence(t, full.IsSet, empty.IsSet,
		"kind",
		"namespace",
		"name",
		"uid",
		"apiVersion",
		"resourceVersion",
		"fieldPath",
	)
}

// TestObjectRefValProtocol covers the [ref.Val] methods.
// References compare by content, so the equal value here is a second
// reference rather than the same one, which is what lets an expression
// compare 'event.regarding' against 'event.related'.
func TestObjectRefValProtocol(t *testing.T) {
	regarding := fullRef()
	assertRefValMethods(t, &objectRefVal{ref: regarding}, objectRefTypeName, regarding,
		&objectRefVal{ref: fullRef()},
		&objectRefVal{ref: &corev1.ObjectReference{}},
	)
}

func TestObjectRefValRejectsBadFields(t *testing.T) {
	rv := &objectRefVal{ref: fullRef()}
	assertFieldErrors(t, rv.Get, rv.IsSet)
}

func TestSeriesValGetsFields(t *testing.T) {
	var (
		series = fullSeries()
		sv     = &seriesVal{s: series}
	)
	if got := sv.Get(types.String("count")); got != types.Int(3) {
		t.Errorf("Get(count) = %v, want 3", got)
	}
	assertTimestampGet(t, sv.Get, "lastObservedTime", series.LastObservedTime.Time)
}

func TestSeriesValPresence(t *testing.T) {
	var (
		full = &seriesVal{s: fullSeries()}
		zero = &seriesVal{s: &eventsv1.EventSeries{}}
	)
	assertPresence(t, full.IsSet, zero.IsSet,
		"count",
		"lastObservedTime",
	)
}

// TestSeriesValProtocol covers the [ref.Val] methods.
// A series compares by content, so two of them are equal when
// their count and last observation time match.
func TestSeriesValProtocol(t *testing.T) {
	series := fullSeries()
	assertRefValMethods(t, &seriesVal{s: series}, seriesTypeName, series,
		&seriesVal{s: fullSeries()},
		&seriesVal{s: &eventsv1.EventSeries{}},
	)
}

func TestSeriesValRejectsBadFields(t *testing.T) {
	sv := &seriesVal{s: fullSeries()}
	assertFieldErrors(t, sv.Get, sv.IsSet)
}

func TestRegardingObjectValGetsFields(t *testing.T) {
	ov := &regardingObjectVal{obj: fullRegardingObject()}

	if got := readLabelByKey(t, ov.Get(types.String("labels")), "team"); got != "platform" {
		t.Errorf("labels[team] = %q, want %q", got, "platform")
	}
	if got := readLabelByKey(t, ov.Get(types.String("annotations")), "owner"); got != "sre" {
		t.Errorf("annotations[owner] = %q, want %q", got, "sre")
	}
	if got := ov.Get(types.String("owner")); !isObjectRef(got) {
		t.Errorf("owner is %T, want an object reference", got)
	}
	if got := ov.Get(types.String("terminating")); got != types.True {
		t.Errorf("Get(terminating) = %v, want true", got)
	}
}

// TestRegardingObjectValNullsAbsentOwner covers an object without
// an owner, such as a Pod created directly rather than by a ReplicaSet.
func TestRegardingObjectValNullsAbsentOwner(t *testing.T) {
	ov := &regardingObjectVal{obj: &event.RegardingObject{}}
	if got := ov.Get(types.String("owner")); got != types.NullValue {
		t.Errorf("owner = %v, want null", got)
	}
}

func TestRegardingObjectValPresence(t *testing.T) {
	var (
		full  = &regardingObjectVal{obj: fullRegardingObject()}
		empty = &regardingObjectVal{obj: &event.RegardingObject{}}
	)
	assertPresence(t, full.IsSet, empty.IsSet,
		"labels",
		"annotations",
		"owner",
		"terminating",
	)
}

// TestRegardingObjectValProtocol covers the [ref.Val] methods.
// Enriched metadata compares by content, labels/annotations maps included.
func TestRegardingObjectValProtocol(t *testing.T) {
	obj := fullRegardingObject()
	assertRefValMethods(t, &regardingObjectVal{obj: obj}, regardingObjectTypeName, obj,
		&regardingObjectVal{obj: fullRegardingObject()},
		&regardingObjectVal{obj: &event.RegardingObject{}},
	)
}

func TestRegardingObjectValRejectsBadFields(t *testing.T) {
	ov := &regardingObjectVal{obj: fullRegardingObject()}
	assertFieldErrors(t, ov.Get, ov.IsSet)
}

// TestValuesAreEqual covers the wrappers that compare by content,
// which is what lets an expression compare two event fields
// describing the same thing.
func TestValuesAreEqual(t *testing.T) {
	var (
		observed  = time.Date(2009, 1, 3, 3, 4, 5, 0, time.UTC)
		elsewhere = observed.In(time.FixedZone("elsewhere", 3600))
	)
	cases := map[string]struct {
		a, b ref.Val
	}{
		"references to the same object": {
			a: objectRef(corev1.ObjectReference{Kind: "Pod", Name: "nginx"}),
			b: objectRef(corev1.ObjectReference{Kind: "Pod", Name: "nginx"}),
		},
		"series with the same count and time": {
			a: eventSeries(3, observed),
			b: eventSeries(3, observed),
		},
		"a series observed at one time in two zones": {
			a: eventSeries(3, observed),
			b: eventSeries(3, elsewhere),
		},
		"enriched metadata with the same content": {
			a: enrichedObject(event.RegardingObject{
				Labels: map[string]string{"team": "platform"},
				Owner:  &corev1.ObjectReference{Kind: "ReplicaSet"},
			}),
			b: enrichedObject(event.RegardingObject{
				Labels: map[string]string{"team": "platform"},
				Owner:  &corev1.ObjectReference{Kind: "ReplicaSet"},
			}),
		},
		"enriched metadata owned by nothing": {
			a: enrichedObject(event.RegardingObject{}),
			b: enrichedObject(event.RegardingObject{}),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.a.Equal(tc.b); got != types.True {
				t.Error("values are different, want the two to be equal")
			}
		})
	}
}

func TestValuesAreNotEqual(t *testing.T) {
	observed := time.Date(2009, 1, 3, 3, 4, 5, 0, time.UTC)

	cases := map[string]struct {
		a, b ref.Val
	}{
		"references to different objects": {
			a: objectRef(corev1.ObjectReference{Kind: "Pod", Name: "nginx"}),
			b: objectRef(corev1.ObjectReference{Kind: "Pod", Name: "web"}),
		},
		"series with different counts": {
			a: eventSeries(3, observed),
			b: eventSeries(4, observed),
		},
		"series observed at different times": {
			a: eventSeries(3, observed),
			b: eventSeries(3, observed.Add(time.Second)),
		},
		"enriched metadata with different labels": {
			a: enrichedObject(event.RegardingObject{
				Labels: map[string]string{"team": "platform"},
			}),
			b: enrichedObject(event.RegardingObject{
				Labels: map[string]string{"team": "storage"},
			}),
		},
		"enriched metadata owned on one side only": {
			a: enrichedObject(event.RegardingObject{Owner: &corev1.ObjectReference{Kind: "ReplicaSet"}}),
			b: enrichedObject(event.RegardingObject{}),
		},
		"enriched metadata differing by terminating flag": {
			a: enrichedObject(event.RegardingObject{Terminating: true}),
			b: enrichedObject(event.RegardingObject{}),
		},
		"a reference and a string": {
			a: objectRef(corev1.ObjectReference{Kind: "Pod"}),
			b: types.String("Pod"),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.a.Equal(tc.b); got != types.False {
				t.Error("values are equal, want the two to differ")
			}
		})
	}
}

func isObjectRef(v ref.Val) bool {
	_, ok := v.(*objectRefVal)
	return ok
}

func fullRef() *corev1.ObjectReference {
	return &fullEvent().Regarding
}

func fullSeries() *eventsv1.EventSeries {
	return fullEvent().Series
}

func fullRegardingObject() *event.RegardingObject {
	return fullEvent().RegardingObject
}

func objectRef(r corev1.ObjectReference) *objectRefVal {
	return &objectRefVal{ref: &r}
}

func eventSeries(count int32, observed time.Time) *seriesVal {
	return &seriesVal{s: &eventsv1.EventSeries{
		Count:            count,
		LastObservedTime: metav1.NewMicroTime(observed),
	}}
}

func enrichedObject(obj event.RegardingObject) *regardingObjectVal {
	return &regardingObjectVal{obj: &obj}
}
