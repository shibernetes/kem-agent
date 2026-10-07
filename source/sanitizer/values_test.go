package sanitizer

import (
	"testing"

	"github.com/shibernetes/kem-agent/event"
)

func TestFieldValuesSanitize(t *testing.T) {
	cases := map[string]struct {
		allowed []string
		value   string
		want    string
	}{
		"allowed value":           {allowed: []string{"Normal", "Warning"}, value: "Warning", want: "Warning"},
		"value not allowed":       {allowed: []string{"Normal", "Warning"}, value: "Error", want: ""},
		"value differing by case": {allowed: []string{"Warning"}, value: "warning", want: ""},
		"empty allowlist":         {allowed: []string{}, value: "Normal", want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ev := &event.Event{Type: tc.value}
			newFieldValues(FieldAllowlists{event.FieldType: tc.allowed}).Sanitize(ev)

			if ev.Type != tc.want {
				t.Errorf("got type %q, want %q", ev.Type, tc.want)
			}
		})
	}
}

func TestFieldValuesAccessesEachField(t *testing.T) {
	for _, name := range restrictedFields {
		ev := &event.Event{
			ReportingController: "kubelet",
			ReportingInstance:   "node-001",
			Action:              "Pull",
			Reason:              "Pulling",
			Type:                "Normal",
		}
		newFieldValues(FieldAllowlists{name: {}}).Sanitize(ev)

		for _, other := range restrictedFields {
			got, _ := ev.Field(other)
			switch {
			case other == name && got != "":
				t.Errorf("%s is %q, want it cleared", name, got)
			case other != name && got == "":
				t.Errorf("clearing %s also cleared %s", name, other)
			}
		}
	}
}
