package filter

import (
	"fmt"
	"maps"
	"reflect"

	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"

	"github.com/shibernetes/kem-agent/event"
)

var (
	_ ref.Val            = (*eventVal)(nil)
	_ traits.Indexer     = (*eventVal)(nil)
	_ traits.FieldTester = (*eventVal)(nil)
)

// eventVal adapts a canonical event as a CEL value, resolving each
// field an expression can use. The sub-object wrappers are reused
// from one event to the next, so reading into one costs no allocation
// beyond the value CEL boxes.
type eventVal struct {
	ev              *event.Event
	regarding       objectRefVal
	related         objectRefVal
	series          seriesVal
	regardingObject regardingObjectVal
	labels          ref.Val
	annotations     ref.Val
}

// Get implements the [traits.Indexer] interface.
func (v *eventVal) Get(index ref.Val) ref.Val {
	name, ok := index.(types.String)
	if !ok {
		return types.ValOrErr(index, "field name is not a string")
	}
	switch string(name) {
	case event.FieldUID:
		return types.String(v.ev.UID)
	case event.FieldResourceVersion:
		return types.String(v.ev.ResourceVersion)
	case event.FieldNamespace:
		return types.String(v.ev.Namespace)
	case event.FieldName:
		return types.String(v.ev.Name)
	case event.FieldLabels:
		if v.labels == nil {
			v.labels = types.NewStringStringMap(types.DefaultTypeAdapter, v.ev.Labels)
		}
		return v.labels
	case event.FieldAnnotations:
		if v.annotations == nil {
			v.annotations = types.NewStringStringMap(types.DefaultTypeAdapter, v.ev.Annotations)
		}
		return v.annotations
	case event.FieldEventTime:
		return types.Timestamp{Time: v.ev.EventTime.Time}
	case event.FieldSeries:
		if v.ev.Series == nil {
			return types.NullValue
		}
		v.series.s = v.ev.Series
		return &v.series
	case event.FieldReportingController:
		return types.String(v.ev.ReportingController)
	case event.FieldReportingInstance:
		return types.String(v.ev.ReportingInstance)
	case event.FieldAction:
		return types.String(v.ev.Action)
	case event.FieldReason:
		return types.String(v.ev.Reason)
	case event.FieldRegarding:
		v.regarding.ref = &v.ev.Regarding
		return &v.regarding
	case event.FieldRelated:
		if v.ev.Related == nil {
			return types.NullValue
		}
		v.related.ref = v.ev.Related
		return &v.related
	case event.FieldNote:
		return types.String(v.ev.Note)
	case event.FieldType:
		return types.String(v.ev.Type)
	case event.FieldRegardingObject:
		if v.ev.RegardingObject == nil {
			return types.NullValue
		}
		v.regardingObject.obj = v.ev.RegardingObject
		return &v.regardingObject
	}
	return types.NewErr("no such field %q", string(name))
}

// IsSet implements the [traits.FieldTester] interface.
// A field counts as set when it holds something, an empty string,
// an empty map and a zero time read as absent.
func (v *eventVal) IsSet(field ref.Val) ref.Val {
	name, ok := field.(types.String)
	if !ok {
		return types.ValOrErr(field, "field name is not a string")
	}
	switch string(name) {
	case event.FieldUID:
		return types.Bool(v.ev.UID != "")
	case event.FieldResourceVersion:
		return types.Bool(v.ev.ResourceVersion != "")
	case event.FieldNamespace:
		return types.Bool(v.ev.Namespace != "")
	case event.FieldName:
		return types.Bool(v.ev.Name != "")
	case event.FieldLabels:
		return types.Bool(len(v.ev.Labels) > 0)
	case event.FieldAnnotations:
		return types.Bool(len(v.ev.Annotations) > 0)
	case event.FieldEventTime:
		return types.Bool(!v.ev.EventTime.IsZero())
	case event.FieldSeries:
		return types.Bool(v.ev.Series != nil)
	case event.FieldReportingController:
		return types.Bool(v.ev.ReportingController != "")
	case event.FieldReportingInstance:
		return types.Bool(v.ev.ReportingInstance != "")
	case event.FieldAction:
		return types.Bool(v.ev.Action != "")
	case event.FieldReason:
		return types.Bool(v.ev.Reason != "")
	case event.FieldRegarding:
		return types.Bool(v.ev.Regarding != corev1.ObjectReference{})
	case event.FieldRelated:
		return types.Bool(v.ev.Related != nil)
	case event.FieldNote:
		return types.Bool(v.ev.Note != "")
	case event.FieldType:
		return types.Bool(v.ev.Type != "")
	case event.FieldRegardingObject:
		return types.Bool(v.ev.RegardingObject != nil)
	}
	return types.NewErr("no such field %q", string(name))
}

// Equal implements the [ref.Val] interface.
// Two values are equal when they wrap the same event, which is the only
// case an expression reaches, one event being bound at a time.
func (v *eventVal) Equal(other ref.Val) ref.Val {
	o, ok := other.(*eventVal)
	return types.Bool(ok && v.ev == o.ev)
}

func (v *eventVal) Type() ref.Type {
	return eventType
}

func (v *eventVal) Value() any {
	return v.ev
}

func (v *eventVal) ConvertToType(t ref.Type) ref.Val {
	return convertToType(eventType, t)
}

func (v *eventVal) ConvertToNative(typeDesc reflect.Type) (any, error) {
	return nil, noNativeConversion(eventType, typeDesc)
}

// reset rebinds the value to another event.
func (v *eventVal) reset(ev *event.Event) {
	// Zeroing the whole struct drops the cached views and the
	// sub-object pointers together, so it covers any field added
	// later.
	*v = eventVal{ev: ev}
}

var (
	_ ref.Val            = (*objectRefVal)(nil)
	_ traits.Indexer     = (*objectRefVal)(nil)
	_ traits.FieldTester = (*objectRefVal)(nil)
)

// objectRefVal adapts a Kubernetes object reference as a CEL value.
// The wrapped reference is never nil. A reference the event does
// not carry reads as null instead.
type objectRefVal struct {
	ref *corev1.ObjectReference
}

// Get implements the [traits.Indexer] interface.
func (v *objectRefVal) Get(index ref.Val) ref.Val {
	name, ok := index.(types.String)
	if !ok {
		return types.ValOrErr(index, "field name is not a string")
	}
	switch string(name) {
	case event.FieldKind:
		return types.String(v.ref.Kind)
	case event.FieldNamespace:
		return types.String(v.ref.Namespace)
	case event.FieldName:
		return types.String(v.ref.Name)
	case event.FieldUID:
		return types.String(v.ref.UID)
	case event.FieldAPIVersion:
		return types.String(v.ref.APIVersion)
	case event.FieldResourceVersion:
		return types.String(v.ref.ResourceVersion)
	case event.FieldFieldPath:
		return types.String(v.ref.FieldPath)
	}
	return types.NewErr("no such field %q", string(name))
}

// IsSet implements the [traits.FieldTester] interface.
// A field counts as set when it holds something, an empty string
// read as absent.
func (v *objectRefVal) IsSet(field ref.Val) ref.Val {
	name, ok := field.(types.String)
	if !ok {
		return types.ValOrErr(field, "field name is not a string")
	}
	switch string(name) {
	case event.FieldKind:
		return types.Bool(v.ref.Kind != "")
	case event.FieldNamespace:
		return types.Bool(v.ref.Namespace != "")
	case event.FieldName:
		return types.Bool(v.ref.Name != "")
	case event.FieldUID:
		return types.Bool(v.ref.UID != "")
	case event.FieldAPIVersion:
		return types.Bool(v.ref.APIVersion != "")
	case event.FieldResourceVersion:
		return types.Bool(v.ref.ResourceVersion != "")
	case event.FieldFieldPath:
		return types.Bool(v.ref.FieldPath != "")
	}
	return types.NewErr("no such field %q", string(name))
}

// Equal implements the [ref.Val] interface.
// References compare by content, so two fields describing the same
// object are equal whichever field they were reached through.
func (v *objectRefVal) Equal(other ref.Val) ref.Val {
	o, ok := other.(*objectRefVal)
	return types.Bool(ok && *v.ref == *o.ref)
}

func (v *objectRefVal) Type() ref.Type {
	return objectRefType
}

func (v *objectRefVal) Value() any {
	return v.ref
}

func (v *objectRefVal) ConvertToType(t ref.Type) ref.Val {
	return convertToType(objectRefType, t)
}

func (v *objectRefVal) ConvertToNative(typeDesc reflect.Type) (any, error) {
	return nil, noNativeConversion(objectRefType, typeDesc)
}

var (
	_ ref.Val            = (*seriesVal)(nil)
	_ traits.Indexer     = (*seriesVal)(nil)
	_ traits.FieldTester = (*seriesVal)(nil)
)

// seriesVal adapts the series of a repeating event as a CEL value.
// The wrapped series is never nil. An event that does not repeat
// reads as null instead.
type seriesVal struct {
	s *eventsv1.EventSeries
}

// Get implements the [traits.Indexer] interface.
func (v *seriesVal) Get(index ref.Val) ref.Val {
	name, ok := index.(types.String)
	if !ok {
		return types.ValOrErr(index, "field name is not a string")
	}
	switch string(name) {
	case event.FieldCount:
		return types.Int(v.s.Count)
	case event.FieldLastObservedTime:
		return types.Timestamp{Time: v.s.LastObservedTime.Time}
	}
	return types.NewErr("no such field %q", string(name))
}

// IsSet implements the [traits.FieldTester] interface.
// A field counts as set when it holds something, a zero count and
// a zero time read as absent.
func (v *seriesVal) IsSet(field ref.Val) ref.Val {
	name, ok := field.(types.String)
	if !ok {
		return types.ValOrErr(field, "field name is not a string")
	}
	switch string(name) {
	case event.FieldCount:
		return types.Bool(v.s.Count != 0)
	case event.FieldLastObservedTime:
		return types.Bool(!v.s.LastObservedTime.IsZero())
	}
	return types.NewErr("no such field %q", string(name))
}

// Equal implements the [ref.Val] interface.
// A series compares by content.
func (v *seriesVal) Equal(other ref.Val) ref.Val {
	o, ok := other.(*seriesVal)
	if !ok {
		return types.False
	}
	return types.Bool(
		v.s.Count == o.s.Count && v.s.LastObservedTime.Time.Equal(o.s.LastObservedTime.Time),
	)
}

func (v *seriesVal) Type() ref.Type {
	return seriesType
}

func (v *seriesVal) Value() any {
	return v.s
}

func (v *seriesVal) ConvertToType(t ref.Type) ref.Val {
	return convertToType(seriesType, t)
}

func (v *seriesVal) ConvertToNative(typeDesc reflect.Type) (any, error) {
	return nil, noNativeConversion(seriesType, typeDesc)
}

var (
	_ ref.Val            = (*regardingObjectVal)(nil)
	_ traits.Indexer     = (*regardingObjectVal)(nil)
	_ traits.FieldTester = (*regardingObjectVal)(nil)
)

// regardingObjectVal adapts the metadata of the object an event is
// about as a CEL value. The wrapped metadata is never nil. An event
// the source did not enrich reads as null instead.
//
// The label and annotation views are built on first use, so a reset
// must clear them or the next event reads the previous one's metadata.
type regardingObjectVal struct {
	obj         *event.RegardingObject
	owner       objectRefVal
	labels      ref.Val
	annotations ref.Val
}

// Get implements the [traits.Indexer] interface.
func (v *regardingObjectVal) Get(index ref.Val) ref.Val {
	name, ok := index.(types.String)
	if !ok {
		return types.ValOrErr(index, "field name is not a string")
	}
	switch string(name) {
	case event.FieldLabels:
		if v.labels == nil {
			v.labels = types.NewStringStringMap(types.DefaultTypeAdapter, v.obj.Labels)
		}
		return v.labels
	case event.FieldAnnotations:
		if v.annotations == nil {
			v.annotations = types.NewStringStringMap(types.DefaultTypeAdapter, v.obj.Annotations)
		}
		return v.annotations
	case event.FieldOwner:
		if v.obj.Owner == nil {
			return types.NullValue
		}
		v.owner.ref = v.obj.Owner
		return &v.owner
	case event.FieldTerminating:
		return types.Bool(v.obj.Terminating)
	}
	return types.NewErr("no such field %q", string(name))
}

// IsSet implements the [traits.FieldTester] interface.
// A field counts as set when it holds something, an empty map and
// a false flag read as absent.
func (v *regardingObjectVal) IsSet(field ref.Val) ref.Val {
	name, ok := field.(types.String)
	if !ok {
		return types.ValOrErr(field, "field name is not a string")
	}
	switch string(name) {
	case event.FieldLabels:
		return types.Bool(len(v.obj.Labels) > 0)
	case event.FieldAnnotations:
		return types.Bool(len(v.obj.Annotations) > 0)
	case event.FieldOwner:
		return types.Bool(v.obj.Owner != nil)
	case event.FieldTerminating:
		return types.Bool(v.obj.Terminating)
	}
	return types.NewErr("no such field %q", string(name))
}

// Equal implements the [ref.Val] interface.
// Enriched metadata compares by content, maps included.
func (v *regardingObjectVal) Equal(other ref.Val) ref.Val {
	o, ok := other.(*regardingObjectVal)
	if !ok {
		return types.False
	}
	return types.Bool(
		v.obj.Terminating == o.obj.Terminating &&
			equalRefs(v.obj.Owner, o.obj.Owner) &&
			maps.Equal(v.obj.Labels, o.obj.Labels) &&
			maps.Equal(v.obj.Annotations, o.obj.Annotations),
	)
}

func (v *regardingObjectVal) Type() ref.Type {
	return regardingObjectType
}

func (v *regardingObjectVal) Value() any {
	return v.obj
}

func (v *regardingObjectVal) ConvertToType(t ref.Type) ref.Val {
	return convertToType(regardingObjectType, t)
}

func (v *regardingObjectVal) ConvertToNative(typeDesc reflect.Type) (any, error) {
	return nil, noNativeConversion(regardingObjectType, typeDesc)
}

// equalRefs reports whether two object references point to the same object.
// An absent reference matches only another absent one.
func equalRefs(a, b *corev1.ObjectReference) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// convertToType converts a wrapped value to another type.
// Only type(x) works, giving back the value's own type. Anything
// else, such as string(x), is an error.
func convertToType(self *types.Type, t ref.Type) ref.Val {
	if t == types.TypeType {
		return self
	}
	return types.NewErr("no conversion from %q to %q", self.TypeName(), t.TypeName())
}

// noNativeConversion refuses to turn a wrapped value back into
// a Go value. Expressions only read fields off these wrappers,
// so nothing asks for one.
func noNativeConversion(self *types.Type, typeDesc reflect.Type) error {
	return fmt.Errorf("no conversion from %q to %q", self.TypeName(), typeDesc)
}
