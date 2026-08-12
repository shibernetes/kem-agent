package sink

import (
	"log/slog"

	"github.com/shibernetes/kem-agent/internal/identity"
)

// Factories are the sink factories a configuration may name,
// keyed by the value of a sink's type key.
type Factories map[string]Factory

// Factory is implemented by every sink factory.
// Each builds the sinks of one type, which is the key it is registered under.
type Factory interface {
	// CreateDefaultConfig creates the default configuration for a sink.
	// It is called once per configured sink of this type, so it must
	// return a fresh value and cause no side effects. The sink's own
	// block is decoded onto it, so a key left unset keeps the default.
	CreateDefaultConfig() Config

	// Create creates a sink based on the config, which its own
	// CreateDefaultConfig produced and which it asserts back to that
	// type. It performs no I/O and never fails for a reason validation
	// would not have caught, so a config that validates always builds.
	Create(Options, Config) (Sink, error)
}

// Options are the build options common to every sink.
type Options struct {
	Name     string
	Logger   *slog.Logger
	Identity identity.AgentMetadata
}
