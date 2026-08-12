package sanitizer

import (
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/shibernetes/kem-agent/event"
)

// EventSanitizer sanitizes an event in place after decoding and
// before the source fans it out. That is the only place where
// mutating an event is allowed throughout its lifetime.
type EventSanitizer interface {
	Sanitize(*event.Event)
}

// MetadataSanitizer sanitizes an object's metadata in place.
// It runs on an event's own metadata after decoding, and on an
// enriched object's metadata. Since an object carries the same
// data an event does, both are held to the same limits.
type MetadataSanitizer interface {
	Sanitize(*event.ObjectMeta)
}

// truncateValues shortens every value of m over limit bytes.
// A limit of zero leaves the map untouched.
func truncateValues(m map[string]string, limit int) {
	if limit <= 0 {
		return
	}
	for k, v := range m {
		if len(v) > limit {
			m[k] = truncate(v, limit)
		}
	}
}

// truncate returns a copy of s shortened to at most limit bytes,
// or the original value when it already fits. The cut lands on a
// rune boundary rather than through one, since a split rune would
// hand invalid UTF-8 to every sink.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for i := 0; i < utf8.UTFMax-1 && limit > 0 && !utf8.RuneStart(s[limit]); i++ {
		limit--
	}
	return strings.Clone(s[:limit])
}

// capMapEntries returns a copy of m holding its first n entries
// in key order. It returns m unchanged when it already holds no
// more than n, or when n is zero.
func capMapEntries(m map[string]string, n int) map[string]string {
	if n <= 0 || len(m) <= n {
		return m
	}
	capped := make(map[string]string, n)
	for _, k := range slices.Sorted(maps.Keys(m))[:n] {
		capped[k] = m[k]
	}
	return capped
}
