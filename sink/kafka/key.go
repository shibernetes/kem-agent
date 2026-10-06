package kafka

import (
	"fmt"
	"slices"

	"github.com/invopop/jsonschema"

	"github.com/shibernetes/kem-agent/internal/enum"
)

// A MessageKey selects the event field that is written as the key of each
// record. Kafka places a record on a partition by hashing its key, so the
// records that share a key land on the same partition and keep their order.
// Records without a key are spread across the partitions.
type MessageKey string

const (
	MessageKeyUID       MessageKey = "uid"
	MessageKeyNamespace MessageKey = "namespace"
	MessageKeyNone      MessageKey = "none"
)

var messageKeys = []MessageKey{
	MessageKeyUID,
	MessageKeyNamespace,
	MessageKeyNone,
}

// String implements the [fmt.Stringer] interface.
func (k MessageKey) String() string {
	return string(k)
}

// Validate validates the message key.
func (k MessageKey) Validate() error {
	if slices.Contains(messageKeys, k) {
		return nil
	}
	return fmt.Errorf("unknown message key %q, allowed values are %s", k, enum.Join(messageKeys))
}

// JSONSchema satisfies the [github.com/invopop/jsonschema] reflector.
// It describes the supported message keys, in the JSON Schema spec.
func (MessageKey) JSONSchema() *jsonschema.Schema {
	return enum.Schema(messageKeys)
}
