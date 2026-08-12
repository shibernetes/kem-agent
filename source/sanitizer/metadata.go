package sanitizer

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/shibernetes/kem-agent/event"
)

var _ MetadataSanitizer = lastAppliedConfig{}

// lastAppliedConfig drops the annotation kubectl writes to record an
// object as it was applied. That annotation mirrors the whole object,
// so on a Secret it carries the secret data itself.
type lastAppliedConfig struct{}

// Sanitize implements the [MetadataSanitizer] interface.
func (lastAppliedConfig) Sanitize(m *event.ObjectMeta) {
	delete(m.Annotations, corev1.LastAppliedConfigAnnotation)
}

var _ MetadataSanitizer = metadataCountLimit{}

// metadataCountLimit caps how many labels and annotations are kept and
// drops the rest. The API bounds annotations by their total size only,
// which admits tens of thousands of small ones, and does not bound the
// label count at all.
type metadataCountLimit struct {
	maxLabels      int
	maxAnnotations int
}

// Sanitize implements the [MetadataSanitizer] interface.
func (s metadataCountLimit) Sanitize(m *event.ObjectMeta) {
	m.Labels = capMapEntries(m.Labels, s.maxLabels)
	m.Annotations = capMapEntries(m.Annotations, s.maxAnnotations)
}

var _ MetadataSanitizer = annotationValueLimit{}

// annotationValueLimit shortens annotation values. The API limits an
// object's annotations at 256 KiB in total, but no single value, so
// one value can in practice be very large.
type annotationValueLimit struct {
	maxBytes int
}

// Sanitize implements the [MetadataSanitizer] interface.
func (s annotationValueLimit) Sanitize(m *event.ObjectMeta) {
	truncateValues(m.Annotations, s.maxBytes)
}
