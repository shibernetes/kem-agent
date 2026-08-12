package event

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ jsonv2.MarshalerTo = (*Event)(nil)

// Event is the agent's own event type, built once from each Kubernetes events.k8s.io/v1
// object. Everything downstream works with this type rather than the Kubernetes one,
// which keeps the agent's event shape under its own control.
//
// An Event must be treated as read-only once built. The same pointer is shared with
// every pipeline and sink, so nothing may modify it in place.
//
// Its JSON tags reuse the Kubernetes field names, so events are serialized under the
// same names the API uses. The EventTime and Regarding fields have no omitempty tag
// because it is inert on a struct value, so both are always written.
type Event struct {
	ObjectMeta
	UID                 types.UID               `json:"uid,omitempty"`
	ResourceVersion     string                  `json:"resourceVersion,omitempty"`
	Namespace           string                  `json:"namespace,omitempty"`
	Name                string                  `json:"name,omitempty"`
	EventTime           metav1.MicroTime        `json:"eventTime"`
	Series              *eventsv1.EventSeries   `json:"series,omitempty"`
	ReportingController string                  `json:"reportingController,omitempty"`
	ReportingInstance   string                  `json:"reportingInstance,omitempty"`
	Action              string                  `json:"action,omitempty"`
	Reason              string                  `json:"reason,omitempty"`
	Regarding           corev1.ObjectReference  `json:"regarding"`
	Related             *corev1.ObjectReference `json:"related,omitempty"`
	Note                string                  `json:"note,omitempty"`
	Type                string                  `json:"type,omitempty"`
	RegardingObject     *RegardingObject        `json:"regardingObject,omitempty"`
}

// RegardingObject holds the metadata of the object an event is about.
// The source fills it when enrichment is enabled, and leaves it nil otherwise.
// Owner is the controlling owner reference, such as the ReplicaSet of a Pod.
//
// The same value is shared by every event about that object, so it must be
// treated as read-only, like the [Event] holding it.
type RegardingObject struct {
	ObjectMeta
	Owner       *corev1.ObjectReference `json:"owner,omitempty"`
	Terminating bool                    `json:"terminating,omitempty"`
}

// ObjectMeta holds the labels and annotations an event and the object it
// regards both carry.
type ObjectMeta struct {
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Time returns the event's occurrence time, taken from the series' last
// observation for a repeating event and from the event time otherwise.
// The lift records the read time on an event that carries neither, so it
// never returns the zero time.
func (e *Event) Time() time.Time {
	if e.Series != nil && !e.Series.LastObservedTime.IsZero() {
		return e.Series.LastObservedTime.Time
	}
	return e.EventTime.Time
}

// MarshalJSONTo implements the [jsonv2.MarshalerTo] interface.
// It writes the event as a JSON object straight into enc, with no
// reflection over the struct and no intermediate buffer.
//
// The field names, order, and omitempty behavior mirror the struct tags.
func (e *Event) MarshalJSONTo(enc *jsontext.Encoder) error {
	w := encodeWriter{enc: enc}
	w.tok(jsontext.BeginObject)

	w.str(FieldUID, string(e.UID))
	w.str(FieldResourceVersion, e.ResourceVersion)
	w.str(FieldNamespace, e.Namespace)
	w.str(FieldName, e.Name)
	w.mss(FieldLabels, e.Labels)
	w.mss(FieldAnnotations, e.Annotations)

	// eventTime has no omitempty tag, so it is always written, null when zero.
	w.microTime(FieldEventTime, e.EventTime)
	if e.Series != nil {
		w.series(FieldSeries, e.Series)
	}
	w.str(FieldReportingController, e.ReportingController)
	w.str(FieldReportingInstance, e.ReportingInstance)
	w.str(FieldAction, e.Action)
	w.str(FieldReason, e.Reason)

	// regarding has no omitempty tag, so it is always written.
	w.objectRef(FieldRegarding, &e.Regarding)
	if e.Related != nil {
		w.objectRef(FieldRelated, e.Related)
	}
	w.str(FieldNote, e.Note)
	w.str(FieldType, e.Type)
	if e.RegardingObject != nil {
		w.regardingObject(FieldRegardingObject, e.RegardingObject)
	}
	w.tok(jsontext.EndObject)

	return w.err
}
