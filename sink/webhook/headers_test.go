package webhook

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/shibernetes/kem-agent/config/opaque"
	"github.com/shibernetes/kem-agent/internal/identity"
	"github.com/shibernetes/kem-agent/sink/internal/compress"
)

func TestNewHeaders(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Compression = compress.Gzip
	cfg.Auth.Bearer = "s3cr3t"
	cfg.Headers = map[string]opaque.String{"X-Tenant-Name": "team-a"}

	headers := newHeaders(cfg, testAgentMetadata())

	cases := map[string]string{
		headerContentType:     "application/json",
		headerContentEncoding: "gzip",
		headerAuthorization:   "Bearer s3cr3t",
		headerCluster:         "prod-eu-1",
		headerNode:            "node-001",
		headerNamespace:       "kem-system",
		headerPod:             "kem-agent-0",
		headerVersion:         "0.0.1",
		"X-Tenant-Name":       "team-a",
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			if got := headers.Get(name); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// TestNewHeadersWithoutCompression asserts that a sink sending
// uncompressed bodies has no content encoding header.
func TestNewHeadersWithoutCompression(t *testing.T) {
	headers := newHeaders(DefaultConfig(), testAgentMetadata())

	if _, ok := headers[headerContentEncoding]; ok {
		t.Errorf("got %s = %q, want it unset", headerContentEncoding, headers.Get(headerContentEncoding))
	}
}

// TestNewHeadersSkipsUnresolvedMetadata asserts that an unset
// agent metadata value is ignored rather than sent empty.
func TestNewHeadersSkipsUnresolvedMetadata(t *testing.T) {
	headers := newHeaders(DefaultConfig(), identity.AgentMetadata{Cluster: "prod-eu-1"})

	if headers.Get(headerCluster) != "prod-eu-1" {
		t.Errorf("got %s = %q, want the cluster set", headerCluster, headers.Get(headerCluster))
	}
	for _, name := range []string{headerNode, headerNamespace, headerPod, headerVersion} {
		if _, ok := headers[name]; ok {
			t.Errorf("got %s set, want it omitted", name)
		}
	}
}

// TestNewHeadersBasicAuth asserts basic auth credentials encoding.
// The password holds characters whose base64 differs between the
// standard and URL alphabets.
func TestNewHeadersBasicAuth(t *testing.T) {
	const (
		username = "agent"
		password = "pa55w0rd?!~"
	)
	cfg := DefaultConfig()
	cfg.Auth.Basic = &BasicAuth{Username: username, Password: password}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://foo.xyz", nil)
	req.SetBasicAuth(username, password)

	want := req.Header.Get(headerAuthorization)
	if got := newHeaders(cfg, testAgentMetadata()).Get(headerAuthorization); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestValidateHeaders(t *testing.T) {
	cases := map[string]struct {
		headers map[string]opaque.String
		valid   bool
	}{
		"none":              {nil, true},
		"one":               {map[string]opaque.String{"X-Tenant": "team-a"}, true},
		"punctuation":       {map[string]opaque.String{"X-Tenant_1.2": "team-a"}, true},
		"empty value":       {map[string]opaque.String{"X-Tenant": ""}, true},
		"tab in value":      {map[string]opaque.String{"X-Tenant": "a\tb"}, true},
		"no name":           {map[string]opaque.String{"": "team-a"}, false},
		"space in name":     {map[string]opaque.String{"X Tenant": "team-a"}, false},
		"colon in name":     {map[string]opaque.String{"X:Tenant": "team-a"}, false},
		"unicode name":      {map[string]opaque.String{"Ünïcode": "team-a"}, false},
		"newline in value":  {map[string]opaque.String{"X-Tenant": "a\r\nX-Evil: 1"}, false},
		"delete in value":   {map[string]opaque.String{"X-Tenant": "a\x7fb"}, false},
		"reserved identity": {map[string]opaque.String{headerCluster: "forged"}, false},
		"reserved lowercase": {
			map[string]opaque.String{"kem-agent-pod": "forged"}, false,
		},
		"reserved content type": {map[string]opaque.String{"content-TYPE": "text/plain"}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Headers = tc.headers

			err := cfg.validateHeaders()
			switch {
			case tc.valid && err != nil:
				t.Errorf("got %v, want the headers accepted", err)
			case !tc.valid && err == nil:
				t.Error("got no error, want the headers rejected")
			}
		})
	}
}

// TestValidateHeadersRejectsDuplicates asserts that two header names
// differing only by case are refused, one of the two values being lost
// otherwise, and that the pair is always reported in the same order.
func TestValidateHeadersRejectsDuplicates(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Headers = map[string]opaque.String{"X-TENANT": "team-a", "x-tenant": "team-b"}

	want := duplicateHeaderError{first: "X-TENANT", second: "x-tenant"}
	for range 20 {
		err := cfg.validateHeaders()

		de, ok := errors.AsType[*duplicateHeaderError](err)
		if !ok {
			t.Fatalf("got type %T, want *duplicateHeaderError", err)
		}
		diff := cmp.Diff(want, *de, cmp.AllowUnexported(duplicateHeaderError{}))
		if diff != "" {
			t.Fatalf("the reported pair differs (-want +got):\n%s", diff)
		}
	}
}

func TestReservedHeaders(t *testing.T) {
	cases := map[string]struct {
		opt      func(*Config)
		header   string
		reserved bool
	}{
		"content type":             {nil, headerContentType, true},
		"metadata":                 {nil, headerCluster, true},
		"encoding, uncompressed":   {nil, headerContentEncoding, false},
		"encoding, compressed":     {func(c *Config) { c.Compression = compress.Gzip }, headerContentEncoding, true},
		"authorization, no auth":   {nil, headerAuthorization, false},
		"authorization, bearer":    {func(c *Config) { c.Auth.Bearer = "s3cr3t" }, headerAuthorization, true},
		"authorization, basic":     {func(c *Config) { c.Auth.Basic = &BasicAuth{Username: "agent"} }, headerAuthorization, true},
		"signature, unsigned":      {nil, http.CanonicalHeaderKey(headerID), false},
		"signature, signed":        {func(c *Config) { c.Signature.Identifier = IdentifierV1 }, http.CanonicalHeaderKey(headerID), true},
		"timestamp, signed":        {func(c *Config) { c.Signature.Identifier = IdentifierV1 }, http.CanonicalHeaderKey(headerTimestamp), true},
		"signature header, signed": {func(c *Config) { c.Signature.Identifier = IdentifierV1 }, http.CanonicalHeaderKey(headerSignature), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig()
			if tc.opt != nil {
				tc.opt(&cfg)
			}
			reserved := slices.Contains(cfg.reservedHeaders(), tc.header)
			switch {
			case tc.reserved && !reserved:
				t.Errorf("got %s allowed, want it reserved", tc.header)
			case !tc.reserved && reserved:
				t.Errorf("got %s reserved, want it allowed", tc.header)
			}
		})
	}
}

// TestReservedHeadersAreCanonical asserts that every reserved
// header name is already in canonical form, since a collision
// would otherwise be invisible.
func TestReservedHeadersAreCanonical(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Compression = compress.Gzip
	cfg.Auth.Bearer = "s3cr3t"
	cfg.Signature.Identifier = IdentifierV1

	for _, name := range cfg.reservedHeaders() {
		if want := http.CanonicalHeaderKey(name); name != want {
			t.Errorf("got %q, want canonical form %q", name, want)
		}
	}
}
