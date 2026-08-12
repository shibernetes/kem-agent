package webhook

import (
	"encoding/json/jsontext"
	"fmt"
	"strings"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/identity"
	"github.com/shibernetes/kem-agent/sink"
)

const (
	tagOpen     = "{{"
	tagClose    = "}}"
	agentPrefix = "agent."
)

var _ sink.Encoder = (*template)(nil)

// A template renders an event into a request body,
// substituting the event's fields for its tags.
type template struct {
	segments []segment
	suffix   string
}

// A segment groups a literal text and the tag rendered after it.
type segment struct {
	text string
	tag  string
}

// newTemplate compiles the template text against the fields
// an event exposes. A misspelled or unknown tag is refused.
func newTemplate(s string, meta identity.AgentMetadata) (*template, error) {
	var (
		tmpl template
		sb   strings.Builder
		read int
	)
	for {
		prefix, rest, found := strings.Cut(s, tagOpen)
		if !found {
			break
		}
		sb.WriteString(prefix)

		// The index of the tag in the original string, which is what
		// an error points at once the text has been consumed past it.
		idx := read + len(prefix)

		raw, remainder, closed := strings.Cut(rest, tagClose)
		if !closed {
			return nil, fmt.Errorf("template: unclosed tag at index %d", idx)
		}
		s = remainder
		read = idx + len(tagOpen) + len(raw) + len(tagClose)

		tag := strings.TrimSpace(raw)
		if tag == "" {
			return nil, fmt.Errorf("template: empty tag at index %d", idx)
		}
		// The agent's identity is immutable, so its tags are folded
		// into the literal text around them and cost nothing per event.
		if v, ok := metadataFieldValue(meta, tag); ok {
			sb.Write(appendEscaped(nil, v))
			continue
		}
		if _, ok := (&event.Event{}).Field(tag); !ok {
			return nil, fmt.Errorf("template: unknown tag %q at index %d", tag, idx)
		}
		tmpl.segments = append(tmpl.segments, segment{text: sb.String(), tag: tag})
		sb.Reset()
	}
	sb.WriteString(s)
	tmpl.suffix = sb.String()

	return &tmpl, nil
}

// AppendEvent implements the [sink.Encoder] interface. It appends the
// rendered event to dst, and returns the extended buffer. It cannot fail,
// since all tags were resolved when the template was compiled. An absent
// field renders an empty value.
func (t *template) AppendEvent(dst []byte, ev *event.Event) []byte {
	for _, s := range t.segments {
		dst = append(dst, s.text...)
		val, _ := ev.Field(s.tag)
		dst = appendEscaped(dst, val)
	}
	return append(dst, t.suffix...)
}

// metadataFieldValue returns the content of the agent metadata
// field named by tag, and whether one was found.
func metadataFieldValue(id identity.AgentMetadata, tag string) (string, bool) {
	name, ok := strings.CutPrefix(tag, agentPrefix)
	if !ok {
		return "", false
	}
	switch name {
	case "cluster":
		return id.Cluster, true
	case "node":
		return id.Node, true
	case "namespace":
		return id.Namespace, true
	case "pod":
		return id.Pod, true
	case "version":
		return id.Version, true
	case "commit":
		return id.Commit, true
	}
	return "", false
}

// appendEscaped appends s to dst as an unquoted JSON string,
// and returns the extended buffer.
func appendEscaped(dst []byte, s string) []byte {
	i := len(dst)
	b, _ := jsontext.AppendQuote(dst, s)
	// A quoted string is at least a pair of quotes, so anything shorter
	// would make the reslice below run past the buffer.
	if len(b) < i+2 {
		return dst
	}
	n := copy(b[i:], b[i+1:len(b)-1])

	return b[:i+n]
}
