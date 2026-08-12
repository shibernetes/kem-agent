package graylog

import (
	"encoding/json/jsontext"
	"maps"
	"slices"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/identity"
	"github.com/shibernetes/kem-agent/sink"
)

const (
	gelfVersion         = "1.1"
	levelWarning        = 4
	levelInfo           = 6
	defaultShortMessage = "-"
	reservedFieldPrefix = "kem_"
)

// The keys of the additional fields, written whole so that encoding an
// event appends them rather than building them. Each opens with the comma
// separating it from the previous field.
const (
	keyEventReason              = `,"_event_reason":`
	keyEventAction              = `,"_event_action":`
	keyEventType                = `,"_event_type":`
	keyEventCount               = `,"_event_count":`
	keyEventStartTime           = `,"_event_start_time":`
	keyEventName                = `,"_event_name":`
	keyEventUID                 = `,"_event_uid":`
	keyEventReportingController = `,"_event_reporting_controller":`
	keyEventReportingInstance   = `,"_event_reporting_instance":`
	keyNamespaceName            = `,"_namespace_name":`
	keyObjectKind               = `,"_object_kind":`
	keyObjectName               = `,"_object_name":`
	keyObjectNamespace          = `,"_object_namespace":`
	keyObjectUID                = `,"_object_uid":`
	keyObjectAPIVersion         = `,"_object_api_version":`
	keyObjectResourceVersion    = `,"_object_resource_version":`
	keyObjectOwnerKind          = `,"_object_owner_kind":`
	keyObjectOwnerName          = `,"_object_owner_name":`
	keyObjectTerminating        = `,"_object_terminating":`
	keyIdentityCluster          = `,"_` + reservedFieldPrefix + `cluster":`
	keyIdentityNode             = `,"_` + reservedFieldPrefix + `node":`
	keyIdentityNamespace        = `,"_` + reservedFieldPrefix + `namespace":`
	keyIdentityPod              = `,"_` + reservedFieldPrefix + `pod":`
	keyIdentityVersion          = `,"_` + reservedFieldPrefix + `version":`
	keyObjectLabel              = `,"_object_label_`
	keyObjectAnnotation         = `,"_object_annotation_`
)

var (
	_ sink.Encoder = encoder{}
	_ sink.Framer  = framer{}
)

// encoder writes an event as one GELF 1.1 message.
type encoder struct {
	prefix []byte
	static []byte
}

// A framer joins frames that carry their own terminator.
type framer struct{}

// newFramer returns a framer concatenating null-terminated frames.
func newFramer() sink.Framer {
	return framer{}
}

// Separator implements the [sink.Framer] interface.
func (framer) Separator() []byte {
	return nil
}

// Compose implements the [sink.Framer] interface.
func (framer) Compose(dst, frames []byte, _ int) []byte {
	return append(dst, frames...)
}

// Fixed implements the [sink.Framer] interface.
func (framer) Fixed() int {
	return 0
}

func newEncoder(cfg Config, id identity.AgentMetadata) sink.Encoder {
	host := cfg.SourceHost
	if host == "" {
		host = id.Pod
	}
	var b []byte

	prefix := appendQuotedString([]byte(`{"version":"`+gelfVersion+`","host":`), host)
	prefix = append(prefix, `,"short_message":`...)

	b = appendStringField(b, keyIdentityCluster, id.Cluster)
	b = appendStringField(b, keyIdentityNode, id.Node)
	b = appendStringField(b, keyIdentityNamespace, id.Namespace)
	b = appendStringField(b, keyIdentityPod, id.Pod)
	b = appendStringField(b, keyIdentityVersion, id.Version)

	for _, name := range slices.Sorted(maps.Keys(cfg.AdditionalFields)) {
		b = append(b, `,"_`...)
		b = appendFieldName(b, name)
		b = append(b, `":`...)
		b = appendQuotedString(b, cfg.AdditionalFields[name])
	}
	return encoder{
		prefix: prefix,
		static: b,
	}
}

// AppendEvent implements the [sink.Encoder] interface.
// It appends the event as a GELF message with a null terminator.
func (e encoder) AppendEvent(dst []byte, ev *event.Event) []byte {
	dst = append(dst, e.prefix...)
	dst = appendQuotedString(dst, shortMessage(ev))

	if ev.Note != "" {
		dst = append(dst, `,"full_message":`...)
		dst = appendQuotedString(dst, ev.Note)
	}
	// A zero time is omitted, so the server defaults to the delivery time instead.
	if ts := ev.Time(); !ts.IsZero() {
		dst = append(dst, `,"timestamp":`...)

		// Format as seconds plus a fractional part rather than UnixNano/1e9,
		// because the latter silently wraps outside years range ~1678-2262,
		// and an event can be created with a far-future time through the legacy
		// core/v1 endpoint.
		dst = strconv.AppendFloat(dst, float64(ts.Unix())+float64(ts.Nanosecond())/1e9, 'f', 3, 64)
	}
	dst = appendFields(dst, ev)
	dst = append(dst, e.static...)

	return append(dst, '}', 0)
}

func appendFields(dst []byte, ev *event.Event) []byte {
	dst = append(dst, `,"level":`...)
	dst = strconv.AppendInt(dst, int64(gelfLevel(ev.Type)), 10)
	dst = appendStringField(dst, keyEventReason, ev.Reason)
	dst = appendStringField(dst, keyEventAction, ev.Action)
	dst = appendStringField(dst, keyEventType, ev.Type)
	dst = appendSeries(dst, ev)
	dst = appendStringField(dst, keyEventName, ev.Name)
	dst = appendStringField(dst, keyEventUID, string(ev.UID))
	dst = appendStringField(dst, keyEventReportingController, ev.ReportingController)
	dst = appendStringField(dst, keyEventReportingInstance, ev.ReportingInstance)
	dst = appendStringField(dst, keyNamespaceName, ev.Namespace)
	dst = appendStringField(dst, keyObjectKind, ev.Regarding.Kind)
	dst = appendStringField(dst, keyObjectName, ev.Regarding.Name)
	dst = appendStringField(dst, keyObjectNamespace, ev.Regarding.Namespace)
	dst = appendStringField(dst, keyObjectUID, string(ev.Regarding.UID))
	dst = appendStringField(dst, keyObjectAPIVersion, ev.Regarding.APIVersion)
	dst = appendStringField(dst, keyObjectResourceVersion, ev.Regarding.ResourceVersion)

	return appendRegardingObject(dst, ev.RegardingObject)
}

// shortMessage returns the GELF short message for an event.
func shortMessage(ev *event.Event) string {
	if ev.Reason != "" {
		return ev.Reason
	}
	return defaultShortMessage
}

// gelfLevel maps an event type to the syslog severity GELF encodes
// as its numeric level. Unknown types fall back to informational.
func gelfLevel(eventType string) int {
	switch eventType {
	case corev1.EventTypeWarning:
		return levelWarning
	case corev1.EventTypeNormal:
		return levelInfo
	}
	return levelInfo
}

// appendSeries appends how many times the event has occurred, and when
// it was first seen for a repeating one. The message timestamp reports
// the last occurrence rather than the first.
func appendSeries(dst []byte, ev *event.Event) []byte {
	count := int64(1)
	if ev.Series != nil {
		count = int64(ev.Series.Count)
	}
	dst = append(dst, keyEventCount...)
	dst = strconv.AppendInt(dst, count, 10)

	if ev.Series != nil {
		dst = append(dst, keyEventStartTime...)
		dst = append(dst, '"')
		dst = ev.EventTime.UTC().AppendFormat(dst, time.RFC3339Nano)
		dst = append(dst, '"')
	}
	return dst
}

// appendRegardingObject appends the metadata of the object the event
// regards, which enrichment resolved.
func appendRegardingObject(dst []byte, obj *event.RegardingObject) []byte {
	if obj == nil {
		return dst
	}
	if obj.Owner != nil {
		dst = appendStringField(dst, keyObjectOwnerKind, obj.Owner.Kind)
		dst = appendStringField(dst, keyObjectOwnerName, obj.Owner.Name)
	}
	dst = append(dst, keyObjectTerminating...)
	dst = strconv.AppendBool(dst, obj.Terminating)
	dst = appendMapField(dst, keyObjectLabel, obj.Labels)

	return appendMapField(dst, keyObjectAnnotation, obj.Annotations)
}

// appendMapField appends one additional field per entry in m.
// The keys are sorted so that an event is always encoded similarly.
func appendMapField(dst []byte, head string, m map[string]string) []byte {
	for _, name := range slices.Sorted(maps.Keys(m)) {
		dst = append(dst, head...)
		dst = appendFieldName(dst, name)
		dst = append(dst, `":`...)
		dst = appendQuotedString(dst, m[name])
	}
	return dst
}

// appendFieldName appends a key normalized as a GELF field name. Label and
// annotation keys are qualified names, so the slash character of their
// optional prefix is the only character that a field name rejects, and is
// replaced by an underscore.
func appendFieldName(dst []byte, name string) []byte {
	for i := range len(name) {
		if b := name[i]; b == '/' {
			dst = append(dst, '_')
		} else {
			dst = append(dst, b)
		}
	}
	return dst
}

func appendStringField(dst []byte, key, value string) []byte {
	if value == "" {
		return dst
	}
	return appendQuotedString(append(dst, key...), value)
}

// appendQuotedString appends s to dst as a quoted JSON string.
func appendQuotedString(dst []byte, s string) []byte {
	b, _ := jsontext.AppendQuote(dst, s)
	return b
}
