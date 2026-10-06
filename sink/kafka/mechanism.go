package kafka

import (
	"fmt"

	"github.com/invopop/jsonschema"

	"github.com/shibernetes/kem-agent/internal/enum"
)

// A SASLMechanism selects how the client authenticates to the brokers.
// The names are spelled as IANA registers them, which is also how broker
// configurations write them.
type SASLMechanism string

const (
	SASLMechanismPlain       SASLMechanism = "PLAIN"
	SASLMechanismSCRAMSHA256 SASLMechanism = "SCRAM-SHA-256"
	SASLMechanismSCRAMSHA512 SASLMechanism = "SCRAM-SHA-512"
)

var saslMechanisms = []SASLMechanism{
	SASLMechanismPlain,
	SASLMechanismSCRAMSHA256,
	SASLMechanismSCRAMSHA512,
}

// String implements the [fmt.Stringer] interface.
func (m SASLMechanism) String() string {
	return string(m)
}

// Validate validates the mechanism.
// The name must match a supported mechanism exactly, so a lowercase one
// is rejected.
func (m SASLMechanism) Validate() error {
	switch m {
	case SASLMechanismPlain, SASLMechanismSCRAMSHA256, SASLMechanismSCRAMSHA512:
		return nil
	}
	return fmt.Errorf("unknown SASL mechanism %q, allowed values are %s", m, enum.Join(saslMechanisms))
}

// JSONSchema satisfies the [github.com/invopop/jsonschema] reflector.
// It describes the supported mechanisms, in the JSON Schema spec.
func (SASLMechanism) JSONSchema() *jsonschema.Schema {
	return enum.Schema(saslMechanisms)
}
