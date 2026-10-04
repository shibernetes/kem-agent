package otel

import (
	"fmt"
	"slices"

	"github.com/invopop/jsonschema"

	"github.com/shibernetes/kem-agent/internal/enum"
)

// A Compression represents a request compression algorithm.
// Its value is also the token a compressor is registered with in grpc-go,
// which is what the transport sets grpc-encoding to.
type Compression string

const (
	CompressionNone Compression = "none"
	CompressionGzip Compression = "gzip"
)

var compressions = []Compression{CompressionNone, CompressionGzip}

// String implements the [fmt.Stringer] interface.
func (c Compression) String() string {
	return string(c)
}

// Validate validates the compression.
func (c Compression) Validate() error {
	if slices.Contains(compressions, c) {
		return nil
	}
	return fmt.Errorf("unknown algorithm %q, allowed values are %s", c, enum.Join(compressions))
}

// JSONSchema satisfies the [github.com/invopop/jsonschema] reflector.
// It describes the supported algorithms, in the JSON Schema spec.
func (Compression) JSONSchema() *jsonschema.Schema {
	return enum.Schema(compressions)
}
