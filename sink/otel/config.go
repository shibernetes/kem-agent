package otel

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/config/opaque"
	"github.com/shibernetes/kem-agent/config/units"
	"github.com/shibernetes/kem-agent/internal/tlsconfig"
	"github.com/shibernetes/kem-agent/sink"
)

// TypeName is the value of the type key that selects this implementation.
const (
	TypeName = "otel"
)

const (
	defaultSendTimeout = units.Duration(5 * time.Second)

	// defaultMaxBytes stays under the 4 MiB an OTLP receiver accepts by
	// default, which it measures after decompression.
	defaultMaxBytes = units.Bytes(2 << 20) // 2 MiB

	// reservedHeaderPrefix is the metadata prefix the gRPC spec reserves.
	reservedHeaderPrefix = "grpc-"

	// binaryHeaderSuffix marks metadata whose value is base64-encoded
	// bytes, which a configured string is not.
	binaryHeaderSuffix = "-bin"
)

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
	schemeUnix  = "unix"
)

var _ sink.Drainable = Config{}

// Config defines the configuration of the OTel sink.
type Config struct {
	Type        string                   `yaml:"type"`
	Endpoint    string                   `yaml:"endpoint"`
	Compression Compression              `yaml:"compression,omitempty"`
	Headers     map[string]opaque.String `yaml:"headers,omitempty"`
	SendTimeout units.Duration           `yaml:"send_timeout,omitempty"`
	TLS         *tlsconfig.Config        `yaml:"tls,omitempty"`
	Batch       sink.BatchConfig         `yaml:"batch,omitempty"`
	Queue       sink.QueueConfig         `yaml:"queue,omitempty"`
	Retry       sink.RetryConfig         `yaml:"retry,omitempty"`
}

// DefaultConfig returns the default configuration.
// The connection is dialed as plaintext unless an HTTPS endpoint or
// a TLS block are configured.
func DefaultConfig() Config {
	cfg := Config{
		Type:        TypeName,
		Compression: CompressionGzip,
		SendTimeout: defaultSendTimeout,
		Batch:       sink.DefaultBatchConfig(),
		Queue:       sink.DefaultQueueConfig(),
		Retry:       sink.DefaultRetryConfig(),
	}
	cfg.Batch.MaxBytes = defaultMaxBytes

	return cfg
}

// Validate validates the configuration.
func (c Config) Validate() error {
	if err := diag.Required(c); err != nil {
		return err
	}
	if err := c.validate(); err != nil {
		return err
	}
	return sink.ValidateSharedConfigs(c.Batch, c.Queue, c.Retry)
}

// DrainerConfig implements the [sink.Drainable] interface.
func (c Config) DrainerConfig() sink.DrainerConfig {
	return sink.DrainerConfig{
		Batch:       c.Batch,
		Queue:       c.Queue,
		Retry:       c.Retry,
		SendTimeout: c.SendTimeout,
	}
}

func (c Config) validate() error {
	if err := c.validateEndpoint(); err != nil {
		return err
	}
	if err := c.Compression.Validate(); err != nil {
		return diag.Prefix(err, "compression")
	}
	if c.SendTimeout <= 0 {
		return diag.Pathf("send_timeout", "send_timeout must be positive")
	}
	if err := c.validateHeaders(); err != nil {
		return err
	}
	return c.validateTLS()
}

// scheme returns the transport scheme of the endpoint, if any.
func (c Config) scheme() string {
	switch s := strings.ToLower(c.Endpoint); {
	case strings.HasPrefix(s, schemeHTTPS+"://"):
		return schemeHTTPS
	case strings.HasPrefix(s, schemeHTTP+"://"):
		return schemeHTTP
	}
	return ""
}

// grpcDialTarget returns the endpoint without its transport scheme.
func (c Config) grpcDialTarget() string {
	target, ok := splitScheme(c.Endpoint, c.scheme())
	if !ok {
		return c.Endpoint
	}
	return target
}

// isSecure reports whether the connection is encrypted.
func (c Config) isSecure() bool {
	if c.TLS != nil {
		return !c.TLS.Insecure
	}
	return c.scheme() == schemeHTTPS
}

// sanitizedEndpoint strips the URI scheme and authority from the endpoint
// to extract the host:port for validation. For gRPC URIs of the form
// "scheme://[authority]/endpoint", the authority is also stripped, matching
// the parsing behavior of grpc-go's url.Parse approach.
func (c Config) sanitizedEndpoint() string {
	target := c.grpcDialTarget()

	if !strings.Contains(target, "://") {
		return target
	}
	u, err := url.Parse(target)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}

// validateEndpoint checks that the endpoint contains a host and a numeric port.
func (c Config) validateEndpoint() error {
	// A socket uses a path rather than an address,
	// so only its presence is checked.
	if path, ok := splitScheme(c.Endpoint, schemeUnix); ok {
		if path == "" {
			return diag.Pathf("endpoint", "endpoint: unix socket path is required")
		}
		return nil
	}
	endpoint := c.sanitizedEndpoint()

	if endpoint == "" {
		return diag.Pathf("endpoint", "endpoint %q does not contain host:port", c.Endpoint)
	}
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return diag.Pathf("endpoint", "invalid endpoint %q: %w", c.Endpoint, err)
	}
	if host == "" {
		return diag.Pathf("endpoint", "endpoint %q has no host", c.Endpoint)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return diag.Pathf("endpoint", "invalid port %q", port)
	}
	return nil
}

// validateTLS checks the TLS block against the endpoint scheme.
// Both select transport security, so a pair that contradicts is refused
// rather than settled by precedence.
func (c Config) validateTLS() error {
	if c.TLS == nil {
		return nil
	}
	switch {
	case c.scheme() == schemeHTTP:
		return diag.Pathf("tls", "tls settings cannot be used with a %s scheme", schemeHTTP)
	case c.TLS.Insecure && c.scheme() == schemeHTTPS:
		return diag.Pathf("tls.insecure", "tls: insecure cannot be used with an %s scheme", schemeHTTPS)
	}
	return diag.Prefix(c.TLS.Validate(), "tls")
}

// validateHeaders checks that every header is usable as gRPC metadata,
// and that none is sent over a plaintext connection.
func (c Config) validateHeaders() error {
	for _, name := range slices.Sorted(maps.Keys(c.Headers)) {
		if err := validateHeaderName(name); err != nil {
			return diag.Pathf("headers."+name, "headers: %w", err)
		}
		if !httpguts.ValidHeaderFieldValue(string(c.Headers[name])) {
			return diag.Pathf("headers."+name, "headers: invalid value for %q", name)
		}
		if !c.isSecure() && c.Headers[name] != "" {
			return diag.Pathf("headers."+name,
				"headers: %q requires transport security, a header value is always read as a credential", name)
		}
	}
	return nil
}

// validateHeaderName checks that name is usable as gRPC metadata.
// HTTP/2 carries field names in lowercase, so an uppercase one is rejected
// rather than folded.
func validateHeaderName(name string) error {
	switch {
	case name == "":
		return errors.New("name is required")
	case !httpguts.ValidHeaderFieldName(name):
		return fmt.Errorf("invalid name %q", name)
	case name != strings.ToLower(name):
		return fmt.Errorf("name %q must be lowercase", name)
	case strings.HasPrefix(name, reservedHeaderPrefix):
		return fmt.Errorf("name %q is reserved (prefix %q)", name, reservedHeaderPrefix)
	case strings.HasSuffix(name, binaryHeaderSuffix):
		return fmt.Errorf("name %q is reserved for binary values (suffix %q)", name, binaryHeaderSuffix)
	}
	return nil
}

// splitScheme returns the endpoint without the scheme, and whether it had one.
// The match is case-insensitive, so an uppercase scheme is still recognized.
func splitScheme(endpoint, scheme string) (string, bool) {
	if scheme == "" {
		return "", false
	}
	prefix := scheme + "://"

	if !strings.HasPrefix(strings.ToLower(endpoint), prefix) {
		return "", false
	}
	return endpoint[len(prefix):], true
}
