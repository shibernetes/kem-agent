package webhook

import (
	"encoding/json"
	"testing"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/version"
	"github.com/shibernetes/kem-agent/sink"
)

func TestNewEncoder(t *testing.T) {
	cases := map[Format]string{
		FormatJSONList:    "json",
		FormatNDJSON:      "json",
		FormatCloudEvents: "cloudevents",
		FormatTemplate:    "template",
	}
	for format, want := range cases {
		t.Run(format.String(), func(t *testing.T) {
			var got string

			switch mustEncoder(t, format).(type) {
			case jsonEncoder:
				got = "json"
			case cloudEventsEncoder:
				got = "cloudevents"
			case *template:
				got = "template"
			}
			if got != want {
				t.Errorf("got %s encoder, want %s", got, want)
			}
		})
	}
}

// TestNewEncoderRejectsBadTemplate asserts that a template with
// one or more unknown tags fails to compile.
func TestNewEncoderRejectsBadTemplate(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Format, cfg.Template = FormatTemplate, `{{ unknown }}`

	if _, err := newEncoder(cfg, testAgentMetadata()); err == nil {
		t.Error("encoder built successfully, want the template refused")
	}
}

func TestNewFramer(t *testing.T) {
	cases := map[Format]struct {
		separator string
		fixed     int
	}{
		FormatJSONList:    {",", 2},
		FormatNDJSON:      {"\n", 1},
		FormatCloudEvents: {",", 2},
		FormatTemplate:    {"\n", 0},
	}
	for format, want := range cases {
		t.Run(format.String(), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Format = format

			framer := newFramer(cfg)
			if got := string(framer.Separator()); got != want.separator {
				t.Errorf("got separator %q, want %q", got, want.separator)
			}
			if got := framer.Fixed(); got != want.fixed {
				t.Errorf("got %d fixed bytes, want %d", got, want.fixed)
			}
		})
	}
}

// TestNewFramerTemplateSeparator asserts that the template format
// reads its separator from the configuration, an empty one included.
func TestNewFramerTemplateSeparator(t *testing.T) {
	cases := map[string]string{
		"custom": ";",
		"empty":  "",
	}
	for name, separator := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Format, cfg.Separator = FormatTemplate, &separator

			if got := string(newFramer(cfg).Separator()); got != separator {
				t.Errorf("got %q, want %q", got, separator)
			}
		})
	}
}

// TestJSONEncoderTrimsNewline asserts that a frame ends at the value,
// without the newline the shared encoder terminates it with.
func TestJSONEncoderTrimsNewline(t *testing.T) {
	frame := jsonEncoder{}.AppendEvent(nil, testEvent())
	t.Logf("frame: %s", frame)

	if len(frame) == 0 {
		t.Fatal("got an empty frame, want one JSON value")
	}
	if frame[len(frame)-1] == '\n' {
		t.Error("the frame ends with a newline, want it terminated at the value")
	}
	var doc map[string]any
	if err := json.Unmarshal(frame, &doc); err != nil {
		t.Fatalf("the frame is not one JSON value: %v", err)
	}
}

func TestEncoderExtendsBuffer(t *testing.T) {
	cases := map[string]sink.Encoder{
		"json":        jsonEncoder{},
		"cloudevents": newCloudEventsEncoder(CloudEvents{Type: "io.k8s.event"}),
	}
	for name, encoder := range cases {
		t.Run(name, func(t *testing.T) {
			got := encoder.AppendEvent([]byte("kept:"), testEvent())

			if string(got[:5]) != "kept:" {
				t.Errorf("got %q, want it appended after current bytes", got)
			}
		})
	}
}

// TestCloudEventsDefaultSource asserts that a CloudEvents configuration
// that provides no source uses the agent's name instead.
func TestCloudEventsDefaultSource(t *testing.T) {
	encoder := newCloudEventsEncoder(CloudEvents{Type: "io.k8s.event"})

	if got := cloudEventAttribute(t, encoder, "source", testEvent()); got != version.Name {
		t.Errorf("got source %q, want %q", got, version.Name)
	}
}

// TestCloudEventsID asserts that an event updated in place encodes
// with a different ID than the previous one.
func TestCloudEventsID(t *testing.T) {
	encoder := newCloudEventsEncoder(CloudEvents{Type: "io.k8s.event"})

	ev := testEvent()
	first := cloudEventAttribute(t, encoder, "id", ev)
	ev.ResourceVersion = "184214"
	second := cloudEventAttribute(t, encoder, "id", ev)

	if first == second {
		t.Errorf("got id %q for two resource versions, want them to differ", first)
	}
}

func TestCompose(t *testing.T) {
	cases := map[string]struct {
		framer sink.Framer
		want   string
	}{
		"array":     {arrayFramer{separator: []byte{','}}, "[a,b]"},
		"lines":     {linesFramer{separator: []byte{'\n'}}, "a\nb\n"},
		"separator": {separatorFramer{separator: []byte(";")}, "a;b"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			frames := append([]byte("a"), append(tc.framer.Separator(), 'b')...)

			if got := string(tc.framer.Compose(nil, frames, 2)); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestComposeExtendsBuffer(t *testing.T) {
	cases := map[string]sink.Framer{
		"array":     arrayFramer{separator: []byte{','}},
		"lines":     linesFramer{separator: []byte{'\n'}},
		"separator": separatorFramer{separator: []byte(";")},
	}
	for name, framer := range cases {
		t.Run(name, func(t *testing.T) {
			got := framer.Compose([]byte("kept:"), []byte("a"), 1)

			if string(got[:5]) != "kept:" {
				t.Errorf("got %q, want it appended after current bytes", got)
			}
		})
	}
}

// TestFramerArithmetic asserts that Fixed and Separator account for
// exactly the bytes Compose adds.
func TestFramerArithmetic(t *testing.T) {
	events := []*event.Event{testEvent(), testEvent(), testEvent()}

	for _, format := range formats {
		t.Run(format.String(), func(t *testing.T) {
			var (
				encoder = mustEncoder(t, format)
				framer  = newFramer(testFormatConfig(format))
				frames  []byte
				sum     int
			)
			for i, ev := range events {
				if i > 0 {
					frames = append(frames, framer.Separator()...)
				}
				before := len(frames)
				frames = encoder.AppendEvent(frames, ev)
				sum += len(frames) - before
			}
			want := framer.Fixed() + sum + (len(events)-1)*len(framer.Separator())
			if got := len(framer.Compose(nil, frames, len(events))); got != want {
				t.Errorf("got a payload of %d bytes, want the %d the framer accounts for", got, want)
			}
		})
	}
}

// TestBodyIsValidJSON asserts that the formats composing a JSON
// payload produce one a receiver can parse.
func TestBodyIsValidJSON(t *testing.T) {
	events := []*event.Event{testEvent(), testEvent()}

	for _, format := range []Format{FormatJSONList, FormatCloudEvents} {
		t.Run(format.String(), func(t *testing.T) {
			var (
				encoder = mustEncoder(t, format)
				framer  = newFramer(testFormatConfig(format))
				frames  []byte
			)
			for i, ev := range events {
				if i > 0 {
					frames = append(frames, framer.Separator()...)
				}
				frames = encoder.AppendEvent(frames, ev)
			}
			body := framer.Compose(nil, frames, len(events))

			var docs []map[string]any
			if err := json.Unmarshal(body, &docs); err != nil {
				t.Fatalf("body is not a JSON array: %v\n%s", err, body)
			}
			if len(docs) != len(events) {
				t.Errorf("got %d documents, want %d", len(docs), len(events))
			}
		})
	}
}

func testFormatConfig(format Format) Config {
	cfg := DefaultConfig()
	cfg.Format = format

	switch format {
	case FormatCloudEvents:
		cfg.CloudEvents = CloudEvents{
			Source: "kem-agent-test",
			Type:   "io.k8s.event",
		}
	case FormatTemplate:
		cfg.Template = `{{ reason }}`
	}
	return cfg
}

func mustEncoder(t *testing.T, format Format) sink.Encoder {
	t.Helper()

	encoder, err := newEncoder(testFormatConfig(format), testAgentMetadata())
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}
	return encoder
}

// cloudEventAttribute encodes the event and returns the attribute
// with the given name from the resulting CloudEvent.
func cloudEventAttribute(t *testing.T, enc cloudEventsEncoder, name string, ev *event.Event) string {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal(enc.AppendEvent(nil, ev), &doc); err != nil {
		t.Fatalf("frame is not valid JSON: %v", err)
	}
	value, ok := doc[name].(string)
	if !ok {
		t.Fatalf("got %v for %q, want a string", doc[name], name)
	}
	return value
}
