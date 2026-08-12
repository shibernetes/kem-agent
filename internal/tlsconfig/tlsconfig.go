package tlsconfig

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/shibernetes/kem-agent/config/diag"
)

const (
	defaultMinVersion = "1.2"
)

var tlsVersions = map[string]uint16{
	"1.0": tls.VersionTLS10,
	"1.1": tls.VersionTLS11,
	"1.2": tls.VersionTLS12,
	"1.3": tls.VersionTLS13,
}

// Config is a declarative TLS configuration that [Config.Build] turns into
// a [tls.Config]. It carries certificate and verification settings only.
type Config struct {
	Insecure                 bool   `yaml:"insecure,omitempty"`
	InsecureSkipVerify       bool   `yaml:"insecure_skip_verify,omitempty"`
	MinVersion               string `yaml:"min_version,omitempty"`
	CAFile                   string `yaml:"ca_file,omitempty"`
	CAPEM                    string `yaml:"ca_pem,omitempty"`
	IncludeSystemCACertsPool bool   `yaml:"include_system_ca_certs_pool,omitempty"`
	CertFile                 string `yaml:"cert_file,omitempty"`
	CertPEM                  string `yaml:"cert_pem,omitempty"`
	KeyFile                  string `yaml:"key_file,omitempty"`
	KeyPEM                   string `yaml:"key_pem,omitempty"`
	ServerName               string `yaml:"server_name,omitempty"`
}

// Validate validates the configuration.
// It does not open files, it parses and verifies inline PEM only.
func (c Config) Validate() error {
	if _, err := c.minVersion(); err != nil {
		return diag.Path("min_version", err)
	}
	if c.CAFile != "" && c.CAPEM != "" {
		return diag.Pathf("ca_pem", "ca_file and ca_pem are mutually exclusive")
	}
	if c.CertFile != "" && c.CertPEM != "" {
		return diag.Pathf("cert_pem", "cert_file and cert_pem are mutually exclusive")
	}
	if c.KeyFile != "" && c.KeyPEM != "" {
		return diag.Pathf("key_pem", "key_file and key_pem are mutually exclusive")
	}
	var (
		hasCert = c.CertFile != "" || c.CertPEM != ""
		hasKey  = c.KeyFile != "" || c.KeyPEM != ""
	)
	if hasCert != hasKey {
		return errors.New("a client certificate and its key must be set together")
	}
	if c.CAPEM != "" && !x509.NewCertPool().AppendCertsFromPEM([]byte(c.CAPEM)) {
		return diag.Pathf("ca_pem", "ca_pem holds no valid certificate")
	}
	if c.CertPEM != "" && c.KeyPEM != "" {
		if _, err := tls.X509KeyPair([]byte(c.CertPEM), []byte(c.KeyPEM)); err != nil {
			return fmt.Errorf("failed to load the inline keypair: %w", err)
		}
	}
	return nil
}

// Build turns the configuration into a [tls.Config], or returns nil when
// the connection is meant to be plaintext, which the caller then dials
// with no transport security at all. Any referenced certificate files are
// read and parsed here, so a bad path or malformed certificate surfaces
// as an error at build time.
func (c Config) Build() (*tls.Config, error) {
	if c.Insecure {
		return nil, nil
	}
	version, err := c.minVersion()
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		ServerName:         c.ServerName,
		InsecureSkipVerify: c.InsecureSkipVerify, //nolint:gosec // Explicit user opt-in.
		MinVersion:         version,
	}
	if err := c.setRootCAs(cfg); err != nil {
		return nil, err
	}
	if err := c.setCertificate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// setRootCAs installs the configured authority, which replaces the
// system roots rather than merging, unless the config asks for both.
func (c Config) setRootCAs(cfg *tls.Config) error {
	ca, err := readPEM(c.CAFile, c.CAPEM)
	if err != nil {
		return err
	}
	if ca == nil {
		return nil
	}
	pool := x509.NewCertPool()
	if c.IncludeSystemCACertsPool {
		if pool, err = x509.SystemCertPool(); err != nil {
			return fmt.Errorf("failed to load the system cert pool: %w", err)
		}
	}
	if !pool.AppendCertsFromPEM(ca) {
		return errors.New("failed to parse the configured CA certificates")
	}
	cfg.RootCAs = pool

	return nil
}

// setCertificate installs the certificate a mutually authenticated
// connection presents.
func (c Config) setCertificate(cfg *tls.Config) error {
	cert, err := readPEM(c.CertFile, c.CertPEM)
	if err != nil {
		return err
	}
	key, err := readPEM(c.KeyFile, c.KeyPEM)
	if err != nil {
		return err
	}
	// Validate guarantees a certificate and its key are set together,
	// so a nil certificate implies a nil key here.
	if cert == nil {
		return nil
	}
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return fmt.Errorf("failed to load keypair: %w", err)
	}
	cfg.Certificates = []tls.Certificate{pair}

	return nil
}

func (c Config) minVersion() (uint16, error) {
	name := c.MinVersion
	if name == "" {
		name = defaultMinVersion
	}
	version, ok := tlsVersions[name]
	if !ok {
		return 0, fmt.Errorf(
			"unknown TLS min_version %q, accepted versions are %s",
			c.MinVersion,
			strings.Join(slices.Sorted(maps.Keys(tlsVersions)), ", "),
		)
	}
	return version, nil
}

func readPEM(path, inline string) ([]byte, error) {
	if inline != "" {
		return []byte(inline), nil
	}
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %q: %w", path, err)
	}
	return b, nil
}
