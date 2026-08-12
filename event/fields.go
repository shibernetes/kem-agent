package event

// The field names an [Event] is written, filtered and addressed under.
// They are declared once and used by each layer. The encoder writes them,
// the Field method matches a path built from them, and the CEL provider
// declares them on its types, so a rename is one edit and all three follow.
const (
	FieldUID                 = "uid"
	FieldResourceVersion     = "resourceVersion"
	FieldNamespace           = "namespace"
	FieldName                = "name"
	FieldLabels              = "labels"
	FieldAnnotations         = "annotations"
	FieldEventTime           = "eventTime"
	FieldSeries              = "series"
	FieldReportingController = "reportingController"
	FieldReportingInstance   = "reportingInstance"
	FieldAction              = "action"
	FieldReason              = "reason"
	FieldRegarding           = "regarding"
	FieldRelated             = "related"
	FieldNote                = "note"
	FieldType                = "type"
	FieldRegardingObject     = "regardingObject"
)

// The field names of the sub-objects an event carries.
const (
	FieldCount            = "count"
	FieldLastObservedTime = "lastObservedTime"
	FieldKind             = "kind"
	FieldAPIVersion       = "apiVersion"
	FieldFieldPath        = "fieldPath"
	FieldOwner            = "owner"
	FieldTerminating      = "terminating"
)
