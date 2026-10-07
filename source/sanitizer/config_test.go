package sanitizer

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/shibernetes/kem-agent/event"
)

func TestDefaultConfig(t *testing.T) {
	want := Config{
		FieldLimits:       FieldLimits{Enabled: true},
		LastAppliedConfig: LastAppliedConfig{Enabled: true},
		MetadataCountLimit: MetadataCountLimit{
			Enabled:        true,
			MaxLabels:      100,
			MaxAnnotations: 100,
		},
		AnnotationValueLimit: AnnotationValueLimit{
			Enabled:  true,
			MaxBytes: 4 << 10, // 4 KiB
		},
	}
	got := DefaultConfig()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the default configuration differs (-want +got):\n%s", diff)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("the default configuration does not validate: %v", err)
	}
}

func TestConfigValidates(t *testing.T) {
	cases := map[string]Config{
		"zero limits": {
			MetadataCountLimit:   MetadataCountLimit{Enabled: true},
			AnnotationValueLimit: AnnotationValueLimit{Enabled: true},
		},
		"limits set": {
			MetadataCountLimit:   MetadataCountLimit{Enabled: true, MaxLabels: 1, MaxAnnotations: 1},
			AnnotationValueLimit: AnnotationValueLimit{Enabled: true, MaxBytes: 1},
		},
		"every sanitizer off": {},
		"field values allowed": {
			FieldValues: FieldValues{Enabled: true, Fields: FieldAllowlists{event.FieldType: {"Normal", "Warning"}}},
		},
		"no field value allowed": {
			FieldValues: FieldValues{Enabled: true, Fields: FieldAllowlists{event.FieldReason: {}}},
		},
		"field values listed while disabled": {
			FieldValues: FieldValues{Fields: FieldAllowlists{event.FieldType: {"Normal"}}},
		},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err != nil {
				t.Errorf("got %v, want the config to validate", err)
			}
		})
	}
}

func TestConfigRejects(t *testing.T) {
	// The blocks are left disabled, since a limit is
	// validated even when its sanitizer is disabled.
	cases := map[string]Config{
		"negative label limit":                 {MetadataCountLimit: MetadataCountLimit{MaxLabels: -1}},
		"negative annotation limit":            {MetadataCountLimit: MetadataCountLimit{MaxAnnotations: -1}},
		"negative value limit":                 {AnnotationValueLimit: AnnotationValueLimit{MaxBytes: -1}},
		"field values enabled without a field": {FieldValues: FieldValues{Enabled: true}},
		"unknown field":                        {FieldValues: FieldValues{Fields: FieldAllowlists{event.FieldNote: {"x"}}}},
		"empty value":                          {FieldValues: FieldValues{Fields: FieldAllowlists{event.FieldType: {""}}}},
		"blank value":                          {FieldValues: FieldValues{Fields: FieldAllowlists{event.FieldType: {"  "}}}},
		"value with surrounding whitespace":    {FieldValues: FieldValues{Fields: FieldAllowlists{event.FieldType: {" Warning"}}}},
		"duplicate value":                      {FieldValues: FieldValues{Fields: FieldAllowlists{event.FieldType: {"Normal", "Normal"}}}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Error("got no error, want the config to be rejected")
			}
		})
	}
}
