package stdout

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/shibernetes/kem-agent/sink"
)

func TestDefaultConfigValidates(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Errorf("the default config was rejected, want it accepted: %v", err)
	}
}

// TestConfigRejects asserts that the sink validates the shared
// blocks it carries, rather than leaving them to the agent.
func TestConfigRejects(t *testing.T) {
	cases := map[string]func(*Config){
		"batch with no limit":   func(c *Config) { c.Batch.MaxEvents, c.Batch.MaxBytes = 0, 0 },
		"queue below one chunk": func(c *Config) { c.Queue.MaxBytes = sink.QueueChunkSize - 1 },
		"retry with no timeout": func(c *Config) { c.Retry.Timeout = 0 },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig()
			fn(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Error("the config was accepted, want it rejected")
			}
		})
	}
}

// TestDrainerConfig asserts that the delivery path reads the
// sink's own config blocks, and has no send timeout, since a
// write cannot be interrupted.
func TestDrainerConfig(t *testing.T) {
	cfg := DefaultConfig()

	want := sink.DrainerConfig{
		Batch: cfg.Batch,
		Queue: cfg.Queue,
		Retry: cfg.Retry,
	}
	if diff := cmp.Diff(want, cfg.DrainerConfig()); diff != "" {
		t.Errorf("the drainer config differs (-want +got):\n%s", diff)
	}
}
