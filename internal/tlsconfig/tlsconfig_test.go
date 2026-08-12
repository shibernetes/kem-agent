package tlsconfig

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildInsecure(t *testing.T) {
	got, err := Config{Insecure: true}.Build()
	if err != nil {
		t.Fatalf("failed to build TLS config: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want a nil config", got)
	}
}

func TestBuildZeroValueIsSecure(t *testing.T) {
	got := mustBuild(t, Config{})

	if got.InsecureSkipVerify {
		t.Error("got verification off, want the zero value to verify")
	}
	if got.MinVersion != tls.VersionTLS12 {
		t.Errorf("got min version %#x, want %#x", got.MinVersion, tls.VersionTLS12)
	}
}

func TestBuildMinVersion(t *testing.T) {
	cases := map[string]uint16{
		"":    tls.VersionTLS12,
		"1.0": tls.VersionTLS10,
		"1.1": tls.VersionTLS11,
		"1.2": tls.VersionTLS12,
		"1.3": tls.VersionTLS13,
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			if got := mustBuild(t, Config{MinVersion: name}).MinVersion; got != want {
				t.Errorf("got %#x, want %#x", got, want)
			}
		})
	}
}

func TestBuildPEMSources(t *testing.T) {
	kp := newKeyPair(t)

	cases := map[string]struct {
		cfg       Config
		wantCA    bool
		wantCerts int
	}{
		"ca from a file":     {cfg: Config{CAFile: kp.certFile}, wantCA: true},
		"ca inline":          {cfg: Config{CAPEM: kp.certPEM}, wantCA: true},
		"key pair from disk": {cfg: Config{CertFile: kp.certFile, KeyFile: kp.keyFile}, wantCerts: 1},
		"key pair inline":    {cfg: Config{CertPEM: kp.certPEM, KeyPEM: kp.keyPEM}, wantCerts: 1},
		"key pair mixed":     {cfg: Config{CertFile: kp.certFile, KeyPEM: kp.keyPEM}, wantCerts: 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := mustBuild(t, tc.cfg)

			switch {
			case tc.wantCA && got.RootCAs == nil:
				t.Error("got no root CAs, want the configured CA installed")
			case !tc.wantCA && got.RootCAs != nil:
				t.Error("got root CAs, want none installed")
			}
			if len(got.Certificates) != tc.wantCerts {
				t.Errorf("got %d certificates, want %d", len(got.Certificates), tc.wantCerts)
			}
		})
	}
}

func TestBuildCAReplacesSystemPool(t *testing.T) {
	system, err := x509.SystemCertPool()
	if err != nil || system.Equal(x509.NewCertPool()) {
		t.Skip("no system certificate pool to compare against")
	}
	kp := newKeyPair(t)

	replaced := mustBuild(t, Config{CAPEM: kp.certPEM}).RootCAs
	included := mustBuild(t, Config{CAPEM: kp.certPEM, IncludeSystemCACertsPool: true}).RootCAs

	// A configured authority replaces the system roots,
	// which is the opposite of the natural assumption.
	if replaced.Equal(included) {
		t.Error("got the same cert pool, want the system roots included only when configured")
	}
}

func TestBuildErrors(t *testing.T) {
	var (
		absent    = filepath.Join(t.TempDir(), "absent.pem")
		malformed = writeFile(t, "bad.pem", "not a pem")
		kp        = newKeyPair(t)
	)
	cases := map[string]Config{
		"unknown min version": {MinVersion: "1.4"},
		"missing ca file":     {CAFile: absent},
		"malformed ca file":   {CAFile: malformed},
		"missing cert file":   {CertFile: absent, KeyFile: kp.keyFile},
		"missing key file":    {CertFile: kp.certFile, KeyFile: absent},
		"mismatched pair":     {CertFile: kp.certFile, KeyFile: kp.certFile},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := cfg.Build(); err == nil {
				t.Error("got no error, want the build to fail")
			}
		})
	}
}

func TestValidate(t *testing.T) {
	kp := newKeyPair(t)

	cases := map[string]Config{
		"empty":       {},
		"insecure":    {Insecure: true},
		"paths only":  {CAFile: "ca", CertFile: "c", KeyFile: "k"},
		"inline pair": {CertPEM: kp.certPEM, KeyPEM: kp.keyPEM},
		"mixed forms": {CertFile: "c", KeyPEM: kp.keyPEM},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err != nil {
				t.Errorf("got %v, want the config to validate", err)
			}
		})
	}
}

func TestValidateErrors(t *testing.T) {
	kp := newKeyPair(t)

	cases := map[string]Config{
		"unknown min version": {MinVersion: "1.4"},
		"both ca forms":       {CAFile: "ca", CAPEM: kp.certPEM},
		"both cert forms":     {CertFile: "c", CertPEM: kp.certPEM, KeyFile: "k"},
		"both key forms":      {CertFile: "c", KeyFile: "k", KeyPEM: kp.keyPEM},
		"cert without key":    {CertFile: "c"},
		"key without cert":    {KeyFile: "k"},
		"malformed inline ca": {CAPEM: "not a pem"},
		"mismatched inline":   {CertPEM: kp.certPEM, KeyPEM: kp.certPEM},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Error("got no error, want validation to fail")
			}
		})
	}
}

func TestValidateIgnoresMissingFiles(t *testing.T) {
	// File are not read, so a config naming files that exist only
	// in a production environment still validates in a CI runner.
	absent := filepath.Join(t.TempDir(), "absent.pem")

	cfg := Config{CAFile: absent, CertFile: absent, KeyFile: absent}
	if err := cfg.Validate(); err != nil {
		t.Errorf("got %v, want a missing path to validate", err)
	}
}

func TestValidateNamesTheAcceptedVersions(t *testing.T) {
	err := Config{MinVersion: "1.4"}.Validate()
	if err == nil {
		t.Fatal("got no error, want an unknown version rejected")
	}
	const want = `unknown TLS min_version "1.4", accepted versions are 1.0, 1.1, 1.2, 1.3`
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}

func mustBuild(t *testing.T, cfg Config) *tls.Config {
	t.Helper()

	tlsConfig, err := cfg.Build()
	if err != nil {
		t.Fatalf("failed to build TLS config: %v", err)
	}
	if tlsConfig == nil {
		t.Fatal("got no config for a secure connection")
	}
	return tlsConfig
}

// keyPair holds one self-signed certificate in both of the
// forms a config may supply it in, inline or via files.
type keyPair struct {
	certPEM  string
	keyPEM   string
	certFile string
	keyFile  string
}

func newKeyPair(t *testing.T) keyPair {
	t.Helper()

	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "kem-agent"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDer, err := x509.CreateCertificate(rand.Reader, &template, &template, &pk.PublicKey, pk)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	keyDer, err := x509.MarshalPKCS8PrivateKey(pk)
	if err != nil {
		t.Fatalf("failed to marshal key: %v", err)
	}
	kp := keyPair{
		certPEM: encodePEM("CERTIFICATE", certDer),
		keyPEM:  encodePEM("PRIVATE KEY", keyDer),
	}
	kp.certFile = writeFile(t, "cert.pem", kp.certPEM)
	kp.keyFile = writeFile(t, "key.pem", kp.keyPEM)

	return kp
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write file %q: %v", path, err)
	}
	return path
}

func encodePEM(blockType string, der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}))
}
