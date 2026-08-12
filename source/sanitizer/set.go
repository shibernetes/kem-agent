package sanitizer

import (
	"github.com/shibernetes/kem-agent/event"
)

// Set is a list of sanitizers built from config, in the order they run.
// One set serves every watch, and its members are stateless, so several
// goroutines may run them at once.
type Set struct {
	events   []EventSanitizer
	metadata []MetadataSanitizer
}

// New builds the sanitizers the configuration turns on. One whose limits
// are all zero can never change anything, so it is skipped rather than
// called for every event.
func New(cfg Config) *Set {
	s := &Set{}
	if cfg.FieldLimits.Enabled {
		s.events = append(s.events, fieldLimits{})
	}
	// The metadata sanitizers run in the order they are added here.
	// Dropping the last-applied annotation first spares the count limit
	// a slot on an entry that will be removed, and capping the count
	// before the values leaves far fewer values to shorten.
	if cfg.LastAppliedConfig.Enabled {
		s.metadata = append(s.metadata, lastAppliedConfig{})
	}
	if c := cfg.MetadataCountLimit; c.Enabled && (c.MaxLabels > 0 || c.MaxAnnotations > 0) {
		s.metadata = append(s.metadata, metadataCountLimit{
			maxLabels:      c.MaxLabels,
			maxAnnotations: c.MaxAnnotations,
		})
	}
	if c := cfg.AnnotationValueLimit; c.Enabled && c.MaxBytes > 0 {
		s.metadata = append(s.metadata, annotationValueLimit{maxBytes: int(c.MaxBytes)})
	}
	return s
}

// Sanitize runs every sanitizer over the event, its own labels and
// annotations included. It skips the metadata of an enriched object,
// which the enricher sanitizes when it caches it.
func (s *Set) Sanitize(ev *event.Event) {
	for _, san := range s.events {
		san.Sanitize(ev)
	}
	for _, san := range s.metadata {
		san.Sanitize(&ev.ObjectMeta)
	}
}

// Metadata returns the sanitizers that run on an object's metadata,
// for the enricher to apply to the projection it builds.
func (s *Set) Metadata() []MetadataSanitizer {
	return s.metadata
}
