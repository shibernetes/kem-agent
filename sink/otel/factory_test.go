package otel

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/shibernetes/kem-agent/config/opaque"
	"github.com/shibernetes/kem-agent/internal/tlsconfig"
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
	s := createSink(t, validConfig())

	if got := s.Name(); got != "collector" {
		t.Errorf("got name %q, want %q", got, "collector")
	}
	if _, ok := s.(sink.BatchSink); !ok {
		t.Errorf("got type %T, want BatchSink", s)
	}
	if _, ok := s.(sink.Opener); !ok {
		t.Errorf("got type %T, want a type implementing the Opener interface", s)
	}
}

// TestCreateRejectsForeignConfig asserts that a miswired registry
// is a startup error rather than a panic.
func TestCreateRejectsForeignConfig(t *testing.T) {
	if _, err := NewFactory().Create(sink.Options{Name: "collector"}, struct{}{}); err == nil {
		t.Error("another type's config was accepted, want it rejected")
	}
}

// TestCreateOpensNothing asserts that building a sink performs no
// I/O, so a configuration can be checked where its certificates
// do not exist, and that opening it is what reads them.
func TestCreateOpensNothing(t *testing.T) {
	cfg := validConfig()
	cfg.TLS = &tlsconfig.Config{CAFile: filepath.Join(t.TempDir(), "missing.crt")}

	s := createSink(t, cfg)
	opener, ok := s.(sink.Opener)
	if !ok {
		t.Fatalf("got type %T, want a type implementing the Opener interface", s)
	}
	if err := opener.Open(t.Context()); err == nil {
		t.Error("sink opened with a certificate that does not exist, want an error")
	}
}

// TestCreateMatchesValidate asserts that Create does not fail on
// a configuration that validates successfully.
func TestCreateMatchesValidate(t *testing.T) {
	cases := map[string]func(*Config){
		"plaintext":   func(*Config) {},
		"http":        func(c *Config) { c.Endpoint = "http://collector:4317" },
		"https":       func(c *Config) { c.Endpoint = "https://collector:4317" },
		"unix socket": func(c *Config) { c.Endpoint = "unix:///var/run/otel.sock" },
		"no gzip":     func(c *Config) { c.Compression = CompressionNone },
		"tls":         func(c *Config) { c.TLS = &tlsconfig.Config{MinVersion: "1.3"} },
		"headers": func(c *Config) {
			c.Endpoint, c.Headers = "https://collector:4317", map[string]opaque.String{"x-scope-orgid": "team-a"}
		},
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			opt(&cfg)

			if err := cfg.Validate(); err != nil {
				t.Fatalf("configuration was rejected: %v", err)
			}
			if _, err := NewFactory().Create(sink.Options{Name: "collector"}, &cfg); err != nil {
				t.Errorf("failed to build valid configuration: %v", err)
			}
		})
	}
}

// createSink builds a sink through its factory.
func createSink(t *testing.T, cfg Config) sink.Sink {
	t.Helper()

	s, err := NewFactory().Create(sink.Options{Name: "collector", Identity: testAgentMetadata()}, &cfg)
	if err != nil {
		t.Fatalf("failed to create sink: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Shutdown(context.Background())
	})
	return s
}

// config returns the sink's own configuration type.
func config(t *testing.T, cfg sink.Config) *Config {
	t.Helper()

	c, ok := cfg.(*Config)
	if !ok {
		t.Fatalf("got type %T, want %T", cfg, (*Config)(nil))
	}
	return c
}
