package file

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/shibernetes/kem-agent/sink"
)

func TestDefaultMode(t *testing.T) {
	if got := DefaultConfig().Mode; got != 0o644 {
		t.Errorf("got mode %o, want 644", got)
	}
}

func TestConfigAccepts(t *testing.T) {
	cases := map[string]func(*Config){
		"path only":     func(*Config) {},
		"narrower mode": func(c *Config) { c.Mode = 0o600 },
		"wider mode":    func(c *Config) { c.Mode = 0o666 },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig("/var/log/events.jsonl")
			fn(&cfg)

			if err := cfg.Validate(); err != nil {
				t.Errorf("the config was rejected, want it accepted: %v", err)
			}
		})
	}
}

func TestConfigRejects(t *testing.T) {
	cases := map[string]func(*Config){
		"no path":               func(c *Config) { c.Path = "" },
		"relative path":         func(c *Config) { c.Path = "events.jsonl" },
		"no mode":               func(c *Config) { c.Mode = 0 },
		"mode with a type bit":  func(c *Config) { c.Mode = 0o1644 },
		"batch with no limit":   func(c *Config) { c.Batch.MaxEvents, c.Batch.MaxBytes = 0, 0 },
		"queue below one chunk": func(c *Config) { c.Queue.MaxBytes = sink.QueueChunkSize - 1 },
		"retry with no timeout": func(c *Config) { c.Retry.Timeout = 0 },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig("/var/log/events.jsonl")
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
	cfg := testConfig("/var/log/events.jsonl")

	want := sink.DrainerConfig{
		Batch: cfg.Batch,
		Queue: cfg.Queue,
		Retry: cfg.Retry,
	}
	if diff := cmp.Diff(want, cfg.DrainerConfig()); diff != "" {
		t.Errorf("the drainer config differs (-want +got):\n%s", diff)
	}
}

// testConfig returns the default config writing to a path.
func testConfig(path string) Config {
	cfg := DefaultConfig()
	cfg.Path = path

	return cfg
}
