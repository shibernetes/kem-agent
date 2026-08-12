package sanitizer

import (
	"maps"
	"strconv"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/shibernetes/kem-agent/event"
)

const (
	lastApplied = corev1.LastAppliedConfigAnnotation
)

func TestLastAppliedConfig(t *testing.T) {
	cases := map[string]struct {
		annotations map[string]string
		want        map[string]string
	}{
		"among other annotations": {
			annotations: map[string]string{lastApplied: "{}", "team": "platform"},
			want:        map[string]string{"team": "platform"},
		},
		"the only annotation": {
			annotations: map[string]string{lastApplied: "{}"},
			want:        map[string]string{},
		},
		"never written": {
			annotations: map[string]string{"team": "platform"},
			want:        map[string]string{"team": "platform"},
		},
		"no annotations at all": {
			annotations: nil,
			want:        nil,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := &event.ObjectMeta{Annotations: tc.annotations}
			lastAppliedConfig{}.Sanitize(m)

			if !maps.Equal(m.Annotations, tc.want) {
				t.Errorf("got %v, want %v", m.Annotations, tc.want)
			}
		})
	}
}

func TestMetadataCountLimit(t *testing.T) {
	cases := map[string]struct {
		limit           metadataCountLimit
		wantLabels      int
		wantAnnotations int
	}{
		// The two limits differ, so a crossed pair caps both maps
		// at the other one's size rather than going unnoticed.
		"limit on each map": {
			limit:           metadataCountLimit{maxLabels: 2, maxAnnotations: 5},
			wantLabels:      2,
			wantAnnotations: 5,
		},
		"no limit on labels": {
			limit:           metadataCountLimit{maxAnnotations: 5},
			wantLabels:      10,
			wantAnnotations: 5,
		},
		"no limit on annotations": {
			limit:           metadataCountLimit{maxLabels: 2},
			wantLabels:      2,
			wantAnnotations: 10,
		},
		"limits above the entry count": {
			limit:           metadataCountLimit{maxLabels: 20, maxAnnotations: 20},
			wantLabels:      10,
			wantAnnotations: 10,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := &event.ObjectMeta{
				Labels:      entries(10),
				Annotations: entries(10),
			}
			tc.limit.Sanitize(m)

			if got := len(m.Labels); got != tc.wantLabels {
				t.Errorf("got %d labels, want %d", got, tc.wantLabels)
			}
			if got := len(m.Annotations); got != tc.wantAnnotations {
				t.Errorf("got %d annotations, want %d", got, tc.wantAnnotations)
			}
		})
	}
}

func TestAnnotationValueLimit(t *testing.T) {
	m := &event.ObjectMeta{
		Labels:      map[string]string{"a": "foobar"},
		Annotations: map[string]string{"a": "foobar"},
	}
	annotationValueLimit{maxBytes: 3}.Sanitize(m)

	if got := m.Annotations["a"]; got != "foo" {
		t.Errorf("got annotation %q, want %q", got, "foo")
	}
	// It must not touch labels, whose values are
	// already capped at 63 bytes by the API.
	if got := m.Labels["a"]; got != "foobar" {
		t.Errorf("got label %q, want it untouched", got)
	}
}

func entries(n int) map[string]string {
	m := make(map[string]string, n)
	for i := range n {
		m[strconv.Itoa(i)] = "v"
	}
	return m
}
