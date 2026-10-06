package kafka

import (
	"crypto/tls"
	"log/slog"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/shibernetes/kem-agent/config/units"
)

// TestClientTimeoutsFollowSendTimeout asserts that the client's own timeouts
// are derived from the send timeout, so none of them outlasts a delivery
// attempt.
func TestClientTimeoutsFollowSendTimeout(t *testing.T) {
	cases := map[string]struct {
		send     time.Duration
		delivery time.Duration
		request  time.Duration
		minAge   time.Duration
	}{
		"default":  {5 * time.Second, 5 * time.Second / 2, 5 * time.Second / 2, time.Second},
		"long":     {time.Minute, 30 * time.Second, 30 * time.Second, 5 * time.Second},
		"shortest": {2 * time.Second, time.Second, time.Second, 400 * time.Millisecond},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			cfg.SendTimeout = units.Duration(tc.send)

			cl := newTestClient(t, cfg)
			if got := cl.OptValue(kgo.RecordDeliveryTimeout); got != tc.delivery {
				t.Errorf("got delivery timeout %v, want %v", got, tc.delivery)
			}
			if got := cl.OptValue(kgo.ProduceRequestTimeout); got != tc.request {
				t.Errorf("got produce request timeout %v, want %v", got, tc.request)
			}
			if got := cl.OptValue(kgo.MetadataMinAge); got != tc.minAge {
				t.Errorf("got metadata refresh interval %v, want %v", got, tc.minAge)
			}
		})
	}
}

// TestClientAcks asserts that idempotence is enabled only when all in-sync
// replicas must acknowledge a record, and that an in-flight record can
// always be canceled at the attempt's deadline.
func TestClientAcks(t *testing.T) {
	cases := map[RequiredAcks]struct {
		acks       kgo.Acks
		idempotent bool
	}{
		RequiredAcksAll:    {kgo.AllISRAcks(), true},
		RequiredAcksLeader: {kgo.LeaderAck(), false},
		RequiredAcksNone:   {kgo.NoAck(), false},
	}
	for acks, tc := range cases {
		t.Run(string(acks), func(t *testing.T) {
			cfg := testConfig()
			cfg.RequiredAcks = acks

			cl := newTestClient(t, cfg)
			if got := cl.OptValue(kgo.RequiredAcks); got != tc.acks {
				t.Errorf("got acks %v, want %v", got, tc.acks)
			}
			switch disabled := cl.OptValue(kgo.DisableIdempotentWrite) == true; {
			case tc.idempotent && disabled:
				t.Error("idempotent writes are disabled, want them enabled")
			case !tc.idempotent && !disabled:
				t.Error("idempotent writes are enabled, want them disabled")
			}
			if cl.OptValue(kgo.AllowIdempotentProduceCancellation) != true {
				t.Error("in-flight records can't be canceled")
			}
		})
	}
}

// TestClientUsesTLS asserts that the client connects with TLS unless the
// configuration turns it off.
func TestClientUsesTLS(t *testing.T) {
	cfg := testConfig()

	if tc, _ := newTestClient(t, cfg).OptValue(kgo.DialTLSConfig).(*tls.Config); tc == nil {
		t.Error("the client connects in plaintext, want TLS")
	}
	cfg.TLS.Insecure = true

	if tc, _ := newTestClient(t, cfg).OptValue(kgo.DialTLSConfig).(*tls.Config); tc != nil {
		t.Error("the client connects with TLS, want plaintext")
	}
}

// TestCodecOf asserts that each compression selects the matching codec.
func TestCodecOf(t *testing.T) {
	cases := map[Compression]kgo.CompressionCodec{
		CompressionNone:   kgo.NoCompression(),
		CompressionGzip:   kgo.GzipCompression(),
		CompressionSnappy: kgo.SnappyCompression(),
		CompressionLZ4:    kgo.Lz4Compression(),
		CompressionZstd:   kgo.ZstdCompression(),
	}
	for c, want := range cases {
		if got := codecOf(c); got != want {
			t.Errorf("compression %q selects codec %v, want %v", c, got, want)
		}
	}
}

// TestMechanismOf asserts that each SASL mechanism authenticates with
// the mechanism of the same name.
func TestMechanismOf(t *testing.T) {
	for _, m := range saslMechanisms {
		got := mechanismOf(SASLConfig{
			Mechanism: m,
			Username:  "agent",
			Password:  "s3cr3t",
		})
		if got.Name() != string(m) {
			t.Errorf("mechanism %q authenticates with %q", m, got.Name())
		}
	}
}

func newTestClient(t *testing.T, cfg Config) *kgo.Client {
	t.Helper()

	opts, err := clientOptions(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("failed to build client options: %v", err)
	}
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	t.Cleanup(cl.Close)

	return cl
}
