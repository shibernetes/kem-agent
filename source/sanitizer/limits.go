package sanitizer

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/shibernetes/kem-agent/event"
)

// Maximum lengths in bytes of the event string fields the events.k8s.io/v1
// API bounds. It enforces them only for events created through that API, so
// one written through the legacy core/v1 endpoint arrives unbounded. Note
// is what core/v1 calls message, and reportingController is the qualified
// name bound of a 253-character prefix, a slash and a 63-character
// name.
//
// See https://github.com/kubernetes/kubernetes/blob/v1.36.3/pkg/apis/core/validation/events.go.
const (
	eventMaxActionSize              = 128
	eventMaxNoteSize                = 1024
	eventMaxReasonSize              = 128
	eventMaxReportingControllerSize = 253 + 1 + 63
	eventMaxReportingInstanceSize   = 128
)

// eventMaxReferenceFieldSize bounds each string field of the object
// references an event carries. The API declares no limit for these, so
// the agent sets one anyway. It exceeds any real reference field, and
// bounds what a filter over those fields has to read.
const (
	eventMaxReferenceFieldSize = 512
)

var _ EventSanitizer = fieldLimits{}

// fieldLimits enforces the length bounds the events.k8s.io/v1 API applies,
// on an event's own string fields and on the object references it carries.
type fieldLimits struct{}

// Sanitize implements the [EventSanitizer] interface.
func (fieldLimits) Sanitize(ev *event.Event) {
	ev.Action = Truncate(ev.Action, eventMaxActionSize)
	ev.Note = Truncate(ev.Note, eventMaxNoteSize)
	ev.Reason = Truncate(ev.Reason, eventMaxReasonSize)
	ev.ReportingController = Truncate(ev.ReportingController, eventMaxReportingControllerSize)
	ev.ReportingInstance = Truncate(ev.ReportingInstance, eventMaxReportingInstanceSize)

	truncateRef(&ev.Regarding)
	if ev.Related != nil {
		truncateRef(ev.Related)
	}
}

// truncateRef shortens every string field of an object reference.
// The reference names an object the event's creator chose, and no
// upstream mechanism bounds that value.
func truncateRef(ref *corev1.ObjectReference) {
	ref.Kind = Truncate(ref.Kind, eventMaxReferenceFieldSize)
	ref.Namespace = Truncate(ref.Namespace, eventMaxReferenceFieldSize)
	ref.Name = Truncate(ref.Name, eventMaxReferenceFieldSize)
	ref.UID = types.UID(Truncate(string(ref.UID), eventMaxReferenceFieldSize))
	ref.APIVersion = Truncate(ref.APIVersion, eventMaxReferenceFieldSize)
	ref.ResourceVersion = Truncate(ref.ResourceVersion, eventMaxReferenceFieldSize)
	ref.FieldPath = Truncate(ref.FieldPath, eventMaxReferenceFieldSize)
}
