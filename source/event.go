package source

import (
	"cmp"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/source/sanitizer"
)

// lift projects a Kubernetes events.k8s.io/v1 event onto the canonical
// [event.Event] type, which is the only conversion between the two.
//
// Strings and maps are shared with the object it reads rather than copied,
// because that object is discarded as soon as the event is built. The
// deprecated fields of an event written through the legacy core/v1 path
// are promoted into the modern fields that replaced them.
func lift(e *eventsv1.Event) *event.Event {
	// A protobuf client can write invalid UTF-8 in any field that the
	// APIServer does not validate. For an event, it validates the
	// namespace, the labels and the annotation keys.
	ev := &event.Event{
		Labels:              e.Labels,
		Annotations:         e.Annotations,
		UID:                 e.UID,
		ResourceVersion:     e.ResourceVersion,
		Namespace:           e.Namespace,
		Name:                sanitizer.ValidUTF8(e.Name),
		EventTime:           eventTime(e),
		Series:              eventSeries(e),
		ReportingController: sanitizer.ValidUTF8(cmp.Or(e.ReportingController, e.DeprecatedSource.Component)),
		ReportingInstance:   sanitizer.ValidUTF8(cmp.Or(e.ReportingInstance, e.DeprecatedSource.Host)),
		Action:              sanitizer.ValidUTF8(e.Action),
		Reason:              sanitizer.ValidUTF8(e.Reason),
		Regarding:           e.Regarding,
		Related:             e.Related,
		Note:                sanitizer.ValidUTF8(e.Note),
		Type:                sanitizer.ValidUTF8(e.Type),
	}
	validRef(&ev.Regarding)
	if ev.Related != nil {
		validRef(ev.Related)
	}
	for key, value := range ev.Annotations {
		ev.Annotations[key] = sanitizer.ValidUTF8(value)
	}
	return ev
}

// eventTime returns the time at which an event occurred, taken from the
// deprecated first timestamp when it was written through the legacy path,
// and from the time it was read when it carries neither.
func eventTime(e *eventsv1.Event) metav1.MicroTime {
	switch {
	case !e.EventTime.IsZero():
		return e.EventTime
	case !e.DeprecatedFirstTimestamp.IsZero():
		return metav1.NewMicroTime(e.DeprecatedFirstTimestamp.Time)
	default:
		return metav1.NewMicroTime(time.Now())
	}
}

// eventSeries returns the series of a repeating event, built from the
// deprecated count and last timestamp when the event carries none.
func eventSeries(e *eventsv1.Event) *eventsv1.EventSeries {
	if e.Series != nil || e.DeprecatedCount <= 1 {
		return e.Series
	}
	return &eventsv1.EventSeries{
		Count:            e.DeprecatedCount,
		LastObservedTime: metav1.NewMicroTime(e.DeprecatedLastTimestamp.Time),
	}
}

// validRef replaces the invalid UTF-8 in the fields of an object reference.
func validRef(ref *corev1.ObjectReference) {
	ref.Kind = sanitizer.ValidUTF8(ref.Kind)
	ref.Namespace = sanitizer.ValidUTF8(ref.Namespace)
	ref.Name = sanitizer.ValidUTF8(ref.Name)
	ref.UID = types.UID(sanitizer.ValidUTF8(string(ref.UID)))
	ref.APIVersion = sanitizer.ValidUTF8(ref.APIVersion)
	ref.ResourceVersion = sanitizer.ValidUTF8(ref.ResourceVersion)
	ref.FieldPath = sanitizer.ValidUTF8(ref.FieldPath)
}
