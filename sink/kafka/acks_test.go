package kafka

import (
	"testing"

	"github.com/goccy/go-yaml"
)

type acksDoc struct {
	RequiredAcks RequiredAcks `yaml:"required_acks"`
}

// TestRequiredAcksDecode asserts that a string setting and the number
// Kafka uses for it decode to the same value.
func TestRequiredAcksDecode(t *testing.T) {
	cases := map[string]struct {
		text string
		want RequiredAcks
	}{
		"all":           {"all", RequiredAcksAll},
		"leader":        {"leader", RequiredAcksLeader},
		"none":          {"none", RequiredAcksNone},
		"leader as 1":   {"1", RequiredAcksLeader},
		"none as 0":     {"0", RequiredAcksNone},
		"quoted number": {`"1"`, RequiredAcksLeader},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := decodeAcks(tc.text)
			if err != nil {
				t.Fatalf("failed to decode %q: %v", tc.text, err)
			}
			if got != tc.want {
				t.Errorf("decoded %q as %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestRequiredAcksRejects asserts that a value outside both spellings is
// refused, Kafka's -1 for all included.
func TestRequiredAcksRejects(t *testing.T) {
	for _, text := range []string{"-1", "2"} {
		got, err := decodeAcks(text)
		if err == nil {
			err = got.Validate()
		}
		if err == nil {
			t.Errorf("%q was accepted, want it rejected", text)
		}
	}
}

func decodeAcks(text string) (RequiredAcks, error) {
	var doc acksDoc

	err := yaml.Unmarshal([]byte("required_acks: "+text+"\n"), &doc)
	return doc.RequiredAcks, err
}
