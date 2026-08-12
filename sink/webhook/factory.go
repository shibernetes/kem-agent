package webhook

import (
	"fmt"

	"github.com/shibernetes/kem-agent/sink"
)

var _ sink.Factory = factory{}

// factory builds the sinks delivering to an HTTP endpoint.
type factory struct{}

// NewFactory returns the factory of the webhook sink.
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
	c, ok := cfg.(*Config)
	if !ok {
		return nil, fmt.Errorf("sink/webhook: got config of type %T, want %T", cfg, (*Config)(nil))
	}
	s, err := New(opts.Name, *c, opts.Identity)
	if err != nil {
		return nil, fmt.Errorf("sink/webhook: %w", err)
	}
	return s, nil
}
