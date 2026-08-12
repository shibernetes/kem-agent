package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/shibernetes/kem-agent/config"
	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/sink/metrics"
	"github.com/shibernetes/kem-agent/sink/stdout"
)

// testFactories returns the sinks the fixtures declare.
func testFactories() sink.Factories {
	return sink.Factories{
		stdout.TypeName:  stdout.NewFactory(),
		metrics.TypeName: metrics.NewFactory(),
	}
}

// parseFixture parses a fixture and fails when it is rejected.
func parseFixture(t *testing.T, name string) *config.Result {
	t.Helper()

	res, err := config.Parse(readFixture(t, name), testFactories())
	if err != nil {
		t.Fatalf("failed to parse fixture %s: %v", name, err)
	}
	return res
}

// parseFixtureErr parses a fixture and fails when it is accepted.
func parseFixtureErr(t *testing.T, name string) error {
	t.Helper()

	_, err := config.Parse(readFixture(t, name), testFactories())
	if err == nil {
		t.Fatalf("parsed fixture %s successfully, want it to fail", name)
	}
	return err
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", fmt.Sprintf("%s.yaml", name)))
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	return data
}
