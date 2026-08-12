package otel

import (
	"fmt"
	"slices"
	"strings"

	"github.com/invopop/jsonschema"
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
	return fmt.Errorf("unknown algorithm %q, allowed values are %s", c, strings.Join(compressionNames(), ", "))
}

// JSONSchema satisfies the [github.com/invopop/jsonschema] reflector.
// It describes the supported algorithms, in the JSON Schema spec.
func (Compression) JSONSchema() *jsonschema.Schema {
	enum := make([]any, len(compressions))
	for i, name := range compressionNames() {
		enum[i] = name
	}
	return &jsonschema.Schema{
		Type:        "string",
		Enum:        enum,
		Description: "The compression algorithm name.",
	}
}

func compressionNames() []string {
	names := make([]string, len(compressions))
	for i, c := range compressions {
		names[i] = string(c)
	}
	return names
}
