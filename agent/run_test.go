package agent

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/shibernetes/kem-agent/internal/kube"
)

// TestStartSinksStopsOnFailedOpen asserts that a sink whose destination can't
// be opened stops the agent startup, with an error naming the failed sink.
func TestStartSinksStopsOnFailedOpen(t *testing.T) {
	a, err := New(parseFixture(t, "missing-ca-file").Config, Options{
		Factories:  testFactories(),
		Logger:     slog.New(slog.DiscardHandler),
		KubeConfig: kube.OfflineConfig(),
	})
	if err != nil {
		t.Fatalf("failed to build agent: %v", err)
	}
	t.Cleanup(func() {
		_ = a.Close(context.Background())
	})
	err = a.startSinks(t.Context())
	if err == nil {
		t.Fatal("got no error, want the missing CA file reported")
	}
	if !strings.Contains(err.Error(), "sinks[collector]") {
		t.Errorf("got %v, want the error to name the sink", err)
	}
}
