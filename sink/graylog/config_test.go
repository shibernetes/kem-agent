package graylog

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/shibernetes/kem-agent/sink"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Port != 12201 {
		t.Errorf("got port %d, want 12201", cfg.Port)
	}
	if got := time.Duration(cfg.SendTimeout); got != 5*time.Second {
		t.Errorf("got send timeout %s, want 5s", got)
	}
}

// TestDefaultTransportIsSecure asserts that a sink declaring no
// TLS settings still dials securely by default.
func TestDefaultTransportIsSecure(t *testing.T) {
	if DefaultConfig().TLS.Insecure {
		t.Error("the default configuration dials in plaintext, want TLS")
	}
}

func TestConfigAccepts(t *testing.T) {
	cases := map[string]func(*Config){
		"host only":                func(*Config) {},
		"IPv4 host":                func(c *Config) { c.Host = "10.0.0.1" },
		"IPv6 host":                func(c *Config) { c.Host = "2001:db8::1" },
		"custom port":              func(c *Config) { c.Port = 12222 },
		"plaintext":                func(c *Config) { c.TLS.Insecure = true },
		"source host":              func(c *Config) { c.SourceHost = "node-1" },
		"source host at the limit": func(c *Config) { c.SourceHost = strings.Repeat("h", 255) },
		"additional fields": func(c *Config) {
			c.AdditionalFields = map[string]string{"env": "prod", "app.kubernetes.io/name": "web"}
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			fn(&cfg)

			if err := cfg.Validate(); err != nil {
				t.Errorf("the config was rejected, want it accepted: %v", err)
			}
		})
	}
}

func TestConfigRejects(t *testing.T) {
	cases := map[string]func(*Config){
		"no host":                                func(c *Config) { c.Host = "" },
		"host with port":                         func(c *Config) { c.Host = "graylog:12201" },
		"host with scheme":                       func(c *Config) { c.Host = "tcp://graylog" },
		"zero port":                              func(c *Config) { c.Port = 0 },
		"port above the range":                   func(c *Config) { c.Port = 65536 },
		"source host over the cap":               func(c *Config) { c.SourceHost = strings.Repeat("h", 256) },
		"no send timeout":                        func(c *Config) { c.SendTimeout = 0 },
		"negative send timeout":                  func(c *Config) { c.SendTimeout = -1 },
		"both CA forms":                          func(c *Config) { c.TLS.CAFile, c.TLS.CAPEM = "/ca.crt", "-----BEGIN" },
		"batch with no limit":                    func(c *Config) { c.Batch.MaxEvents, c.Batch.MaxBytes = 0, 0 },
		"queue below one chunk":                  func(c *Config) { c.Queue.MaxBytes = sink.QueueChunkSize - 1 },
		"retry with no timeout":                  func(c *Config) { c.Retry.Timeout = 0 },
		"empty field name":                       withField(""),
		"field name with a space":                withField("two words"),
		"field name reserved":                    withField("id"),
		"reserved field prefix":                  withField("kem_cluster"),
		"reserved field prefix before transform": withField("kem/cluster"),
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			fn(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Error("the config was accepted, want it rejected")
			}
		})
	}
}

// TestDrainerConfig asserts that the drainer is given the settings of
// the sink rather than its own defaults.
func TestDrainerConfig(t *testing.T) {
	cfg := testConfig()

	want := sink.DrainerConfig{
		Batch:       cfg.Batch,
		Queue:       cfg.Queue,
		Retry:       cfg.Retry,
		SendTimeout: cfg.SendTimeout,
	}
	if diff := cmp.Diff(want, cfg.DrainerConfig()); diff != "" {
		t.Errorf("the drainer config differs (-want +got):\n%s", diff)
	}
}

// TestNormalizedNamesAgree asserts that validation and encoding normalize
// a key alike, each replacing a slash in a different way.
func TestNormalizedNamesAgree(t *testing.T) {
	cases := []string{
		"",
		"env",
		"app.kubernetes.io/name",
		"a/b/c",
		"kem/cluster",
		"no.slash-here",
	}
	for _, name := range cases {
		if got, want := fieldName(name), string(appendFieldName(nil, name)); got != want {
			t.Errorf("%q validates as %q, want it normalized to %q", name, got, want)
		}
	}
}

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.Host = "graylog"

	return cfg
}

// withField returns a mutator that adds one additional field to a config.
func withField(name string) func(*Config) {
	return func(c *Config) {
		c.AdditionalFields = map[string]string{name: "value"}
	}
}
