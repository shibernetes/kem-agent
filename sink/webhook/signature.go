package webhook

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shibernetes/kem-agent/config/opaque"
	"github.com/shibernetes/kem-agent/internal/buffer"
	"github.com/shibernetes/kem-agent/sink"
)

const (
	IdentifierV1  = "v1"
	IdentifierV1a = "v1a"
)

const (
	messageIDPrefix     = "msg_"
	prefixSymmetric     = "whsec_"
	prefixAsymmetric    = "whsk_"
	minSymmetricLen     = 24
	maxSymmetricLen     = 64
	bufReleaseThreshold = 4 << 20 // 4 MiB
)

const (
	headerID        = "webhook-id"
	headerTimestamp = "webhook-timestamp"
	headerSignature = "webhook-signature"
)

// identifiers lists the supported signature schemes,
// symmetric HMAC-SHA256 and asymmetric Ed25519.
var identifiers = []string{IdentifierV1, IdentifierV1a}

// Signature configures Standard Webhooks request signing.
type Signature struct {
	Identifier string        `yaml:"identifier,omitempty"`
	Secret     opaque.String `yaml:"secret,omitempty"`
}

// Validate validates the signing settings.
func (s Signature) Validate() error {
	if s.Identifier == "" {
		// A secret set without an identifier could be mistaken for signing
		// being enabled, so it is refused rather than quietly ignored.
		if s.Secret != "" {
			return errors.New("identifier is required when secret is set")
		}
		return nil
	}
	if !slices.Contains(identifiers, s.Identifier) {
		return fmt.Errorf("unknown identifier %q, allowed values are %s", s.Identifier, strings.Join(identifiers, ", "))
	}
	if s.Secret == "" {
		return errors.New("secret is required when identifier is set")
	}
	// Building the signer decodes the secret, so an invalid key
	// is refused during validation rather than at first use.
	_, err := newSigner(s)

	return err
}

// A signer signs requests following the Standard Webhooks specification.
type signer struct {
	identifier string
	hmacKey    []byte
	ed25519Key ed25519.PrivateKey
	buf        []byte
}

// newSigner returns the signer for the configuration,
// or nil when signing is disabled.
func newSigner(cfg Signature) (*signer, error) {
	switch cfg.Identifier {
	case IdentifierV1:
		key, err := decodeSymmetricKey(cfg.Secret)
		if err != nil {
			return nil, err
		}
		return &signer{identifier: IdentifierV1, hmacKey: key}, nil
	case IdentifierV1a:
		key, err := decodeAsymmetricKey(cfg.Secret)
		if err != nil {
			return nil, err
		}
		return &signer{identifier: IdentifierV1a, ed25519Key: key}, nil
	}
	return nil, nil
}

// sign sets the signature headers on the request, computed over the
// message id, the timestamp and the body. The batch identifier is the
// message id, so a retried batch carries the one it was signed with.
func (s *signer) sign(header http.Header, batch sink.BatchID, body []byte, t time.Time) {
	var (
		sig []byte
		id  = messageIDPrefix + batch.String()
		ts  = strconv.FormatInt(t.Unix(), 10)
	)
	s.buf = buffer.Shrink(s.buf, bufReleaseThreshold)

	// The signed content is id.timestamp.body.
	s.buf = append(s.buf[:0], id...)
	s.buf = append(s.buf, '.')
	s.buf = append(s.buf, ts...)
	s.buf = append(s.buf, '.')

	if s.hmacKey != nil {
		mac := hmac.New(sha256.New, s.hmacKey)
		_, _ = mac.Write(s.buf)
		_, _ = mac.Write(body)
		sig = mac.Sum(nil)
	} else {
		s.buf = append(s.buf, body...)
		sig = ed25519.Sign(s.ed25519Key, s.buf)
	}
	header.Set(headerID, id)
	header.Set(headerTimestamp, ts)
	header.Set(headerSignature, s.identifier+","+base64.StdEncoding.EncodeToString(sig))
}

// decodeSymmetricKey decodes the secret a v1 signature is computed with.
func decodeSymmetricKey(secret opaque.String) ([]byte, error) {
	encoded, ok := strings.CutPrefix(string(secret), prefixSymmetric)
	if !ok {
		return nil, fmt.Errorf("%s secret must be prefixed with %q", IdentifierV1, prefixSymmetric)
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("failed to decode secret: %w", err)
	}
	if len(key) < minSymmetricLen || len(key) > maxSymmetricLen {
		return nil, fmt.Errorf("%s secret must decode to %d-%d bytes but decodes to %d", IdentifierV1, minSymmetricLen, maxSymmetricLen, len(key))
	}
	return key, nil
}

// decodeAsymmetricKey decodes the private key a v1a signature is
// computed with, in the seed and public key form the reference
// implementation uses.
func decodeAsymmetricKey(secret opaque.String) (ed25519.PrivateKey, error) {
	encoded, ok := strings.CutPrefix(string(secret), prefixAsymmetric)
	if !ok {
		return nil, fmt.Errorf("%s secret must be prefixed with %q", IdentifierV1a, prefixAsymmetric)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("failed to decode secret: %w", err)
	}
	if len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s secret must decode to %d bytes but decodes to %d", IdentifierV1a, ed25519.PrivateKeySize, len(decoded))
	}
	key := ed25519.PrivateKey(decoded)

	// The embedded public key is checked against the one the seed
	// derives, so a malformed key is refused here rather than producing
	// signatures that never verify.
	if !key.Equal(ed25519.NewKeyFromSeed(key.Seed())) {
		return nil, errors.New("secret is malformed, the public key does not match the seed")
	}
	return key, nil
}
