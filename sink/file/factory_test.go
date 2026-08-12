package file

import (
	"os"
	"path/filepath"
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

// TestCreateOpensNothing asserts that building a sink performs no I/O,
// so that a config can be checked even when its path does not exist.
func TestCreateOpensNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")

	s := create(t, path)
	t.Cleanup(func() {
		_ = s.Shutdown(t.Context())
	})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the file exists before the sink was opened: %v", err)
	}
}

func TestCreate(t *testing.T) {
	s := create(t, filepath.Join(t.TempDir(), "events.jsonl"))

	t.Cleanup(func() {
		_ = s.Shutdown(t.Context())
	})
	if got := s.Name(); got != "archive" {
		t.Errorf("got name %q, want %q", got, "archive")
	}
	if _, ok := s.(sink.Opener); !ok {
		t.Errorf("got type %T, want a type implementing the Opener interface", s)
	}
}

func TestCreateWiresConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")

	s := create(t, path)
	t.Cleanup(func() {
		_ = s.Shutdown(t.Context())
	})
	opener, ok := s.(sink.Opener)
	if !ok {
		t.Fatalf("got type %T, want a type implementing the Opener interface", s)
	}
	if err := opener.Open(t.Context()); err != nil {
		t.Fatalf("failed to open: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the sink wrote no file at the configured path: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("got mode %o, want 600", got)
	}
}

// TestCreateRejectsForeignConfig asserts that a miswired registry
// is a startup error rather than a panic.
func TestCreateRejectsForeignConfig(t *testing.T) {
	if _, err := NewFactory().Create(sink.Options{Name: "archive"}, struct{}{}); err == nil {
		t.Error("another type's config was accepted, want it rejected")
	}
}

// create builds a sink through its factory.
func create(t *testing.T, path string) sink.Sink {
	t.Helper()

	f := NewFactory()
	cfg := f.CreateDefaultConfig()
	c := config(t, cfg)
	c.Path, c.Mode = path, 0o600

	s, err := f.Create(sink.Options{Name: "archive"}, cfg)
	if err != nil {
		t.Fatalf("failed to create sink: %v", err)
	}
	return s
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
