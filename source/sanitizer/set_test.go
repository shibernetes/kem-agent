package sanitizer

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/shibernetes/kem-agent/event"
)

func TestNewBuildsSanitizers(t *testing.T) {
	cases := map[string]struct {
		cfg          Config
		wantEvents   []string
		wantMetadata []string
	}{
		// The order of the metadata sanitizers is fixed. The last-applied
		// annotation is dropped first, the entry count capped next, and
		// only what survives is shortened at the end.
		"the defaults": {
			cfg:        DefaultConfig(),
			wantEvents: []string{"fieldLimits"},
			wantMetadata: []string{
				"lastAppliedConfig",
				"metadataCountLimit",
				"annotationValueLimit",
			},
		},
		"one sanitizer enabled": {
			cfg:          Config{LastAppliedConfig: LastAppliedConfig{Enabled: true}},
			wantMetadata: []string{"lastAppliedConfig"},
		},
		"count limit on labels alone": {
			cfg:          Config{MetadataCountLimit: MetadataCountLimit{Enabled: true, MaxLabels: 1}},
			wantMetadata: []string{"metadataCountLimit"},
		},
		"limits enabled but all zero": {
			cfg: Config{
				MetadataCountLimit:   MetadataCountLimit{Enabled: true},
				AnnotationValueLimit: AnnotationValueLimit{Enabled: true},
			},
		},
		"nothing enabled": {cfg: Config{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			set := New(tc.cfg)

			if got := typeNames(set.events); !slices.Equal(got, tc.wantEvents) {
				t.Errorf("got event sanitizers %v, want %v", got, tc.wantEvents)
			}
			if got := typeNames(set.Metadata()); !slices.Equal(got, tc.wantMetadata) {
				t.Errorf("got metadata sanitizers %v, want %v", got, tc.wantMetadata)
			}
		})
	}
}

func TestSetSanitizesEvent(t *testing.T) {
	ev := &event.Event{
		Labels: entries(150),
		Annotations: map[string]string{
			lastApplied: "{}",
			"team":      strings.Repeat("x", 8<<10),
		},
		Note: longValue(),
	}
	New(DefaultConfig()).Sanitize(ev)

	if len(ev.Note) != eventMaxNoteSize {
		t.Errorf("the note is %d bytes, want %d", len(ev.Note), eventMaxNoteSize)
	}
	if len(ev.Labels) != 100 {
		t.Errorf("got %d labels, want 100", len(ev.Labels))
	}
	if _, ok := ev.Annotations[lastApplied]; ok {
		t.Error("the last-applied annotation is still present")
	}
	if got := len(ev.Annotations["team"]); got != 4<<10 {
		t.Errorf("the 'team' annotation is %d bytes, want %d", got, 4<<10)
	}
}

func TestSetIgnoresEnrichedObject(t *testing.T) {
	regarding := &event.RegardingObject{
		Labels:      entries(150),
		Annotations: map[string]string{lastApplied: "{}"},
	}
	New(DefaultConfig()).Sanitize(&event.Event{RegardingObject: regarding})

	if len(regarding.Labels) != 150 {
		t.Errorf("got %d enriched labels, want all 150 kept", len(regarding.Labels))
	}
	if _, ok := regarding.Annotations[lastApplied]; !ok {
		t.Error("the enriched last-applied annotation was dropped, want it left to the enricher")
	}
}

// typeNames names the concrete sanitizer types within an interface
// slice, so a set can be compared by content and order.
func typeNames[T any](list []T) []string {
	names := make([]string, len(list))
	for i, v := range list {
		names[i] = strings.TrimPrefix(fmt.Sprintf("%T", v), "sanitizer.")
	}
	return names
}
