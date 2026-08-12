package stdout

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/shibernetes/kem-agent/sink"
)

// TestCreateDefaultConfigIsFresh asserts that each configured sink
// is decoded onto a config of its own, rather than sharing one.
func TestCreateDefaultConfigIsFresh(t *testing.T) {
	f := NewFactory()
	first, second := f.CreateDefaultConfig(), f.CreateDefaultConfig()

	if first == second {
		t.Fatal("two calls returned one config, want a fresh one each time")
	}
	if diff := cmp.Diff(config(t, first), config(t, second)); diff != "" {
		t.Errorf("the two configs differ (-first +second):\n%s", diff)
	}
}

func TestCreate(t *testing.T) {
	f := NewFactory()

	s, err := f.Create(sink.Options{Name: "console"}, f.CreateDefaultConfig())
	if err != nil {
		t.Fatalf("failed to create sink: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Shutdown(t.Context())
	})
	if got := s.Name(); got != "console" {
		t.Errorf("got name %q, want %q", got, "console")
	}
	if _, ok := s.(sink.BatchSink); !ok {
		t.Errorf("got type %T, want a BatchSink", s)
	}
}

// TestCreateRejectsForeignConfig asserts that a miswired registry
// is a startup error rather than a panic.
func TestCreateRejectsForeignConfig(t *testing.T) {
	if _, err := NewFactory().Create(sink.Options{Name: "console"}, struct{}{}); err == nil {
		t.Error("another type's config was accepted, want it rejected")
	}
}

// config returns the sink's own configuration type.
func config(t *testing.T, cfg sink.Config) *Config {
	t.Helper()

	c, ok := cfg.(*Config)
	if !ok {
		t.Fatalf("got config of type %T, want %T", cfg, (*Config)(nil))
	}
	return c
}
