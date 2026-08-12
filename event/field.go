package event

import (
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Field returns the value of the named field, spelled as the events.k8s.io/v1
// API spells it, and whether the name is addressable. Times are formatted as
// the encoder writes them and the series count as a decimal integer.
//
// A label or an annotation is addressed by key, with everything after the dot
// taken as the key. Absent data reads as the empty string, so a key an event
// does not carry is indistinguishable from one that was never set.
func (e *Event) Field(name string) (string, bool) {
	switch name {
	case FieldUID:
		return string(e.UID), true
	case FieldResourceVersion:
		return e.ResourceVersion, true
	case FieldNamespace:
		return e.Namespace, true
	case FieldName:
		return e.Name, true
	case FieldEventTime:
		return microTimeField(e.EventTime)
	case FieldReportingController:
		return e.ReportingController, true
	case FieldReportingInstance:
		return e.ReportingInstance, true
	case FieldAction:
		return e.Action, true
	case FieldReason:
		return e.Reason, true
	case FieldNote:
		return e.Note, true
	case FieldType:
		return e.Type, true
	}
	if key, ok := strings.CutPrefix(name, FieldLabels+"."); ok && key != "" {
		return e.Labels[key], true
	}
	if key, ok := strings.CutPrefix(name, FieldAnnotations+"."); ok && key != "" {
		return e.Annotations[key], true
	}
	if sub, ok := strings.CutPrefix(name, FieldSeries+"."); ok {
		return seriesField(e.Series, sub)
	}
	if sub, ok := strings.CutPrefix(name, FieldRegardingObject+"."); ok {
		return e.regardingObjectField(sub)
	}
	if sub, ok := strings.CutPrefix(name, FieldRegarding+"."); ok {
		return refField(&e.Regarding, sub)
	}
	if sub, ok := strings.CutPrefix(name, FieldRelated+"."); ok {
		return refField(e.Related, sub)
	}
	return "", false
}

// regardingObjectField resolves a field of the enriched metadata.
// An event the source never enriched resolves every field to its zero
// value, which is indistinguishable from an object that has none set.
func (e *Event) regardingObjectField(name string) (string, bool) {
	ro := e.RegardingObject

	if name == FieldTerminating {
		return strconv.FormatBool(ro != nil && ro.Terminating), true
	}
	if key, ok := strings.CutPrefix(name, FieldLabels+"."); ok && key != "" {
		if ro == nil {
			return "", true
		}
		return ro.Labels[key], true
	}
	if key, ok := strings.CutPrefix(name, FieldAnnotations+"."); ok && key != "" {
		if ro == nil {
			return "", true
		}
		return ro.Annotations[key], true
	}
	if sub, ok := strings.CutPrefix(name, FieldOwner+"."); ok {
		if ro == nil {
			return refField(nil, sub)
		}
		return refField(ro.Owner, sub)
	}
	return "", false
}

// seriesField resolves a field of the series a repeating event carries.
func seriesField(s *eventsv1.EventSeries, name string) (string, bool) {
	switch name {
	case FieldCount:
		if s == nil {
			return "", true
		}
		return strconv.FormatInt(int64(s.Count), 10), true
	case FieldLastObservedTime:
		if s == nil {
			return "", true
		}
		return microTimeField(s.LastObservedTime)
	}
	return "", false
}

// refField resolves an object reference subfield, or returns an empty
// string when the reference is absent.
func refField(ref *corev1.ObjectReference, name string) (string, bool) {
	if ref == nil {
		ref = &corev1.ObjectReference{}
	}
	switch name {
	case FieldKind:
		return ref.Kind, true
	case FieldNamespace:
		return ref.Namespace, true
	case FieldName:
		return ref.Name, true
	case FieldUID:
		return string(ref.UID), true
	case FieldAPIVersion:
		return ref.APIVersion, true
	case FieldResourceVersion:
		return ref.ResourceVersion, true
	case FieldFieldPath:
		return ref.FieldPath, true
	}
	return "", false
}

// microTimeField formats a time as the encoder writes it, so one
// event reads the same whether it is filtered or serialized.
func microTimeField(t metav1.MicroTime) (string, bool) {
	if t.IsZero() {
		return "", true
	}
	return t.UTC().Format(metav1.RFC3339Micro), true
}
