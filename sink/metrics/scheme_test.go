package metrics

import (
	"testing"
)

func TestSchemeValidate(t *testing.T) {
	cases := map[string]struct {
		scheme NameValidationScheme
		valid  bool
	}{
		"legacy":      {NameValidationLegacy, true},
		"utf8":        {NameValidationUTF8, true},
		"unset":       {"", false},
		"hyphen":      {"utf-8", false},
		"uppercase":   {"UTF8", false},
		"capitalized": {"Legacy", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.scheme.Validate()
			switch {
			case tc.valid && err != nil:
				t.Errorf("got %v, want the scheme accepted", err)
			case !tc.valid && err == nil:
				t.Error("got no error, want the scheme rejected")
			}
		})
	}
}

func TestSchemeString(t *testing.T) {
	cases := map[NameValidationScheme]string{
		NameValidationLegacy: "legacy",
		NameValidationUTF8:   "utf8",
	}
	for scheme, want := range cases {
		if got := scheme.String(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestSchemeValidatesMetricNames(t *testing.T) {
	cases := map[string]struct {
		scheme NameValidationScheme
		name   string
		valid  bool
	}{
		"legacy accepts an underscored name": {NameValidationLegacy, "kem_events_warnings", true},
		"legacy accepts a colon":             {NameValidationLegacy, "a:b", true},
		"legacy refuses a dash":              {NameValidationLegacy, "kem_events_my-warnings", false},
		"legacy refuses a leading digit":     {NameValidationLegacy, "0abc", false},
		"legacy refuses a non-ASCII rune":    {NameValidationLegacy, "kem_events_héllo", false},
		"legacy refuses an empty name":       {NameValidationLegacy, "", false},
		"utf8 accepts a dash":                {NameValidationUTF8, "kem_events_my-warnings", true},
		"utf8 accepts a non-ASCII rune":      {NameValidationUTF8, "kem_events_héllo", true},
		"utf8 refuses an empty name":         {NameValidationUTF8, "", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			switch got := tc.scheme.ValidMetricName(tc.name); {
			case got && !tc.valid:
				t.Errorf("%q was accepted by %s, want it refused", tc.name, tc.scheme)
			case !got && tc.valid:
				t.Errorf("%q was refused by %s, want it accepted", tc.name, tc.scheme)
			}
		})
	}
}

func TestSchemeValidatesLabelNames(t *testing.T) {
	cases := map[string]struct {
		scheme NameValidationScheme
		label  string
		valid  bool
	}{
		"legacy accepts a plain name":         {NameValidationLegacy, "ns", true},
		"legacy accepts a leading underscore": {NameValidationLegacy, "_ok", true},
		"legacy accepts the reserved prefix":  {NameValidationLegacy, "__reserved", true},
		"legacy refuses a dash":               {NameValidationLegacy, "a-b", false},
		"legacy refuses an empty name":        {NameValidationLegacy, "", false},
		"utf8 accepts a dash":                 {NameValidationUTF8, "a-b", true},
		"utf8 refuses an empty name":          {NameValidationUTF8, "", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			switch got := tc.scheme.ValidLabelName(tc.label); {
			case got && !tc.valid:
				t.Errorf("%q was accepted by %s, want it refused", tc.label, tc.scheme)
			case !got && tc.valid:
				t.Errorf("%q was refused by %s, want it accepted", tc.label, tc.scheme)
			}
		})
	}
}

// TestSchemeFallsBackToLegacy asserts that an unvalidated scheme
// falls back to the legacy one, since the Prometheus scheme panics on
// its own unset value.
func TestSchemeFallsBackToLegacy(t *testing.T) {
	cases := map[string]NameValidationScheme{
		"the zero value": "",
		"an unknown one": "utf-8",
	}
	for name, scheme := range cases {
		t.Run(name, func(t *testing.T) {
			if scheme.ValidMetricName("my-warnings") {
				t.Error("a dashed metric name was accepted, want the strict scheme applied")
			}
			if scheme.ValidLabelName("a-b") {
				t.Error("a dashed label name was accepted, want the strict scheme applied")
			}
		})
	}
}

func TestScheme_JSONSchema(t *testing.T) {
	schema := NameValidationScheme("").JSONSchema()

	if schema.Type != "string" {
		t.Errorf("got type %q, want %q", schema.Type, "string")
	}
	if len(schema.Enum) != len(nameValidationSchemes) {
		t.Fatalf("got %d schemes, want %d", len(schema.Enum), len(nameValidationSchemes))
	}
	for _, v := range schema.Enum {
		name, ok := v.(string)
		if !ok {
			t.Fatalf("got type %T, want a string", v)
		}
		if err := NameValidationScheme(name).Validate(); err != nil {
			t.Errorf("the schema offers %q, which validation rejects: %v", name, err)
		}
	}
}
