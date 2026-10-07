package sanitizer

import (
	"slices"

	"github.com/shibernetes/kem-agent/event"
)

// fieldAccessors returns the pointer of each event field that an
// allowlist can constrain, keyed by their name.
var fieldAccessors = map[string]func(*event.Event) *string{
	event.FieldType:                func(ev *event.Event) *string { return &ev.Type },
	event.FieldReason:              func(ev *event.Event) *string { return &ev.Reason },
	event.FieldAction:              func(ev *event.Event) *string { return &ev.Action },
	event.FieldReportingController: func(ev *event.Event) *string { return &ev.ReportingController },
	event.FieldReportingInstance:   func(ev *event.Event) *string { return &ev.ReportingInstance },
}

var _ EventSanitizer = fieldValues{}

// fieldValues clears an event field whose value isn't allowed.
type fieldValues struct {
	allowlists []allowlist
}

type allowlist struct {
	field  func(*event.Event) *string
	values []string
}

// newFieldValues builds the sanitizer from validated allowlists.
func newFieldValues(f FieldAllowlists) fieldValues {
	s := fieldValues{allowlists: make([]allowlist, 0, len(f))}
	for name, values := range f {
		s.allowlists = append(s.allowlists, allowlist{
			field:  fieldAccessors[name],
			values: values,
		})
	}
	return s
}

// Sanitize implements the [EventSanitizer] interface.
func (s fieldValues) Sanitize(ev *event.Event) {
	for _, a := range s.allowlists {
		if p := a.field(ev); *p != "" && !slices.Contains(a.values, *p) {
			*p = ""
		}
	}
}
