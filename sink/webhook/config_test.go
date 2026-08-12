package webhook

import (
	"net/http"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/shibernetes/kem-agent/config/opaque"
	"github.com/shibernetes/kem-agent/internal/tlsconfig"
	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/sink/internal/compress"
)

const (
	plaintextURL = "http://foo.xyz/event"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	// A destination has no way to declare that it accepts a compressed
	// payload, so nothing is compressed unless explicitly configured.
	if cfg.Compression != compress.None {
		t.Errorf("got compression %s, want it off", cfg.Compression)
	}
	if cfg.Signature.Identifier != "" {
		t.Errorf("got %q signing, want it off", cfg.Signature.Identifier)
	}
	if err := cfg.Validate(); err == nil {
		t.Error("default configuration was accepted without url, want it rejected")
	}
	cfg.URL = "https://foo.xyz/event"

	if err := cfg.Validate(); err != nil {
		t.Errorf("default configuration was rejected with a url set: %v", err)
	}
}

func TestConfigAccepts(t *testing.T) {
	cases := map[string]func(*Config){
		"url only":           func(*Config) {},
		"plaintext url":      func(c *Config) { c.URL = plaintextURL },
		"put method":         func(c *Config) { c.Method = http.MethodPut },
		"ndjson":             func(c *Config) { c.Format = FormatNDJSON },
		"cloudevents":        func(c *Config) { c.Format, c.CloudEvents = FormatCloudEvents, CloudEvents{Type: "io.k8s.event"} },
		"template":           func(c *Config) { c.Format, c.Template = FormatTemplate, `{{ reason }}` },
		"template separator": func(c *Config) { c.Format, c.Template, c.Separator = FormatTemplate, `{{ reason }}`, new(";") },
		"gzip":               func(c *Config) { c.Compression = compress.Gzip },
		"zstd":               func(c *Config) { c.Compression = compress.Zstd },
		"bearer":             func(c *Config) { c.Auth.Bearer = "s3cr3t" },
		"basic":              func(c *Config) { c.Auth.Basic = &BasicAuth{Username: "agent", Password: "s3cr3t"} },
		"signature":          func(c *Config) { c.Signature = testV1Signature() },
		"tls":                func(c *Config) { c.TLS = &tlsconfig.Config{MinVersion: "1.3"} },
		"headers":            func(c *Config) { c.Headers = map[string]opaque.String{"X-Tenant": "team-a"} },
		"username plaintext": func(c *Config) { c.URL, c.Auth.Basic = plaintextURL, &BasicAuth{Username: "agent"} },
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			opt(&cfg)

			if err := cfg.Validate(); err != nil {
				t.Errorf("got %v, want the configuration accepted", err)
			}
		})
	}
}

func TestConfigRejects(t *testing.T) {
	cases := map[string]func(*Config){
		"no url":                    func(c *Config) { c.URL = "" },
		"malformed url":             func(c *Config) { c.URL = "://" },
		"unsupported scheme":        func(c *Config) { c.URL = "ftp://foo.xyz/event" },
		"no host":                   func(c *Config) { c.URL = "https:///hook" },
		"url credentials":           func(c *Config) { c.URL = "https://agent:s3cr3t@foo.xyz/event" },
		"invalid method":            func(c *Config) { c.Method = http.MethodGet },
		"invalid format":            func(c *Config) { c.Format = "protobuf" },
		"invalid compression":       func(c *Config) { c.Compression = "deflate" },
		"no send timeout":           func(c *Config) { c.SendTimeout = 0 },
		"template without format":   func(c *Config) { c.Template = `{{ reason }}` },
		"format without template":   func(c *Config) { c.Format = FormatTemplate },
		"unknown template tag":      func(c *Config) { c.Format, c.Template = FormatTemplate, `{{ unknown }}` },
		"separator without format":  func(c *Config) { c.Separator = new(";") },
		"cloudevents without type":  func(c *Config) { c.Format = FormatCloudEvents },
		"cloudevents wrong format":  func(c *Config) { c.CloudEvents = CloudEvents{Type: "io.k8s.event"} },
		"basic and bearer auth":     func(c *Config) { c.Auth = Auth{Basic: &BasicAuth{Username: "agent"}, Bearer: "s3cr3t"} },
		"basic without username":    func(c *Config) { c.Auth.Basic = &BasicAuth{Password: "s3cr3t"} },
		"secret without identifier": func(c *Config) { c.Signature.Secret = testV1Signature().Secret },
		"signing with compression":  func(c *Config) { c.Signature, c.Compression = testV1Signature(), compress.Gzip },
		"reserved header":           func(c *Config) { c.Headers = map[string]opaque.String{headerCluster: "us-east"} },
		"batch with no limit":       func(c *Config) { c.Batch.MaxEvents, c.Batch.MaxBytes = 0, 0 },
		"unknown tls version":       func(c *Config) { c.TLS = &tlsconfig.Config{MinVersion: "6.9"} },
		"tls ca in both forms":      func(c *Config) { c.TLS = &tlsconfig.Config{CAFile: "/ca.crt", CAPEM: "-----BEGIN CERTIFICATE-----"} },
		"queue below one chunk":     func(c *Config) { c.Queue.MaxBytes = sink.QueueChunkSize - 1 },
		"retry with no timeout":     func(c *Config) { c.Retry.Timeout = 0 },
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			opt(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Error("got no error, want the configuration rejected")
			}
		})
	}
}

// TestConfigRejectsTLSAgainstScheme asserts that the URL scheme is
// what selects transport security, so a valid TLS config block that
// would decode cleanly and do nothing is refused instead.
func TestConfigRejectsTLSAgainstScheme(t *testing.T) {
	cases := map[string]func(*Config){
		"empty config over plaintext": func(c *Config) {
			c.URL, c.TLS = plaintextURL, &tlsconfig.Config{}
		},
		"certificate over plaintext": func(c *Config) {
			c.URL, c.TLS = plaintextURL, &tlsconfig.Config{
				CertFile: "/tls.crt",
				KeyFile:  "/tls.key",
			}
		},
		"insecure over tls": func(c *Config) {
			c.TLS = &tlsconfig.Config{Insecure: true}
		},
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			opt(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Error("got no error, want the TLS settings rejected")
			}
		})
	}
}

// TestConfigRejectsCredentialsOverPlaintext asserts that any credential
// is refused over a plaintext connection, whether it is a bearer token,
// a basic auth password, a signing secret or a header value.
func TestConfigRejectsCredentialsOverPlaintext(t *testing.T) {
	cases := map[string]func(*Config){
		"bearer":    func(c *Config) { c.Auth.Bearer = "s3cr3t" },
		"password":  func(c *Config) { c.Auth.Basic = &BasicAuth{Username: "agent", Password: "s3cr3t"} },
		"secret":    func(c *Config) { c.Signature = testV1Signature() },
		"header":    func(c *Config) { c.Headers = map[string]opaque.String{"X-Tenant": "team-a"} },
		"any value": func(c *Config) { c.Headers = map[string]opaque.String{"X-Scope-OrgID": "team-a"} },
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			cfg.URL = plaintextURL
			opt(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Error("got no error, want the credential rejected")
			}
		})
	}
}

func TestConfigAcceptsEmptyCredentialsOverPlaintext(t *testing.T) {
	cfg := validConfig()
	cfg.URL = plaintextURL
	cfg.Headers = map[string]opaque.String{"X-Tenant": ""}

	if err := cfg.Validate(); err != nil {
		t.Errorf("got %v, want an empty header value accepted", err)
	}
}

func TestConfigSeparator(t *testing.T) {
	cases := map[string]struct {
		separator *string
		want      string
	}{
		"unset":  {nil, "\n"},
		"empty":  {new(""), ""},
		"custom": {new(";"), ";"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Separator = tc.separator

			if got := cfg.separator(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConfigContentType(t *testing.T) {
	cases := map[Format]string{
		FormatJSONList:    "application/json",
		FormatNDJSON:      "application/x-ndjson",
		FormatCloudEvents: "application/cloudevents-batch+json",
		FormatTemplate:    "application/json",
	}
	for format, want := range cases {
		t.Run(format.String(), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Format = format

			if got := cfg.contentType(); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestConfigDrainerConfig(t *testing.T) {
	cfg := validConfig()

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

func TestConfigFieldsReportsTemplateTags(t *testing.T) {
	cfg := Config{
		Format:   FormatTemplate,
		Template: `{"note": "{{ reason }}", "team": "{{ regardingObject.labels.team }}", "from": "{{ agent.cluster }}"}`,
	}
	want := []sink.FieldRef{
		{Field: "reason", Path: "template"},
		{Field: "regardingObject.labels.team", Path: "template"},
	}
	if got := cfg.Fields(); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestConfigFieldsIsEmptyWithoutTemplate(t *testing.T) {
	cfg := Config{Format: FormatJSONList}

	if got := cfg.Fields(); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func validConfig() Config {
	cfg := DefaultConfig()
	cfg.Type = "webhook"
	cfg.URL = "https://foo.xyz/event"

	return cfg
}

func testV1Signature() Signature {
	return Signature{Identifier: IdentifierV1, Secret: symmetricSecret(32)}
}
