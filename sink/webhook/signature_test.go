package webhook

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shibernetes/kem-agent/config/opaque"
	"github.com/shibernetes/kem-agent/sink"
)

// signedAt is the time every signature in this file is computed at.
var signedAt = time.Date(2009, 1, 3, 18, 15, 5, 0, time.UTC)

func TestSignatureValidate(t *testing.T) {
	cases := map[string]struct {
		sig   Signature
		valid bool
	}{
		"unsigned":          {Signature{}, true},
		"v1":                {Signature{Identifier: IdentifierV1, Secret: symmetricSecret(32)}, true},
		"v1a":               {Signature{Identifier: IdentifierV1a, Secret: asymmetricSecret()}, true},
		"secret without id": {Signature{Secret: symmetricSecret(32)}, false},
		"id without secret": {Signature{Identifier: IdentifierV1}, false},
		"unknown id":        {Signature{Identifier: "v2", Secret: symmetricSecret(32)}, false},
		"uppercase id":      {Signature{Identifier: "V1", Secret: symmetricSecret(32)}, false},
		"symmetric as v1a":  {Signature{Identifier: IdentifierV1a, Secret: symmetricSecret(32)}, false},
		"asymmetric as v1":  {Signature{Identifier: IdentifierV1, Secret: asymmetricSecret()}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.sig.Validate()
			switch {
			case tc.valid && err != nil:
				t.Errorf("got %v, want the settings validated", err)
			case !tc.valid && err == nil:
				t.Error("got no error, want the settings rejected")
			}
		})
	}
}

func TestDecodeSymmetricKey(t *testing.T) {
	cases := map[string]struct {
		secret opaque.String
		valid  bool
	}{
		"minimum length":       {symmetricSecret(minSymmetricLen), true},
		"maximum length":       {symmetricSecret(maxSymmetricLen), true},
		"below minimum length": {symmetricSecret(minSymmetricLen - 1), false},
		"above maximum length": {symmetricSecret(maxSymmetricLen + 1), false},
		"no prefix":            {opaque.String(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))), false},
		"wrong prefix":         {asymmetricSecret(), false},
		"prefix only":          {prefixSymmetric, false},
		"not base64":           {prefixSymmetric + "not base64", false},
		"empty":                {"", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := decodeSymmetricKey(tc.secret)
			switch {
			case tc.valid && err != nil:
				t.Errorf("got %v, want the secret accepted", err)
			case !tc.valid && err == nil:
				t.Error("got no error, want the secret rejected")
			}
		})
	}
}

func TestDecodeAsymmetricKey(t *testing.T) {
	cases := map[string]struct {
		secret opaque.String
		valid  bool
	}{
		"complete key": {asymmetricSecret(), true},
		"no prefix":    {opaque.String(base64.StdEncoding.EncodeToString(asymmetricKey())), false},
		"wrong prefix": {symmetricSecret(32), false},
		"seed only":    {asymmetricSecretOf(asymmetricKey().Seed()), false},
		"short key":    {asymmetricSecretOf(make([]byte, 16)), false},
		"not base64":   {prefixAsymmetric + "not base64", false},
		"empty":        {"", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := decodeAsymmetricKey(tc.secret)
			switch {
			case tc.valid && err != nil:
				t.Errorf("got %v, want the secret accepted", err)
			case !tc.valid && err == nil:
				t.Error("got no error, want the secret rejected")
			}
		})
	}
}

// TestDecodeAsymmetricKeyMismatch asserts that a key whose public
// half was not derived from its seed is refused where it is read,
// rather than producing signatures that never verify.
func TestDecodeAsymmetricKeyMismatch(t *testing.T) {
	key := bytes.Clone(asymmetricKey())
	key[ed25519.SeedSize]++

	if _, err := decodeAsymmetricKey(asymmetricSecretOf(key)); err == nil {
		t.Error("the secret was accepted, want the mismatch refused")
	}
}

func TestSignerSignsV1(t *testing.T) {
	var (
		signer = mustSigner(t, IdentifierV1, symmetricSecret(32))
		body   = []byte(`foobar`)
		header = http.Header{}
	)
	signer.sign(header, sink.NewBatchID(), body, signedAt)

	mac := hmac.New(sha256.New, symmetricKey(32))
	mac.Write(signedContent(header, body))

	want := IdentifierV1 + "," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if got := header.Get(headerSignature); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSignerSignsV1a(t *testing.T) {
	var (
		signer = mustSigner(t, IdentifierV1a, asymmetricSecret())
		body   = []byte(`foobar`)
		header = http.Header{}
	)
	signer.sign(header, sink.NewBatchID(), body, signedAt)

	encoded, ok := strings.CutPrefix(header.Get(headerSignature), IdentifierV1a+",")
	if !ok {
		t.Fatalf("got %q, want it prefixed with %q", header.Get(headerSignature), IdentifierV1a+",")
	}
	sig, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("signature is not base64-encoded: %v", err)
	}
	pub := asymmetricKey().Public().(ed25519.PublicKey)
	if !ed25519.Verify(pub, signedContent(header, body), sig) {
		t.Error("signature did not verify against the public key")
	}
}

func TestSignerSignsHeaders(t *testing.T) {
	var (
		signer = mustSigner(t, IdentifierV1, symmetricSecret(32))
		batch  = sink.NewBatchID()
		header = http.Header{}
	)
	signer.sign(header, batch, []byte("{}"), signedAt)

	if want := messageIDPrefix + batch.String(); header.Get(headerID) != want {
		t.Errorf("got id %q, want %q", header.Get(headerID), want)
	}
	if want := strconv.FormatInt(signedAt.Unix(), 10); header.Get(headerTimestamp) != want {
		t.Errorf("got timestamp %q, want %q", header.Get(headerTimestamp), want)
	}
	if header.Get(headerSignature) == "" {
		t.Error("got no signature header, want one")
	}
}

func TestSignerSignsSameBatchIdentically(t *testing.T) {
	var (
		signer = mustSigner(t, IdentifierV1, symmetricSecret(32))
		batch  = sink.NewBatchID()
		body   = []byte("{}")
		first  = http.Header{}
		second = http.Header{}
		third  = http.Header{}
	)
	signer.sign(first, batch, body, signedAt)
	signer.sign(second, batch, body, signedAt)
	signer.sign(third, batch, body, signedAt.Add(time.Hour))

	if first.Get(headerSignature) != second.Get(headerSignature) {
		t.Error("the same batch signed at the same time produced two signatures, want one")
	}
	if first.Get(headerID) != third.Get(headerID) {
		t.Error("the same batch produced two message ids, want one")
	}
	if first.Get(headerSignature) == third.Get(headerSignature) {
		t.Error("got the same signature for a later time, want it to differ")
	}
}

// TestSignerHashesBodyInPlace asserts that a symmetric
// signature does not copy the payload.
func TestSignerHashesBodyInPlace(t *testing.T) {
	var (
		signer = mustSigner(t, IdentifierV1, symmetricSecret(32))
		body   = bytes.Repeat([]byte("a"), 1<<20)
	)
	signer.sign(http.Header{}, sink.NewBatchID(), body, signedAt)

	if got := cap(signer.buf); got >= len(body) {
		t.Errorf("got a buffer of %d bytes, want less than the %d byte body", got, len(body))
	}
}

func TestSignerReleasesBuffer(t *testing.T) {
	signer := mustSigner(t, IdentifierV1a, asymmetricSecret())

	signer.buf = make([]byte, 1<<10, bufReleaseThreshold+1)
	capacity := cap(signer.buf)

	signer.sign(http.Header{}, sink.NewBatchID(), []byte("{}"), signedAt)

	if got := cap(signer.buf); got >= capacity {
		t.Errorf("got capacity %d, want less than %d", got, capacity)
	}
}

func TestSignerKeepsBusyBuffer(t *testing.T) {
	signer := mustSigner(t, IdentifierV1a, asymmetricSecret())

	signer.buf = make([]byte, bufReleaseThreshold+1)
	capacity := cap(signer.buf)

	signer.sign(http.Header{}, sink.NewBatchID(), []byte("{}"), signedAt)

	if got := cap(signer.buf); got < capacity {
		t.Errorf("got capacity %d, want the %d it started with", got, capacity)
	}
}

func mustSigner(t *testing.T, identifier string, secret opaque.String) *signer {
	t.Helper()

	signer, err := newSigner(Signature{Identifier: identifier, Secret: secret})
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}
	if signer == nil {
		t.Fatalf("identifier %q yielded no signer", identifier)
	}
	return signer
}

func signedContent(header http.Header, body []byte) []byte {
	return []byte(header.Get(headerID) + "." + header.Get(headerTimestamp) + "." + string(body))
}

// symmetricKey returns a key of n bytes.
func symmetricKey(n int) []byte {
	return bytes.Repeat([]byte{0x2a}, n)
}

// symmetricSecret returns a v1 secret carrying a key of n bytes.
func symmetricSecret(n int) opaque.String {
	return opaque.String(prefixSymmetric + base64.StdEncoding.EncodeToString(symmetricKey(n)))
}

// asymmetricKey returns the private key every v1a secret carries,
// derived from a fixed seed so a signature is reproducible.
func asymmetricKey() ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x11}, ed25519.SeedSize))
}

func asymmetricSecret() opaque.String {
	return asymmetricSecretOf(asymmetricKey())
}

func asymmetricSecretOf(key []byte) opaque.String {
	return opaque.String(prefixAsymmetric + base64.StdEncoding.EncodeToString(key))
}
