package sanitizer

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/shibernetes/kem-agent/event"
)

func TestFieldLimitsSanitizeEventFields(t *testing.T) {
	ev := &event.Event{
		ReportingController: longValue(),
		ReportingInstance:   longValue(),
		Action:              longValue(),
		Reason:              longValue(),
		Note:                longValue(),
	}
	fieldLimits{}.Sanitize(ev)

	cases := map[string]struct {
		field string
		want  int
	}{
		"reportingController": {field: ev.ReportingController, want: eventMaxReportingControllerSize},
		"reportingInstance":   {field: ev.ReportingInstance, want: eventMaxReportingInstanceSize},
		"action":              {field: ev.Action, want: eventMaxActionSize},
		"reason":              {field: ev.Reason, want: eventMaxReasonSize},
		"note":                {field: ev.Note, want: eventMaxNoteSize},
	}
	for name, tc := range cases {
		if len(tc.field) != tc.want {
			t.Errorf("%s is %d bytes, want %d", name, len(tc.field), tc.want)
		}
	}
}

func TestFieldLimitsSanitizeReferenceFields(t *testing.T) {
	var (
		related = longRef()
		ev      = &event.Event{
			Regarding: longRef(),
			Related:   &related,
		}
	)
	fieldLimits{}.Sanitize(ev)

	refs := map[string]*corev1.ObjectReference{
		"regarding": &ev.Regarding,
		"related":   ev.Related,
	}
	for parent, fields := range refs {
		for name, got := range refFields(fields) {
			if len(got) != eventMaxReferenceFieldSize {
				t.Errorf("%s.%s is %d bytes, want %d", parent, name, len(got), eventMaxReferenceFieldSize)
			}
		}
	}
}

func TestFieldLimitsLeavesShortFieldsAlone(t *testing.T) {
	ev := shortFieldsEvent()
	fieldLimits{}.Sanitize(&ev)

	cases := map[string]struct {
		field string
		want  string
	}{
		"reportingController": {field: ev.ReportingController, want: "kubelet"},
		"action":              {field: ev.Action, want: "Pulling"},
		"reason":              {field: ev.Reason, want: "Pulling"},
		"note":                {field: ev.Note, want: "pulling image"},
		"regarding.name":      {field: ev.Regarding.Name, want: "nginx"},
	}
	for name, tc := range cases {
		if tc.field != tc.want {
			t.Errorf("%s is %q, want %q", name, tc.field, tc.want)
		}
	}
}

func refFields(ref *corev1.ObjectReference) map[string]string {
	return map[string]string{
		"kind":            ref.Kind,
		"namespace":       ref.Namespace,
		"name":            ref.Name,
		"uid":             string(ref.UID),
		"apiVersion":      ref.APIVersion,
		"resourceVersion": ref.ResourceVersion,
		"fieldPath":       ref.FieldPath,
	}
}

func longRef() corev1.ObjectReference {
	return corev1.ObjectReference{
		Kind:            longValue(),
		Namespace:       longValue(),
		Name:            longValue(),
		UID:             types.UID(longValue()),
		APIVersion:      longValue(),
		ResourceVersion: longValue(),
		FieldPath:       longValue(),
	}
}

func longValue() string {
	return strings.Repeat("x", 4<<10)
}

func shortFieldsEvent() event.Event {
	return event.Event{
		ReportingController: "kubelet",
		Action:              "Pulling",
		Reason:              "Pulling",
		Regarding: corev1.ObjectReference{
			Kind: "Pod",
			Name: "nginx",
		},
		Note: "pulling image",
	}
}
