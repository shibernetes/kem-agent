package sanitizer

import (
	"testing"
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
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
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
		"negative label limit":      {MetadataCountLimit: MetadataCountLimit{MaxLabels: -1}},
		"negative annotation limit": {MetadataCountLimit: MetadataCountLimit{MaxAnnotations: -1}},
		"negative value limit":      {AnnotationValueLimit: AnnotationValueLimit{MaxBytes: -1}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Error("got no error, want the config to be rejected")
			}
		})
	}
}
