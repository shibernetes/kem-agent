package kafka

import (
	"bytes"
	"testing"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/sink/internal/shared"
)

// TestFrameRoundTrip asserts that each frame of a composed payload reads back
// as the key and the canonical JSON of its event, and that an empty key reads
// back as no key at all.
func TestFrameRoundTrip(t *testing.T) {
	named := testEvent()

	unnamed := testEvent()
	unnamed.UID, unnamed.Namespace = "", ""

	cases := map[MessageKey][]string{
		MessageKeyUID:       {string(named.UID), ""},
		MessageKeyNamespace: {named.Namespace, ""},
		MessageKeyNone:      {"", ""},
	}
	for key, want := range cases {
		t.Run(string(key), func(t *testing.T) {
			var (
				enc     = encoder{key: key}
				payload = enc.AppendEvent(enc.AppendEvent(nil, named), unnamed)
			)
			for i, ev := range []*event.Event{named, unnamed} {
				gotKey, value, rest, ok := readFrame(payload)
				if !ok {
					t.Fatalf("could not read frame %d", i)
				}
				switch {
				case want[i] == "" && gotKey != nil:
					t.Errorf("frame %d has key %q, want no key", i, gotKey)
				case want[i] != "" && string(gotKey) != want[i]:
					t.Errorf("frame %d has key %q, want %q", i, gotKey, want[i])
				}
				if wantValue := canonicalJSON(ev); !bytes.Equal(value, wantValue) {
					t.Errorf("frame %d has value %s, want %s", i, value, wantValue)
				}
				payload = rest
			}
			if len(payload) != 0 {
				t.Errorf("got %d bytes after the last frame, want none", len(payload))
			}
		})
	}
}

func TestReadFrameRejectsCutPayload(t *testing.T) {
	frame := encoder{key: MessageKeyUID}.AppendEvent(nil, testEvent())

	for n := range frame {
		if _, _, _, ok := readFrame(frame[:n]); ok {
			t.Errorf("a frame cut to %d of its %d bytes was read, want it reported", n, len(frame))
		}
	}
}

func canonicalJSON(ev *event.Event) []byte {
	line := shared.NewJSONEncoder().AppendEvent(nil, ev)
	return bytes.TrimSuffix(line, []byte("\n"))
}
