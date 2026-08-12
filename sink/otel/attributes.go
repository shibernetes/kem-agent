package otel

import (
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

// Keys of the log records attributes.
// They follow the convention of the Collector's Kubernetes events receiver.
// https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/receiver/k8seventsreceiver/k8s_event_to_logdata.go
//
// The namespace is the one exception. The OTel receiver reports the involved
// object's namespace under k8s.namespace.name, which is already used by the
// resource attributes of the agent, so the event's namespace is reported with
// the key k8s.event.namespace instead.
const (
	keyEventName                = "k8s.event.name"
	keyEventUID                 = "k8s.event.uid"
	keyEventNamespace           = "k8s.event.namespace"
	keyEventReason              = "k8s.event.reason"
	keyEventAction              = "k8s.event.action"
	keyEventCount               = "k8s.event.count"
	keyEventStartTime           = "k8s.event.start_time"
	keyEventReportingController = "k8s.event.reporting_controller"
	keyEventReportingInstance   = "k8s.event.reporting_instance"
)

// Keys of the resource attributes about the object an event regards.
const (
	keyObjectKind            = "k8s.object.kind"
	keyObjectName            = "k8s.object.name"
	keyObjectNamespace       = "k8s.object.namespace"
	keyObjectUID             = "k8s.object.uid"
	keyObjectAPIVersion      = "k8s.object.api_version"
	keyObjectResourceVersion = "k8s.object.resource_version"
	keyObjectFieldPath       = "k8s.object.fieldpath"
	keyObjectTerminating     = "k8s.object.terminating"
	keyOwnerKind             = "k8s.object.owner.kind"
	keyOwnerName             = "k8s.object.owner.name"
	keyOwnerNamespace        = "k8s.object.owner.namespace"
	keyOwnerUID              = "k8s.object.owner.uid"
	keyOwnerAPIVersion       = "k8s.object.owner.api_version"
)

// Prefixes of the labels and annotations of the object an event regards.
// The semantic conventions spell these per kind, as k8s.pod.label.<key>,
// and only for a subset of kinds. An event can be reported for any kind,
// custom resources included, so both reuse the object prefix above.
const (
	prefixObjectLabel      = "k8s.object.label."
	prefixObjectAnnotation = "k8s.object.annotation."
)

// attrs builds the attribute list of a resource or of a log record,
// reusing the protobuf messages behind it across events.
type attrs struct {
	attrs []*commonpb.KeyValue
	kvs   []commonpb.KeyValue
	vals  []commonpb.AnyValue
	strs  []commonpb.AnyValue_StringValue
	ints  []commonpb.AnyValue_IntValue
	bools []commonpb.AnyValue_BoolValue
}

// reset empties the lists and ensures capacity for n attributes.
func (a *attrs) reset(n int) {
	if cap(a.kvs) < n {
		a.kvs = make([]commonpb.KeyValue, 0, n)
		a.vals = make([]commonpb.AnyValue, 0, n)
		a.strs = make([]commonpb.AnyValue_StringValue, 0, n)
		a.ints = make([]commonpb.AnyValue_IntValue, 0, n)
		a.bools = make([]commonpb.AnyValue_BoolValue, 0, n)
		a.attrs = make([]*commonpb.KeyValue, 0, n)
		return
	}
	a.attrs, a.kvs, a.vals = a.attrs[:0], a.kvs[:0], a.vals[:0]
	a.strs, a.ints, a.bools = a.strs[:0], a.ints[:0], a.bools[:0]
}

// str adds a non-empty string attribute.
func (a *attrs) str(key, value string) {
	if value == "" {
		return
	}
	a.strs = append(a.strs, commonpb.AnyValue_StringValue{StringValue: value})
	a.next(key).Value = &a.strs[len(a.strs)-1]
}

// num adds an integer attribute.
func (a *attrs) num(key string, value int64) {
	a.ints = append(a.ints, commonpb.AnyValue_IntValue{IntValue: value})
	a.next(key).Value = &a.ints[len(a.ints)-1]
}

// flag adds a boolean attribute.
func (a *attrs) flag(key string, value bool) {
	a.bools = append(a.bools, commonpb.AnyValue_BoolValue{BoolValue: value})
	a.next(key).Value = &a.bools[len(a.bools)-1]
}

// next adds the key and returns the value to write it under.
func (a *attrs) next(key string) *commonpb.AnyValue {
	a.vals = append(a.vals, commonpb.AnyValue{})
	value := &a.vals[len(a.vals)-1]

	a.kvs = append(a.kvs, commonpb.KeyValue{Key: key, Value: value})
	a.attrs = append(a.attrs, &a.kvs[len(a.kvs)-1])

	return value
}

// static adds attributes that do not change per event, such as the
// agent metadata, which is resolved once. They use none of the storage
// reset reserves.
func (a *attrs) static(kvs []*commonpb.KeyValue) {
	a.attrs = append(a.attrs, kvs...)
}
