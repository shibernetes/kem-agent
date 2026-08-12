package metrics

import (
	"fmt"

	"github.com/shibernetes/kem-agent/sink"
)

var _ sink.Factory = factory{}

// factory builds the sinks recording events as Prometheus metrics.
type factory struct{}

// NewFactory returns the factory of the metrics sink.
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
		return nil, fmt.Errorf("sink/metrics: got config of type %T, want %T", cfg, (*Config)(nil))
	}
	return New(opts.Name, *c, opts.Logger), nil
}
