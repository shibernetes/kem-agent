package kafka

import (
	"slices"
	"testing"
)

// TestSASLMechanismValidate asserts that the mechanisms are matched with the
// exact spelling an operator copies from a broker configuration, and that the
// schema offers no name that validation would reject.
func TestSASLMechanismValidate(t *testing.T) {
	accepted := []SASLMechanism{"PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512"}

	if !slices.Equal(saslMechanisms, accepted) {
		t.Errorf("the schema offers %q, want %q", saslMechanisms, accepted)
	}
	for _, m := range accepted {
		if err := m.Validate(); err != nil {
			t.Errorf("mechanism %q was rejected, want it accepted: %v", m, err)
		}
	}
	for _, m := range []SASLMechanism{"", "plain", "scram-sha-512", "GSSAPI"} {
		if err := m.Validate(); err == nil {
			t.Errorf("mechanism %q was accepted, want it rejected", m)
		}
	}
}
