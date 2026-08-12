package filter

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shibernetes/kem-agent/event"
)

func TestEventValGetsStringFields(t *testing.T) {
	ev := &eventVal{ev: fullEvent()}

	assertStringGets(t, ev.Get, map[string]string{
		"uid":                 "uid-1",
		"resourceVersion":     "42",
		"namespace":           "default",
		"name":                "nginx",
		"reportingController": "kubelet",
		"reportingInstance":   "node-001",
		"action":              "Pulling",
		"reason":              "Failed",
		"note":                "pull failed",
		"type":                "Warning",
	})
}

func TestEventValGetsEventTime(t *testing.T) {
	ev := fullEvent()
	assertTimestampGet(t, (&eventVal{ev: ev}).Get, "eventTime", ev.EventTime.Time)
}

func TestEventValGetsSubObjects(t *testing.T) {
	ev := &eventVal{ev: fullEvent()}

	cases := map[string]any{
		"regarding":       (*objectRefVal)(nil),
		"related":         (*objectRefVal)(nil),
		"series":          (*seriesVal)(nil),
		"regardingObject": (*regardingObjectVal)(nil),
	}
	for field, want := range cases {
		t.Run(field, func(t *testing.T) {
			got := ev.Get(types.String(field))
			if reflect.TypeOf(got) != reflect.TypeOf(want) {
				t.Errorf("%s is %T, want %T", field, got, want)
			}
		})
	}
}

// TestEventValCachesMapViews covers the label and annotation map
// views, which are built on first use so that an expression reading
// one twice pays for it once.
func TestEventValCachesMapViews(t *testing.T) {
	ev := &eventVal{ev: fullEvent()}

	cases := map[string]struct {
		key, want string
	}{
		"labels":      {"app", "web"},
		"annotations": {"team", "ops"},
	}
	for field, tc := range cases {
		t.Run(field, func(t *testing.T) {
			got := ev.Get(types.String(field))

			view, ok := got.(traits.Mapper)
			if !ok {
				t.Fatalf("%s is %T, want a mapper", field, got)
			}
			if v := view.Get(types.String(tc.key)); v != types.String(tc.want) {
				t.Errorf("%s[%q] = %v, want %q", field, tc.key, v, tc.want)
			}
			if ev.Get(types.String(field)) != got {
				t.Errorf("%s was rebuilt on the second read, want the cached view", field)
			}
		})
	}
}

// TestEventValNullsAbsentSubObjects covers the fields an event may
// not have, which read as null so that an expression can compare one
// against null and never dereference a missing object. Presence is
// a separate question, answered by IsSet.
func TestEventValNullsAbsentSubObjects(t *testing.T) {
	ev := &eventVal{ev: &event.Event{}}

	for _, field := range []string{
		"series",
		"related",
		"regardingObject",
	} {
		t.Run(field, func(t *testing.T) {
			if got := ev.Get(types.String(field)); got != types.NullValue {
				t.Errorf("%s = %v, want null", field, got)
			}
		})
	}
}

func TestEventValPresence(t *testing.T) {
	var (
		full = &eventVal{ev: fullEvent()}
		bare = &eventVal{ev: &event.Event{}}
	)
	assertPresence(t, full.IsSet, bare.IsSet,
		"uid",
		"resourceVersion",
		"namespace",
		"name",
		"labels",
		"annotations",
		"eventTime",
		"series",
		"reportingController",
		"reportingInstance",
		"action",
		"reason",
		"regarding",
		"related",
		"note",
		"type",
		"regardingObject",
	)
}

// TestEventValProtocol covers the [ref.Val] methods, each of which
// names the type it belongs to. The four wrappers have near-identical
// method sets, so a constant left behind by a copy-paste compiles and
// shows up nowhere else.
func TestEventValProtocol(t *testing.T) {
	full := fullEvent()
	assertRefValMethods(t, &eventVal{ev: full}, eventTypeName, full,
		&eventVal{ev: full},
		&eventVal{ev: fullEvent()},
	)
}

func TestEventValRejectsBadFields(t *testing.T) {
	ev := &eventVal{ev: fullEvent()}
	assertFieldErrors(t, ev.Get, ev.IsSet)
}

// TestEventValResetClearsCachedViews covers the reuse of one value
// across events. The enriched object holds views of its own, so a
// reset that only cleared the event's would leave an event reading
// the previous object's labels.
func TestEventValResetClearsCachedViews(t *testing.T) {
	var (
		first  = fullEvent()
		second = fullEvent()
		ev     = &eventVal{}
	)
	second.Labels = map[string]string{"app": "api"}
	second.RegardingObject = &event.RegardingObject{
		Labels: map[string]string{"team": "storage"},
	}
	ev.reset(first)
	enriched := ev.Get(types.String("regardingObject")).(*regardingObjectVal)

	readLabelByKey(t, ev.Get(types.String("labels")), "app")
	readLabelByKey(t, enriched.Get(types.String("labels")), "team")

	ev.reset(second)
	if got := readLabelByKey(t, ev.Get(types.String("labels")), "app"); got != "api" {
		t.Errorf("labels[app] = %q, want the second event's value", got)
	}
	enriched = ev.Get(types.String("regardingObject")).(*regardingObjectVal)
	if got := readLabelByKey(t, enriched.Get(types.String("labels")), "team"); got != "storage" {
		t.Errorf("regardingObject.labels[team] = %q, want the second event's value", got)
	}
}

func assertStringGets(t *testing.T, get func(ref.Val) ref.Val, fields map[string]string) {
	t.Helper()

	for field, want := range fields {
		if got := get(types.String(field)); got != types.String(want) {
			t.Errorf("Get(%q) = %v, want %q", field, got, want)
		}
	}
}

func assertTimestampGet(t *testing.T, get func(ref.Val) ref.Val, field string, want time.Time) {
	t.Helper()

	got := get(types.String(field))

	ts, ok := got.(types.Timestamp)
	if !ok {
		t.Fatalf("%s is %T, want a timestamp", field, got)
	}
	if !ts.Time.Equal(want) {
		t.Errorf("%s = %v, want %v", field, ts.Time, want)
	}
}

func assertPresence(t *testing.T, set, unset func(ref.Val) ref.Val, fields ...string) {
	t.Helper()

	for _, field := range fields {
		if set(types.String(field)) != types.True {
			t.Errorf("IsSet(%q) on a populated value returned false, want true", field)
		}
		if unset(types.String(field)) != types.False {
			t.Errorf("IsSet(%q) on an empty value returned true, want false", field)
		}
	}
}

func assertFieldErrors(t *testing.T, get, isSet func(ref.Val) ref.Val) {
	t.Helper()

	if !types.IsError(get(types.String("unknown"))) {
		t.Error("Get on an unknown field did not error")
	}
	if !types.IsError(isSet(types.String("unknown"))) {
		t.Error("IsSet on an unknown field did not error")
	}
	if !types.IsError(get(types.Int(0))) {
		t.Error("Get with a non-string index did not error")
	}
	if !types.IsError(isSet(types.Int(0))) {
		t.Error("IsSet with a non-string index did not error")
	}
}

func assertRefValMethods(t *testing.T, v ref.Val, typeName string, value any, same, different ref.Val) {
	t.Helper()

	if v.Type().TypeName() != typeName {
		t.Errorf("Type() = %q, want %q", v.Type().TypeName(), typeName)
	}
	if v.Value() != value {
		t.Error("Value() did not return the underlying value")
	}
	if v.Equal(same) != types.True {
		t.Error("Equal(an equal value) is false, want true")
	}
	if v.Equal(different) != types.False {
		t.Error("Equal(a different value) is true, want false")
	}
	if v.Equal(types.String("x")) != types.False {
		t.Error("Equal(a mismatched type) is true, want false")
	}
	converted := v.ConvertToType(types.TypeType)

	asType, ok := converted.(*types.Type)
	if !ok {
		t.Fatalf("ConvertToType(type) = %v, want the value's own type", converted)
	}
	if asType.TypeName() != typeName {
		t.Errorf("ConvertToType(type) = %q, want %q", asType.TypeName(), typeName)
	}
	if !types.IsError(v.ConvertToType(types.StringType)) {
		t.Error("ConvertToType(string) did not error")
	}
	if _, err := v.ConvertToNative(reflect.TypeFor[string]()); err == nil {
		t.Error("ConvertToNative did not error")
	}
}

func readLabelByKey(t *testing.T, v ref.Val, key string) string {
	t.Helper()

	view, ok := v.(traits.Mapper)
	if !ok {
		t.Fatalf("got %T, want a mapper", v)
	}
	got, ok := view.Get(types.String(key)).(types.String)
	if !ok {
		t.Fatalf("key %q is missing", key)
	}
	return string(got)
}

func fullEvent() *event.Event {
	return &event.Event{
		Labels:          map[string]string{"app": "web"},
		Annotations:     map[string]string{"team": "ops"},
		UID:             "uid-1",
		ResourceVersion: "42",
		Namespace:       "default",
		Name:            "nginx",
		EventTime:       metav1.NewMicroTime(time.Date(2009, 1, 3, 0, 0, 0, 0, time.UTC)),
		Series: &eventsv1.EventSeries{
			Count:            3,
			LastObservedTime: metav1.NewMicroTime(time.Date(2009, 1, 3, 1, 0, 0, 0, time.UTC)),
		},
		ReportingController: "kubelet",
		ReportingInstance:   "node-001",
		Action:              "Pulling",
		Reason:              "Failed",
		Regarding: corev1.ObjectReference{
			Kind:            "Pod",
			Namespace:       "default",
			Name:            "nginx",
			UID:             "ref-uid",
			APIVersion:      "v1",
			ResourceVersion: "69",
			FieldPath:       "spec",
		},
		Related: &corev1.ObjectReference{
			Kind: "Node",
			Name: "node-002",
		},
		Note: "pull failed",
		Type: "Warning",
		RegardingObject: &event.RegardingObject{
			Labels:      map[string]string{"team": "platform"},
			Annotations: map[string]string{"owner": "sre"},
			Owner: &corev1.ObjectReference{
				Kind: "ReplicaSet",
				Name: "nginx-abc",
			},
			Terminating: true,
		},
	}
}
