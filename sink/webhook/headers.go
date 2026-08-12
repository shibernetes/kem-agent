package webhook

import (
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"golang.org/x/net/http/httpguts"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/internal/identity"
	"github.com/shibernetes/kem-agent/sink/internal/compress"
)

const (
	headerContentType     = "Content-Type"
	headerContentEncoding = "Content-Encoding"
	headerAuthorization   = "Authorization"
	agentHeaderPrefix     = "Kem-Agent-"
)

const (
	headerCluster   = agentHeaderPrefix + "Cluster"
	headerPod       = agentHeaderPrefix + "Pod"
	headerNamespace = agentHeaderPrefix + "Namespace"
	headerNode      = agentHeaderPrefix + "Node"
	headerVersion   = agentHeaderPrefix + "Version"
)

// A duplicateHeaderError reports two configured header names
// that differ only by case.
type duplicateHeaderError struct {
	first  string
	second string
}

// Error implements the error interface.
func (e *duplicateHeaderError) Error() string {
	return fmt.Sprintf("headers: %q and %q are the same header", e.first, e.second)
}

// newHeaders returns the headers every request carries. None of them
// varies between batches, so they are built once, leaving the signature
// as the only header set per attempt.
func newHeaders(cfg Config, meta identity.AgentMetadata) http.Header {
	headers := http.Header{}

	for name, value := range cfg.Headers {
		headers.Set(name, string(value))
	}
	headers.Set(headerContentType, cfg.contentType())

	if cfg.Compression != compress.None {
		headers.Set(headerContentEncoding, cfg.Compression.String())
	}
	setIdentityHeaders(headers, meta)
	setAuthorizationHeader(headers, cfg.Auth)

	return headers
}

// setIdentityHeaders sets the agent's metadata headers.
// A metadata value that could not be resolved is skipped
// rather than sent empty.
func setIdentityHeaders(header http.Header, meta identity.AgentMetadata) {
	for name, value := range map[string]string{
		headerCluster:   meta.Cluster,
		headerPod:       meta.Pod,
		headerNamespace: meta.Namespace,
		headerNode:      meta.Node,
		headerVersion:   meta.Version,
	} {
		if value != "" {
			header.Set(name, value)
		}
	}
}

// setAuthorizationHeader sets the credentials the requests are sent with.
func setAuthorizationHeader(header http.Header, auth Auth) {
	switch {
	case auth.Basic != nil:
		header.Set(headerAuthorization, "Basic "+basicAuth(auth.Basic.Username, string(auth.Basic.Password)))
	case auth.Bearer != "":
		header.Set(headerAuthorization, "Bearer "+string(auth.Bearer))
	}
}

// basicAuth returns the credentials in the form RFC 7617 defines,
// the username and password joined by a colon and base64 encoded.
func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

// validateHeaders validates the configured headers.
func (c Config) validateHeaders() error {
	var (
		reserved = c.reservedHeaders()
		seen     = make(map[string]string, len(c.Headers))
	)
	for _, name := range slices.Sorted(maps.Keys(c.Headers)) {
		if err := validateHeaderName(name); err != nil {
			return err
		}
		if !httpguts.ValidHeaderFieldValue(string(c.Headers[name])) {
			return diag.Pathf("headers."+name, "headers: %q holds a character a header value cannot carry", name)
		}
		canonical := http.CanonicalHeaderKey(name)

		// A configuration defining one of the reserved headers is
		// misconfigured, so it is refused rather than resolved by
		// an implicit precedence rule.
		if slices.Contains(reserved, canonical) {
			return diag.Pathf("headers."+name, "headers: %q is set by the sink and cannot be overridden", name)
		}
		// A header name is case-insensitive, so two keys differing
		// only in case are a duplicate.
		if prev, ok := seen[canonical]; ok {
			return &duplicateHeaderError{first: prev, second: name}
		}
		seen[canonical] = name
	}
	return nil
}

// reservedHeaders returns the canonical names of the headers the sink
// sets itself, which depend on what the configuration declares.
func (c Config) reservedHeaders() []string {
	reserved := []string{
		headerContentType,
		headerCluster,
		headerPod,
		headerNamespace,
		headerNode,
		headerVersion,
	}
	if c.Compression != compress.None {
		reserved = append(reserved, headerContentEncoding)
	}
	if c.Auth.Basic != nil || c.Auth.Bearer != "" {
		reserved = append(reserved, headerAuthorization)
	}
	if c.Signature.Identifier != "" {
		reserved = append(reserved,
			http.CanonicalHeaderKey(headerID),
			http.CanonicalHeaderKey(headerTimestamp),
			http.CanonicalHeaderKey(headerSignature),
		)
	}
	return reserved
}

// validateHeaderName checks that name is usable as a header name.
func validateHeaderName(name string) error {
	if name == "" {
		return errors.New("headers: header name is required")
	}
	if !httpguts.ValidHeaderFieldName(name) {
		return fmt.Errorf("headers: invalid header name %q", name)
	}
	return nil
}
