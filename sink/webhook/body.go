package webhook

import (
	"encoding/json/jsontext"
	"time"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/identity"
	"github.com/shibernetes/kem-agent/internal/version"
	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/sink/internal/shared"
)

const (
	cloudEventsSpecVersion     = "1.0"
	cloudEventsDataContentType = "application/json"
)

var (
	_ sink.Encoder = jsonEncoder{}
	_ sink.Encoder = cloudEventsEncoder{}
	_ sink.Framer  = arrayFramer{}
	_ sink.Framer  = linesFramer{}
	_ sink.Framer  = separatorFramer{}
)

// newEncoder returns the encoder writing the frames of the
// configured format.
func newEncoder(cfg Config, meta identity.AgentMetadata) (sink.Encoder, error) {
	switch cfg.Format {
	case FormatCloudEvents:
		return newCloudEventsEncoder(cfg.CloudEvents), nil
	case FormatTemplate:
		tmpl, err := newTemplate(cfg.Template, meta)
		if err != nil {
			return nil, err
		}
		return tmpl, nil
	}
	return jsonEncoder{}, nil
}

// newFramer returns the framer composing the frames of the
// configured format into one request body.
func newFramer(cfg Config) sink.Framer {
	switch cfg.Format {
	case FormatNDJSON:
		return linesFramer{separator: []byte{'\n'}}
	case FormatTemplate:
		return separatorFramer{separator: []byte(cfg.separator())}
	}
	return arrayFramer{separator: []byte{','}}
}

// A jsonEncoder writes an event as a JSON value.
type jsonEncoder struct{}

// AppendEvent implements the [sink.Encoder] interface.
func (jsonEncoder) AppendEvent(dst []byte, ev *event.Event) []byte {
	return appendValue(dst, func(enc *jsontext.Encoder) error {
		return ev.MarshalJSONTo(enc)
	})
}

// A cloudEventsEncoder writes an event as one CloudEvent in the JSON
// format, carrying the event itself as its data.
type cloudEventsEncoder struct {
	source    string
	eventType string
}

// newCloudEventsEncoder returns an encoder wrapping events as
// CloudEvents, naming the agent as the source when the configuration
// does not name one.
func newCloudEventsEncoder(cfg CloudEvents) cloudEventsEncoder {
	source := cfg.Source
	if source == "" {
		source = version.Name
	}
	return cloudEventsEncoder{source: source, eventType: cfg.Type}
}

// AppendEvent implements the [sink.Encoder] interface.
func (e cloudEventsEncoder) AppendEvent(dst []byte, ev *event.Event) []byte {
	return appendValue(dst, func(enc *jsontext.Encoder) error {
		_ = enc.WriteToken(jsontext.BeginObject)

		writeString(enc, "specversion", cloudEventsSpecVersion)
		writeString(enc, "id", string(ev.UID)+"/"+ev.ResourceVersion)
		writeString(enc, "source", e.source)
		writeString(enc, "type", e.eventType)
		writeString(enc, "time", ev.Time().Format(time.RFC3339Nano))
		writeString(enc, "datacontenttype", cloudEventsDataContentType)

		_ = enc.WriteToken(jsontext.String("data"))
		if err := ev.MarshalJSONTo(enc); err != nil {
			return err
		}
		return enc.WriteToken(jsontext.EndObject)
	})
}

func writeString(enc *jsontext.Encoder, key, value string) {
	_ = enc.WriteToken(jsontext.String(key))
	_ = enc.WriteToken(jsontext.String(value))
}

// appendValue appends the JSON value written by fn to dst. It drops
// the newline the encoder terminates a top-level value with, since
// the framer supplies the wrapping chars instead.
func appendValue(dst []byte, fn func(*jsontext.Encoder) error) []byte {
	out := shared.AppendJSON(dst, fn)
	if len(out) > len(dst) && out[len(out)-1] == '\n' {
		return out[:len(out)-1]
	}
	return out
}

// An arrayFramer composes frames into one JSON array.
type arrayFramer struct {
	separator []byte
}

// Separator implements the [sink.Framer] interface.
func (f arrayFramer) Separator() []byte {
	return f.separator
}

// Compose implements the [sink.Framer] interface.
func (arrayFramer) Compose(dst, frames []byte, _ int) []byte {
	dst = append(dst, '[')
	dst = append(dst, frames...)

	return append(dst, ']')
}

// Fixed implements the [sink.Framer] interface.
func (arrayFramer) Fixed() int {
	return 2
}

// A linesFramer composes frames into newline-delimited lines.
type linesFramer struct {
	separator []byte
}

// Separator implements the [sink.Framer] interface.
func (f linesFramer) Separator() []byte {
	return f.separator
}

// Compose implements the [sink.Framer] interface.
func (f linesFramer) Compose(dst, frames []byte, _ int) []byte {
	dst = append(dst, frames...)

	return append(dst, f.separator...)
}

// Fixed implements the [sink.Framer] interface.
func (f linesFramer) Fixed() int {
	return len(f.separator)
}

// A separatorFramer joins frames with the configured separator
// character sequence, and doesn't wrap them.
type separatorFramer struct {
	separator []byte
}

// Separator implements the [sink.Framer] interface.
func (f separatorFramer) Separator() []byte {
	return f.separator
}

// Compose implements the [sink.Framer] interface.
func (separatorFramer) Compose(dst, frames []byte, _ int) []byte {
	return append(dst, frames...)
}

// Fixed implements the [sink.Framer] interface.
func (separatorFramer) Fixed() int {
	return 0
}
