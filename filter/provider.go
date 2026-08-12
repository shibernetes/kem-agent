package filter

import (
	"fmt"

	"github.com/google/cel-go/common/types"

	"github.com/shibernetes/kem-agent/event"
)

// The CEL type names of the event and of the objects it carries.
// They appear in an expression's type errors, and only have to be
// unique within the environment.
const (
	eventTypeName           = "kem.Event"
	objectRefTypeName       = "kem.ObjectReference"
	seriesTypeName          = "kem.EventSeries"
	regardingObjectTypeName = "kem.RegardingObject"
)

// The object types an expression can navigate into.
var (
	eventType           = types.NewObjectType(eventTypeName)
	objectRefType       = types.NewObjectType(objectRefTypeName)
	seriesType          = types.NewObjectType(seriesTypeName)
	regardingObjectType = types.NewObjectType(regardingObjectTypeName)
)

// The fields declared on each object type. An expression is checked
// against these names and types, and the matching value wrapper returns
// them at evaluation, so a field added to one needs adding to the other.
var (
	eventFields = map[string]*types.Type{
		event.FieldUID:                 types.StringType,
		event.FieldResourceVersion:     types.StringType,
		event.FieldNamespace:           types.StringType,
		event.FieldName:                types.StringType,
		event.FieldLabels:              types.NewMapType(types.StringType, types.StringType),
		event.FieldAnnotations:         types.NewMapType(types.StringType, types.StringType),
		event.FieldEventTime:           types.TimestampType,
		event.FieldSeries:              seriesType,
		event.FieldReportingController: types.StringType,
		event.FieldReportingInstance:   types.StringType,
		event.FieldAction:              types.StringType,
		event.FieldReason:              types.StringType,
		event.FieldRegarding:           objectRefType,
		event.FieldRelated:             objectRefType,
		event.FieldNote:                types.StringType,
		event.FieldType:                types.StringType,
		event.FieldRegardingObject:     regardingObjectType,
	}
	objectRefFields = map[string]*types.Type{
		event.FieldKind:            types.StringType,
		event.FieldNamespace:       types.StringType,
		event.FieldName:            types.StringType,
		event.FieldUID:             types.StringType,
		event.FieldAPIVersion:      types.StringType,
		event.FieldResourceVersion: types.StringType,
		event.FieldFieldPath:       types.StringType,
	}
	seriesFields = map[string]*types.Type{
		event.FieldCount:            types.IntType,
		event.FieldLastObservedTime: types.TimestampType,
	}
	regardingObjectFields = map[string]*types.Type{
		event.FieldLabels:      types.NewMapType(types.StringType, types.StringType),
		event.FieldAnnotations: types.NewMapType(types.StringType, types.StringType),
		event.FieldOwner:       objectRefType,
		event.FieldTerminating: types.BoolType,
	}
)

var _ types.Provider = (*provider)(nil)

// provider resolves the event types for the CEL checker.
// Every other type is left to the embedded registry, which also holds
// whatever the enabled extension libraries declare.
type provider struct {
	*types.Registry
}

func newProvider() (*provider, error) {
	reg, err := types.NewRegistry()
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL type registry: %w", err)
	}
	return &provider{Registry: reg}, nil
}

// FindStructType implements the [types.Provider] interface.
func (p *provider) FindStructType(name string) (*types.Type, bool) {
	if _, ok := fieldsOf(name); ok {
		return types.NewTypeTypeWithParam(types.NewObjectType(name)), true
	}
	return p.Registry.FindStructType(name)
}

// FindStructFieldNames implements the [types.Provider] interface.
func (p *provider) FindStructFieldNames(name string) ([]string, bool) {
	fields, ok := fieldsOf(name)
	if !ok {
		return p.Registry.FindStructFieldNames(name)
	}
	names := make([]string, 0, len(fields))
	for n := range fields {
		names = append(names, n)
	}
	return names, true
}

// FindStructFieldType implements the [types.Provider] interface.
func (p *provider) FindStructFieldType(name, field string) (*types.FieldType, bool) {
	fields, ok := fieldsOf(name)
	if !ok {
		return p.Registry.FindStructFieldType(name, field)
	}
	t, ok := fields[field]
	if !ok {
		return nil, false
	}
	// IsSet and GetFrom are left unset on purpose. That routes
	// every field read through the value wrappers' Indexer and
	// FieldTester rather than through a reflection-based accessor.
	return &types.FieldType{Type: t}, true
}

func fieldsOf(name string) (map[string]*types.Type, bool) {
	switch name {
	case eventTypeName:
		return eventFields, true
	case objectRefTypeName:
		return objectRefFields, true
	case seriesTypeName:
		return seriesFields, true
	case regardingObjectTypeName:
		return regardingObjectFields, true
	}
	return nil, false
}
