package kafka

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/shibernetes/kem-agent/sink"
)

// TestCreateBuildsNoClient asserts that creating a sink neither builds its
// client nor reads TLS certificates, so a configuration can be checked offline.
func TestCreateBuildsNoClient(t *testing.T) {
	cfg := testConfig()
	cfg.TLS.CAFile = filepath.Join(t.TempDir(), "missing.crt")

	s, err := NewFactory().Create(testOptions(), &cfg)
	if err != nil {
		t.Fatalf("failed to create sink: %v", err)
	}
	defer func() {
		_ = s.Shutdown(context.Background())
	}()

	if s.(*Sink).client != nil {
		t.Error("the client was built by Create, want it built by Open")
	}
}

// TestCreateRejectsForeignConfig asserts that a miswired registry
// is reported as a startup error rather than a panic.
func TestCreateRejectsForeignConfig(t *testing.T) {
	if _, err := NewFactory().Create(testOptions(), struct{}{}); err == nil {
		t.Error("another type's config was accepted, want it rejected")
	}
}

func testOptions() sink.Options {
	return sink.Options{
		Name:     "events",
		Logger:   slog.New(slog.DiscardHandler),
		Identity: testAgentMetadata(),
	}
}
