package source

import (
	"cmp"
	"time"

	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shibernetes/kem-agent/event"
)

// lift projects a Kubernetes events.k8s.io/v1 event onto the canonical
// [event.Event] type, which is the only conversion between the two.
//
// Strings and maps are shared with the object it reads rather than copied,
// because that object is discarded as soon as the event is built. The
// deprecated fields of an event written through the legacy core/v1 path
// are promoted into the modern fields that replaced them.
func lift(e *eventsv1.Event) *event.Event {
	return &event.Event{
		Labels:              e.Labels,
		Annotations:         e.Annotations,
		UID:                 e.UID,
		ResourceVersion:     e.ResourceVersion,
		Namespace:           e.Namespace,
		Name:                e.Name,
		EventTime:           eventTime(e),
		Series:              eventSeries(e),
		ReportingController: cmp.Or(e.ReportingController, e.DeprecatedSource.Component),
		ReportingInstance:   cmp.Or(e.ReportingInstance, e.DeprecatedSource.Host),
		Action:              e.Action,
		Reason:              e.Reason,
		Regarding:           e.Regarding,
		Related:             e.Related,
		Note:                e.Note,
		Type:                e.Type,
	}
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
