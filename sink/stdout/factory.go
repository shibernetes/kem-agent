package stdout

import (
	"fmt"

	"github.com/shibernetes/kem-agent/sink"
)

var _ sink.Factory = factory{}

// factory builds the sinks writing to standard output.
type factory struct{}

// NewFactory returns the factory of the stdout sink.
func NewFactory() sink.Factory {
	return factory{}
}

// CreateDefaultConfig implements the [sink.Factory] interface.
func (factory) CreateDefaultConfig() sink.Config {
	cfg := DefaultConfig()
	return &cfg
}

// Create implements the [sink.Factory] interface.
func (factory) Create(opts sink.Options, cfg sink.Config) (sink.Sink, error) {
	if _, ok := cfg.(*Config); !ok {
		return nil, fmt.Errorf("sink/stdout: got config of type %T, want %T", cfg, (*Config)(nil))
	}
	return New(opts.Name), nil
}
