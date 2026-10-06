package kafka

import (
	"fmt"
	"slices"

	"github.com/invopop/jsonschema"

	"github.com/shibernetes/kem-agent/internal/enum"
)

// A Compression selects the codec that each batch of records is compressed
// with before it is sent to a broker.
type Compression string

const (
	CompressionNone   Compression = "none"
	CompressionGzip   Compression = "gzip"
	CompressionSnappy Compression = "snappy"
	CompressionLZ4    Compression = "lz4"
	CompressionZstd   Compression = "zstd"
)

var compressions = []Compression{
	CompressionNone,
	CompressionGzip,
	CompressionSnappy,
	CompressionLZ4,
	CompressionZstd,
}

// String implements the [fmt.Stringer] interface.
func (c Compression) String() string {
	return string(c)
}

// Validate validates the compression.
func (c Compression) Validate() error {
	if slices.Contains(compressions, c) {
		return nil
	}
	return fmt.Errorf("unknown compression algorithm %q, allowed values are %s", c, enum.Join(compressions))
}

// JSONSchema satisfies the [github.com/invopop/jsonschema] reflector.
// It describes the supported algorithms, in the JSON Schema spec.
func (Compression) JSONSchema() *jsonschema.Schema {
	return enum.Schema(compressions)
}
