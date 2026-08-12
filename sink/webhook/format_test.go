package webhook

import (
	"testing"
)

func TestFormatValidate(t *testing.T) {
	cases := map[string]struct {
		format Format
		valid  bool
	}{
		"json list":   {FormatJSONList, true},
		"ndjson":      {FormatNDJSON, true},
		"cloudevents": {FormatCloudEvents, true},
		"template":    {FormatTemplate, true},
		"unset":       {"", false},
		"misspelled":  {"ndjsn", false},
		"uppercase":   {"NDJSON", false},
		"underscored": {"json_list", false},
		"unsupported": {"protobuf", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.format.Validate()
			switch {
			case tc.valid && err != nil:
				t.Errorf("got %v, want the format accepted", err)
			case !tc.valid && err == nil:
				t.Error("got no error, want the format rejected")
			}
		})
	}
}

func TestFormatString(t *testing.T) {
	cases := map[Format]string{
		FormatJSONList:    "json-list",
		FormatNDJSON:      "ndjson",
		FormatCloudEvents: "cloudevents",
		FormatTemplate:    "template",
	}
	for format, want := range cases {
		if got := format.String(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestFormat_JSONSchema(t *testing.T) {
	schema := Format("").JSONSchema()

	if schema.Type != "string" {
		t.Errorf("got type %q, want %q", schema.Type, "string")
	}
	if len(schema.Enum) != len(formats) {
		t.Fatalf("got %d formats, want %d", len(schema.Enum), len(formats))
	}
	for _, v := range schema.Enum {
		name, ok := v.(string)
		if !ok {
			t.Fatalf("got type %T, want a string", v)
		}
		if err := Format(name).Validate(); err != nil {
			t.Errorf("the schema offers %q, which validation rejects: %v", name, err)
		}
	}
}
