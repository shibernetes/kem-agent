package otel

import (
	"testing"

	"github.com/shibernetes/kem-agent/config/opaque"
	"github.com/shibernetes/kem-agent/config/units"
	"github.com/shibernetes/kem-agent/internal/tlsconfig"
	"github.com/shibernetes/kem-agent/sink"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Compression != CompressionGzip {
		t.Errorf("got compression %q, want %q", cfg.Compression, CompressionGzip)
	}
	if want := units.Bytes(2 << 20); cfg.Batch.MaxBytes != want {
		t.Errorf("got batch max bytes %d, want %d", cfg.Batch.MaxBytes, want)
	}
}

func TestValidateAcceptsEndpoints(t *testing.T) {
	cases := map[string]string{
		"host and port":    "collector:4317",
		"http":             "http://collector:4317",
		"https":            "https://collector:4317",
		"uppercase scheme": "HTTPS://collector:4317",
		"ipv6":             "[::1]:4317",
		"dns resolver":     "dns:///collector:4317",
		"unix socket":      "unix:///var/run/otel.sock",
	}
	for name, endpoint := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Endpoint = endpoint

			if err := cfg.Validate(); err != nil {
				t.Errorf("configuration was rejected: %v", err)
			}
		})
	}
}

func TestValidateRejectsEndpoints(t *testing.T) {
	cases := map[string]string{
		"empty":               "",
		"no port":             "collector",
		"no port with scheme": "https://collector",
		"port not a number":   "collector:grpc",
		"no host":             ":4317",
		"empty socket path":   "unix://",
		"no host in a uri":    "dns:///",
		"malformed uri":       "dns://[::1/collector:4317",
	}
	for name, endpoint := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Endpoint = endpoint

			if err := cfg.Validate(); err == nil {
				t.Error("the endpoint was accepted, want it rejected")
			}
		})
	}
}

func TestValidateRejectsSettings(t *testing.T) {
	cases := map[string]func(*Config){
		"unknown compression":     func(c *Config) { c.Compression = "snappy" },
		"send timeout unset":      func(c *Config) { c.SendTimeout = 0 },
		"send timeout below zero": func(c *Config) { c.SendTimeout = units.Duration(-1) },
		"invalid batch block":     func(c *Config) { c.Batch.MaxEvents, c.Batch.MaxBytes = 0, 0 },
		"invalid queue block":     func(c *Config) { c.Queue.MaxBytes = 1 },
		"invalid retry block":     func(c *Config) { c.Retry.Multiplier = 0 },
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			opt(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Error("the configuration was accepted, want it rejected")
			}
		})
	}
}

func TestValidateAcceptsHeaders(t *testing.T) {
	cases := map[string]func(*Config){
		"over tls": func(c *Config) {
			c.Endpoint = "https://collector:4317"
			c.Headers = map[string]opaque.String{"x-scope-orgid": "team-a"}
		},
		"empty value over plaintext": func(c *Config) {
			c.Headers = map[string]opaque.String{"x-scope-orgid": ""}
		},
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			opt(&cfg)

			if err := cfg.Validate(); err != nil {
				t.Errorf("configuration was rejected: %v", err)
			}
		})
	}
}

func TestValidateRejectsHeaders(t *testing.T) {
	cases := map[string]map[string]opaque.String{
		"empty name":      {"": "team-a"},
		"uppercase name":  {"X-Scope-OrgID": "team-a"},
		"invalid name":    {"x scope": "team-a"},
		"reserved prefix": {"grpc-timeout": "1s"},
		"binary suffix":   {"x-trace-bin": "team-a"},
		"invalid value":   {"x-scope-orgid": "team\na"},
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Endpoint, cfg.Headers = "https://collector:4317", headers

			if err := cfg.Validate(); err == nil {
				t.Error("the header was accepted, want it rejected")
			}
		})
	}
}

// TestValidateRejectsCredentialOverPlaintext asserts that a header value
// is considered as a credential and rejected on a plaintext connection.
func TestValidateRejectsCredentialOverPlaintext(t *testing.T) {
	cfg := validConfig()
	cfg.Headers = map[string]opaque.String{"x-scope-orgid": "team-a"}

	if err := cfg.Validate(); err == nil {
		t.Error("the header was accepted over a plaintext connection, want it rejected")
	}
}

// TestValidateRejectsContradictingTLS asserts that a TLS block and a
// scheme that disagree are refused rather than settled by precedence.
func TestValidateRejectsContradictingTLS(t *testing.T) {
	cases := map[string]func(*Config){
		"tls under http": func(c *Config) {
			c.Endpoint, c.TLS = "http://collector:4317", &tlsconfig.Config{}
		},
		"insecure under https": func(c *Config) {
			c.Endpoint, c.TLS = "https://collector:4317", &tlsconfig.Config{Insecure: true}
		},
		"invalid tls block": func(c *Config) {
			c.TLS = &tlsconfig.Config{MinVersion: "1.9"}
		},
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			opt(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Error("the configuration was accepted, want it rejected")
			}
		})
	}
}

func TestConfig_scheme(t *testing.T) {
	cases := map[string]string{
		"collector:4317":         "",
		"http://collector:4317":  schemeHTTP,
		"https://collector:4317": schemeHTTPS,
		"HTTPS://collector:4317": schemeHTTPS,
		"https-proxy:4317":       "",
		"unix:///var/run/x.sock": "",
	}
	for endpoint, want := range cases {
		t.Run(endpoint, func(t *testing.T) {
			if got := (Config{Endpoint: endpoint}).scheme(); got != want {
				t.Errorf("got scheme %q, want %q", got, want)
			}
		})
	}
}

// TestConfig_grpcDialTarget asserts that the scheme is stripped from the
// configured endpoint, since grpc-go reads it as a resolver name.
func TestConfig_grpcDialTarget(t *testing.T) {
	cases := map[string]string{
		"collector:4317":         "collector:4317",
		"http://collector:4317":  "collector:4317",
		"https://collector:4317": "collector:4317",
		"HTTPS://collector:4317": "collector:4317",
		"dns:///collector:4317":  "dns:///collector:4317",
		"unix:///var/run/x.sock": "unix:///var/run/x.sock",
	}
	for endpoint, want := range cases {
		t.Run(endpoint, func(t *testing.T) {
			if got := (Config{Endpoint: endpoint}).grpcDialTarget(); got != want {
				t.Errorf("got target %q, want %q", got, want)
			}
		})
	}
}

// TestConfig_isSecure asserts that the TLS block has precedence over the endpoint scheme.
func TestConfig_isSecure(t *testing.T) {
	cases := map[string]struct {
		endpoint string
		tls      *tlsconfig.Config
		want     bool
	}{
		"plain host":            {"collector:4317", nil, false},
		"http":                  {"http://collector:4317", nil, false},
		"https":                 {"https://collector:4317", nil, true},
		"tls block":             {"collector:4317", &tlsconfig.Config{}, true},
		"tls block, insecure":   {"collector:4317", &tlsconfig.Config{Insecure: true}, false},
		"https, insecure block": {"https://collector:4317", &tlsconfig.Config{Insecure: true}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := Config{Endpoint: tc.endpoint, TLS: tc.tls}
			if got := cfg.isSecure(); got != tc.want {
				t.Errorf("got %t for %q, want %t", got, tc.endpoint, tc.want)
			}
		})
	}
}

// TestDrainerConfig asserts that the drainer is given the settings of
// the sink rather than its own defaults.
func TestDrainerConfig(t *testing.T) {
	cfg := validConfig()
	cfg.SendTimeout = units.Duration(9)
	cfg.Batch.MaxEvents = 11

	want := sink.DrainerConfig{
		Batch:       cfg.Batch,
		Queue:       cfg.Queue,
		Retry:       cfg.Retry,
		SendTimeout: cfg.SendTimeout,
	}
	if got := cfg.DrainerConfig(); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
