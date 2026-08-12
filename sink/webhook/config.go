package webhook

import (
	"errors"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/config/opaque"
	"github.com/shibernetes/kem-agent/config/units"
	"github.com/shibernetes/kem-agent/internal/identity"
	"github.com/shibernetes/kem-agent/internal/tlsconfig"
	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/sink/internal/compress"
)

// TypeName is the value of the type key that selects this implementation.
const (
	TypeName = "webhook"
)

const (
	schemeHTTP         = "http"
	schemeHTTPS        = "https"
	defaultSeparator   = "\n"
	defaultMethod      = http.MethodPost
	defaultSendTimeout = units.Duration(15 * time.Second)
)

var allowedMethods = []string{http.MethodPost, http.MethodPut}

var _ sink.Drainable = Config{}

// Config defines the configuration of the webhook sink.
type Config struct {
	Type        string                   `yaml:"type"`
	URL         string                   `yaml:"url"`
	Method      string                   `yaml:"method,omitempty"`
	Format      Format                   `yaml:"format,omitempty"`
	Template    string                   `yaml:"template,omitempty"`
	Separator   *string                  `yaml:"separator,omitempty"`
	CloudEvents CloudEvents              `yaml:"cloudevents,omitempty"`
	Headers     map[string]opaque.String `yaml:"headers,omitempty"`
	Auth        Auth                     `yaml:"auth,omitempty"`
	Signature   Signature                `yaml:"signature,omitempty"`
	Compression compress.Algorithm       `yaml:"compression,omitempty"`
	SendTimeout units.Duration           `yaml:"send_timeout,omitempty"`
	TLS         *tlsconfig.Config        `yaml:"tls,omitempty"`
	Batch       sink.BatchConfig         `yaml:"batch,omitempty"`
	Queue       sink.QueueConfig         `yaml:"queue,omitempty"`
	Retry       sink.RetryConfig         `yaml:"retry,omitempty"`
}

// BasicAuth holds HTTP basic auth credentials.
type BasicAuth struct {
	Username string        `yaml:"username"`
	Password opaque.String `yaml:"password,omitempty"`
}

// CloudEvents holds the attributes of a CloudEvents batch.
type CloudEvents struct {
	Source string `yaml:"source,omitempty"`
	Type   string `yaml:"type,omitempty"`
}

// Auth configures request authentication.
// Basic auth and bearer are mutually exclusive, and leaving both
// empty sends unauthenticated requests.
type Auth struct {
	Basic  *BasicAuth    `yaml:"basic,omitempty"`
	Bearer opaque.String `yaml:"bearer,omitempty"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{
		Type:        TypeName,
		Method:      defaultMethod,
		Format:      FormatJSONList,
		Compression: compress.None,
		SendTimeout: defaultSendTimeout,
		Batch:       sink.DefaultBatchConfig(),
		Queue:       sink.DefaultQueueConfig(),
		Retry:       sink.DefaultRetryConfig(),
	}
}

// Fields returns the event fields the template's tags read, or nothing
// for the formats that render the canonical event.
//
// A template that does not compile returns nothing, since the sink build
// reports that failure on its own.
func (c Config) Fields() []sink.FieldRef {
	tmpl, err := newTemplate(c.Template, identity.AgentMetadata{})
	if err != nil {
		return nil
	}
	var refs []sink.FieldRef

	for _, segment := range tmpl.segments {
		refs = append(refs, sink.FieldRef{
			Field: segment.tag,
			Path:  "template",
		})
	}
	return refs
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

// separator returns the string joining the rendered events of a batch,
// which only the template format reads.
func (c Config) separator() string {
	if c.Separator != nil {
		return *c.Separator
	}
	return defaultSeparator
}

// contentType returns the media type the request body is sent under.
func (c Config) contentType() string {
	switch c.Format {
	case FormatJSONList, FormatTemplate:
		return "application/json"
	case FormatNDJSON:
		return "application/x-ndjson"
	case FormatCloudEvents:
		return "application/cloudevents-batch+json"
	}
	return ""
}

func (c Config) validate() error {
	u, err := c.parseURL()
	if err != nil {
		return err
	}
	if !slices.Contains(allowedMethods, c.Method) {
		return diag.Pathf("method", "unknown method %q, allowed values are %s", c.Method, strings.Join(allowedMethods, ", "))
	}
	if err := c.Format.Validate(); err != nil {
		return diag.Pathf("format", "%w", err)
	}
	if err := c.validateFormatSettings(); err != nil {
		return err
	}
	if err := c.Compression.Validate(); err != nil {
		return diag.Pathf("compression", "%w", err)
	}
	if err := c.Auth.Validate(); err != nil {
		return diag.Prefix(err, "auth")
	}
	if err := c.Signature.Validate(); err != nil {
		return diag.Prefix(err, "signature")
	}
	if c.Signature.Identifier != "" && c.Compression != compress.None {
		return diag.Pathf("compression", "signature cannot be used with compression")
	}
	if c.SendTimeout <= 0 {
		return diag.Pathf("send_timeout", "send_timeout must be positive")
	}
	if err := c.validateHeaders(); err != nil {
		return err
	}
	if err := c.validateCredentials(u.Scheme); err != nil {
		return err
	}
	return c.validateTLS(u.Scheme)
}

func (c Config) parseURL() (*url.URL, error) {
	u, err := url.Parse(c.URL)
	if err != nil {
		return nil, diag.Pathf("url", "invalid url %q: %w", c.URL, err)
	}
	switch {
	case u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS:
		return nil, diag.Pathf("url", "invalid url scheme %q, must be %s or %s", u.Scheme, schemeHTTP, schemeHTTPS)
	case u.Host == "":
		return nil, diag.Pathf("url", "url %q must include a host", c.URL)
	case u.User != nil:
		// Credentials set in the URL bypass the auth block.
		return nil, diag.Pathf("url", "url must not carry credentials")
	}
	return u, nil
}

func (c Config) validateFormatSettings() error {
	switch {
	case c.Format == FormatTemplate && c.Template == "":
		return diag.Pathf("template", "template is required with the template format")
	case c.Format != FormatTemplate && c.Template != "":
		return diag.Pathf("template", "template requires the template format")
	case c.Format != FormatTemplate && c.Separator != nil:
		return diag.Pathf("separator", "separator requires the template format")
	case c.Format == FormatCloudEvents && c.CloudEvents.Type == "":
		return diag.Pathf("cloudevents.type", "cloudevents: type is required with the cloudevents format")
	case c.Format != FormatCloudEvents && c.CloudEvents != (CloudEvents{}):
		return diag.Pathf("cloudevents", "cloudevents requires the cloudevents format")
	}
	if c.Format == FormatTemplate {
		// Compiling the template right away checks its tags, so an
		// invalid one declaring unknown fields is refused during validation
		// rather than at first use, where it would render empty.
		if _, err := newTemplate(c.Template, identity.AgentMetadata{}); err != nil {
			return diag.Pathf("template", "%w", err)
		}
	}
	return nil
}

// validateCredentials refuses a credential the sink would otherwise
// send in the clear. What counts as one is decided by its type rather
// than by its name, so it covers every opaque value the configuration
// holds, header values included.
func (c Config) validateCredentials(scheme string) error {
	if scheme == schemeHTTPS {
		return nil
	}
	switch {
	case c.Auth.Bearer != "":
		return diag.Pathf("auth.bearer", "auth: bearer requires an %s scheme", schemeHTTPS)
	case c.Auth.Basic != nil && c.Auth.Basic.Password != "":
		return diag.Pathf("auth.basic.password", "auth: basic password requires an %s scheme", schemeHTTPS)
	case c.Signature.Secret != "":
		return diag.Pathf("signature.secret", "signature: secret requires an %s scheme", schemeHTTPS)
	}
	// The keys are sorted so that a configuration holding
	// several of them always reports the same one.
	for _, name := range slices.Sorted(maps.Keys(c.Headers)) {
		if c.Headers[name] != "" {
			return diag.Pathf("headers."+name,
				"headers: %q requires a %s scheme, a header value is always read as a credential", name, schemeHTTPS)
		}
	}
	return nil
}

// validateTLS checks the TLS settings against the URL scheme, which is
// what selects transport security. A TLS block configured with an http
// scheme is refused rather than ignored, since every one of its settings
// would otherwise decode cleanly and do nothing, leaving a configured
// client certificate to read as mutual TLS over a plaintext connection.
func (c Config) validateTLS(scheme string) error {
	if c.TLS == nil {
		return nil
	}
	if scheme != schemeHTTPS {
		return diag.Pathf("tls", "tls settings require an %s url", schemeHTTPS)
	}
	if c.TLS.Insecure {
		return diag.Pathf("tls.insecure", "tls: insecure is not supported, the url scheme is %s", scheme)
	}
	return diag.Prefix(c.TLS.Validate(), "tls")
}

// Validate validates authentication settings.
func (a Auth) Validate() error {
	switch {
	case a.Basic != nil && a.Bearer != "":
		return errors.New("basic and bearer are mutually exclusive")
	case a.Basic != nil && a.Basic.Username == "":
		return errors.New("basic username is required")
	}
	return nil
}
