package compress

import (
	"fmt"
	"slices"

	"github.com/invopop/jsonschema"

	"github.com/shibernetes/kem-agent/internal/enum"
)

// An Algorithm represents a payload compression algorithm.
// Its value is also the token a destination names the compression with,
// which is what an HTTP sink sets Content-Encoding to.
type Algorithm string

const (
	None Algorithm = "none"
	Gzip Algorithm = "gzip"
	Zstd Algorithm = "zstd"
)

// algorithms lists the supported algorithms.
var algorithms = []Algorithm{None, Gzip, Zstd}

// String implements the [fmt.Stringer] interface.
func (a Algorithm) String() string {
	return string(a)
}

// Validate validates the algorithm.
func (a Algorithm) Validate() error {
	if slices.Contains(algorithms, a) {
		return nil
	}
	// An unknown value has to be refused rather than read as no
	// compression at all, since a strict decode only reaches the
	// key and would let a misspelled one through.
	return fmt.Errorf("compression: unknown algorithm %q, allowed values are %s", a, enum.Join(algorithms))
}

// JSONSchema satisfies the [github.com/invopop/jsonschema] reflector.
// It describes the supported algorithms, in the JSON Schema spec.
func (Algorithm) JSONSchema() *jsonschema.Schema {
	return enum.Schema(algorithms)
}
