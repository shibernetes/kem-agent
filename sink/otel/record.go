package otel

import (
	"slices"
	"strings"
	"time"
	"unsafe"

	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/identity"
	"github.com/shibernetes/kem-agent/internal/version"
)

const (
	// eventAttrCount is the number of attributes for an event.
	eventAttrCount = 9

	// objectAttrCount is the number of attributes the object an
	// event regards has, labels and annotations aside.
	objectAttrCount = 13

	// scopeName denotes the instrumentation scope of the records.
	scopeName = "github.com/shibernetes/kem-agent/sink/otel"
)

// A record holds the protobuf messages one frame is built from.
// The messages are wired once and refilled per event, so encoding one
// allocates nothing beyond the sorted keys of an enriched object.
type record struct {
	logs        logspb.ResourceLogs
	resource    resourcepb.Resource
	scopeLogs   logspb.ScopeLogs
	scope       commonpb.InstrumentationScope
	entry       logspb.LogRecord
	entryBody   commonpb.AnyValue
	bodyValue   commonpb.AnyValue_StringValue
	agentAttrs  []*commonpb.KeyValue
	objectAttrs attrs
	eventAttrs  attrs
	sortKeys    []string
	arena       []byte
}

// newRecord returns a record with the agent metadata as attributes.
func newRecord(meta identity.AgentMetadata) *record {
	r := &record{agentAttrs: agentAttributes(meta)}

	r.entryBody.Value = &r.bodyValue
	r.entry.Body = &r.entryBody
	r.scope.Name, r.scope.Version = scopeName, meta.Version
	r.scopeLogs.Scope = &r.scope
	r.scopeLogs.LogRecords = []*logspb.LogRecord{&r.entry}
	r.logs.Resource = &r.resource
	r.logs.ScopeLogs = []*logspb.ScopeLogs{&r.scopeLogs}

	return r
}

// fill fills the record from the event and returns the ResourceLogs a frame
// is marshaled from. The result is valid until the next call.
func (r *record) fill(ev *event.Event, observed time.Time) *logspb.ResourceLogs {
	r.arena = r.arena[:0]
	r.entry.TimeUnixNano = uint64(ev.Time().UnixNano())
	r.entry.ObservedTimeUnixNano = uint64(observed.UnixNano())
	r.entry.SeverityNumber = severityOf(ev.Type)
	r.entry.SeverityText = ev.Type
	r.bodyValue.StringValue = ev.Note

	r.writeObject(ev)
	r.writeEvent(ev)

	r.resource.Attributes = r.objectAttrs.attrs
	r.entry.Attributes = r.eventAttrs.attrs

	return &r.logs
}

// writeObject fills the resource attributes, which describe
// the agent and the object the event is about.
func (r *record) writeObject(ev *event.Event) {
	var (
		object = ev.RegardingObject
		count  = objectAttrCount
	)
	if object != nil {
		count += len(object.Labels) + len(object.Annotations)
	}
	r.objectAttrs.reset(count)
	r.objectAttrs.static(r.agentAttrs)

	ref := &ev.Regarding
	r.objectAttrs.str(keyObjectKind, ref.Kind)
	r.objectAttrs.str(keyObjectName, ref.Name)
	r.objectAttrs.str(keyObjectNamespace, ref.Namespace)
	r.objectAttrs.str(keyObjectUID, string(ref.UID))
	r.objectAttrs.str(keyObjectAPIVersion, ref.APIVersion)
	r.objectAttrs.str(keyObjectResourceVersion, ref.ResourceVersion)
	r.objectAttrs.str(keyObjectFieldPath, ref.FieldPath)

	if object == nil {
		return
	}
	r.writeSorted(prefixObjectLabel, object.Labels)
	r.writeSorted(prefixObjectAnnotation, object.Annotations)

	if object.Terminating {
		r.objectAttrs.flag(keyObjectTerminating, true)
	}
	if owner := object.Owner; owner != nil {
		r.objectAttrs.str(keyOwnerKind, owner.Kind)
		r.objectAttrs.str(keyOwnerName, owner.Name)
		r.objectAttrs.str(keyOwnerNamespace, owner.Namespace)
		r.objectAttrs.str(keyOwnerUID, string(owner.UID))
		r.objectAttrs.str(keyOwnerAPIVersion, owner.APIVersion)
	}
}

// writeEvent fills the attributes of the log record, which describe
// the event itself.
func (r *record) writeEvent(ev *event.Event) {
	r.eventAttrs.reset(eventAttrCount)
	r.eventAttrs.str(keyEventName, ev.Name)
	r.eventAttrs.str(keyEventUID, string(ev.UID))
	r.eventAttrs.str(keyEventNamespace, ev.Namespace)
	r.eventAttrs.str(keyEventReason, ev.Reason)
	r.eventAttrs.str(keyEventAction, ev.Action)
	r.eventAttrs.str(keyEventReportingController, ev.ReportingController)
	r.eventAttrs.str(keyEventReportingInstance, ev.ReportingInstance)

	if ev.Series == nil {
		return
	}
	// The event time of a series is its first observation, which the
	// record timestamp no longer reports once the series moves on.
	at := len(r.arena)
	r.arena = ev.EventTime.UTC().AppendFormat(r.arena, time.RFC3339Nano)

	r.eventAttrs.str(keyEventStartTime, r.interned(at))

	if ev.Series.Count > 0 {
		r.eventAttrs.num(keyEventCount, int64(ev.Series.Count))
	}
}

// writeSorted writes a map as resource attributes, sorted by key.
func (r *record) writeSorted(prefix string, m map[string]string) {
	if len(m) == 0 {
		return
	}
	r.sortKeys = r.sortKeys[:0]
	for key := range m {
		r.sortKeys = append(r.sortKeys, key)
	}
	slices.Sort(r.sortKeys)

	for _, key := range r.sortKeys {
		at := len(r.arena)
		r.arena = append(append(r.arena, prefix...), key...)

		r.objectAttrs.str(r.interned(at), m[key])
	}
}

// interned returns the bytes appended to the arena from index i,
// as a string pointing into it.
// The value is valid until the next fill.
func (r *record) interned(i int) string {
	return unsafe.String(&r.arena[i], len(r.arena)-i)
}

// agentAttributes returns the attributes describing the agent.
func agentAttributes(meta identity.AgentMetadata) []*commonpb.KeyValue {
	var a attrs

	a.reset(6)
	a.str(string(semconv.ServiceNameKey), version.Name)
	a.str(string(semconv.ServiceVersionKey), meta.Version)
	a.str(string(semconv.K8SClusterNameKey), meta.Cluster)
	a.str(string(semconv.K8SNamespaceNameKey), meta.Namespace)
	a.str(string(semconv.K8SPodNameKey), meta.Pod)
	a.str(string(semconv.K8SNodeNameKey), meta.Node)

	return a.attrs
}

// severityOf maps an event type to a severity number, using the same
// mapping as the Collector's Kubernetes events receiver. The API defines
// the first two types and reserves the right to add others, which is why
// the receiver reads the last two as well.
// https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/receiver/k8seventsreceiver/k8s_event_to_logdata.go
//
// A type outside the four leaves the number unset, which is what
// SEVERITY_NUMBER_UNSPECIFIED means, while the text keeps the event's
// own type.
func severityOf(eventType string) logspb.SeverityNumber {
	switch {
	case strings.EqualFold(eventType, corev1.EventTypeNormal):
		return logspb.SeverityNumber_SEVERITY_NUMBER_INFO
	case strings.EqualFold(eventType, corev1.EventTypeWarning):
		return logspb.SeverityNumber_SEVERITY_NUMBER_WARN
	case strings.EqualFold(eventType, "Error"):
		return logspb.SeverityNumber_SEVERITY_NUMBER_ERROR
	case strings.EqualFold(eventType, "Critical"):
		return logspb.SeverityNumber_SEVERITY_NUMBER_FATAL
	}
	return logspb.SeverityNumber_SEVERITY_NUMBER_UNSPECIFIED
}
