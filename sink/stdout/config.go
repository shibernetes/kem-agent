package stdout

import (
	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/sink"
)

// TypeName is the value of the type key that selects this implementation.
const (
	TypeName = "stdout"
)

var _ sink.Drainable = Config{}

// Config defines the configuration of the stdout sink.
type Config struct {
	Type  string           `yaml:"type"`
	Batch sink.BatchConfig `yaml:"batch,omitempty"`
	Queue sink.QueueConfig `yaml:"queue,omitempty"`
	Retry sink.RetryConfig `yaml:"retry,omitempty"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{
		Type:  TypeName,
		Batch: sink.DefaultBatchConfig(),
		Queue: sink.DefaultQueueConfig(),
		Retry: sink.DefaultRetryConfig(),
	}
}

// Validate validates the configuration.
func (c Config) Validate() error {
	if err := diag.Required(c); err != nil {
		return err
	}
	return sink.ValidateSharedConfigs(c.Batch, c.Queue, c.Retry)
}

// DrainerConfig implements the [sink.Drainable] interface.
// The sink writes synchronously, so it has no send timeout.
func (c Config) DrainerConfig() sink.DrainerConfig {
	return sink.DrainerConfig{
		Batch: c.Batch,
		Queue: c.Queue,
		Retry: c.Retry,
	}
}
