package event

import (
	"encoding/json/jsontext"
	"maps"
	"slices"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// encodeWriter writes JSON tokens to an encoder, holding the first
// write error so a sequence of writes needs no per-call error check.
// Once an error occurs, later writes are skipped and the caller
// returns that error.
type encodeWriter struct {
	enc *jsontext.Encoder
	err error
}

// tok writes one token unless a prior write already failed.
func (w *encodeWriter) tok(tok jsontext.Token) {
	if w.err == nil {
		w.err = w.enc.WriteToken(tok)
	}
}

// str writes an omitempty string field.
func (w *encodeWriter) str(name, val string) {
	if val == "" {
		return
	}
	w.tok(jsontext.String(name))
	w.tok(jsontext.String(val))
}

// mss writes an omitempty string map field with its keys sorted.
func (w *encodeWriter) mss(name string, m map[string]string) {
	if len(m) == 0 {
		return
	}
	w.tok(jsontext.String(name))
	w.tok(jsontext.BeginObject)

	for _, k := range slices.Sorted(maps.Keys(m)) {
		w.tok(jsontext.String(k))
		w.tok(jsontext.String(m[k]))
	}
	w.tok(jsontext.EndObject)
}

// flag writes an omitempty bool field.
func (w *encodeWriter) flag(name string, val bool) {
	if !val {
		return
	}
	w.tok(jsontext.String(name))
	w.tok(jsontext.Bool(val))
}

// microTime writes a [metav1.MicroTime] field as metav1 does, null when
// zero and an RFC3339 microsecond formatted string otherwise. The string
// is formatted straight into the encoder's spare buffer, so no intermediate
// buffer is allocated.
func (w *encodeWriter) microTime(name string, t metav1.MicroTime) {
	w.tok(jsontext.String(name))

	if t.IsZero() {
		w.tok(jsontext.Null)
		return
	}
	if w.err != nil {
		return
	}
	b := w.enc.AvailableBuffer()
	b = append(b, '"')
	b = t.UTC().AppendFormat(b, metav1.RFC3339Micro)
	b = append(b, '"')
	w.err = w.enc.WriteValue(b)
}

// series writes an [eventsv1.EventSeries] field.
func (w *encodeWriter) series(name string, s *eventsv1.EventSeries) {
	w.tok(jsontext.String(name))
	w.tok(jsontext.BeginObject)
	w.tok(jsontext.String(FieldCount))
	w.tok(jsontext.Int(int64(s.Count)))
	w.microTime(FieldLastObservedTime, s.LastObservedTime)
	w.tok(jsontext.EndObject)
}

// objectRef writes a [corev1.ObjectReference] field.
func (w *encodeWriter) objectRef(name string, ref *corev1.ObjectReference) {
	w.tok(jsontext.String(name))
	w.tok(jsontext.BeginObject)
	w.str(FieldKind, ref.Kind)
	w.str(FieldNamespace, ref.Namespace)
	w.str(FieldName, ref.Name)
	w.str(FieldUID, string(ref.UID))
	w.str(FieldAPIVersion, ref.APIVersion)
	w.str(FieldResourceVersion, ref.ResourceVersion)
	w.str(FieldFieldPath, ref.FieldPath)
	w.tok(jsontext.EndObject)
}

// regardingObject writes a [RegardingObject] field.
func (w *encodeWriter) regardingObject(name string, ro *RegardingObject) {
	w.tok(jsontext.String(name))
	w.tok(jsontext.BeginObject)
	w.mss(FieldLabels, ro.Labels)
	w.mss(FieldAnnotations, ro.Annotations)

	if ro.Owner != nil {
		w.objectRef(FieldOwner, ro.Owner)
	}
	w.flag(FieldTerminating, ro.Terminating)
	w.tok(jsontext.EndObject)
}
