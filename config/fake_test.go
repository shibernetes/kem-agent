package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/sink"
)

const (
	// fakeType is the type key the fake sink is registered under.
	fakeType = "fake"

	// defaultFakeMethod is a value only the factory sets, so a fixture
	// omitting it demonstrates whether the block decoded onto the default.
	defaultFakeMethod = "POST"
)

var _ sink.Factory = fakeFactory{}

// fakeFactory builds the sinks the config tests declare.
type fakeFactory struct{}

// CreateDefaultConfig implements the [sink.Factory] interface.
func (fakeFactory) CreateDefaultConfig() sink.Config {
	return &fakeConfig{Type: fakeType, Method: defaultFakeMethod}
}

func (fakeFactory) Create(sink.Options, sink.Config) (sink.Sink, error) {
	return nil, nil
}

type fakeConfig struct {
	Type     string   `yaml:"type"`
	Endpoint string   `yaml:"endpoint"`
	Method   string   `yaml:"method,omitempty"`
	Metrics  []string `yaml:"metrics,omitempty"`
}

// Validate implements the [Validator] interface.
func (c *fakeConfig) Validate() error {
	return diag.Required(c)
}

// MetricNames implements the metricsNamer interface.
func (c *fakeConfig) MetricNames() []string {
	return c.Metrics
}

// parseFixture parses the named fixture and fails when it is rejected.
func parseFixture(t *testing.T, name string) *Result {
	t.Helper()

	res, err := Parse(readFixture(t, name), sink.Factories{fakeType: fakeFactory{}})
	if err != nil {
		t.Fatalf("failed to parse fixture %s: %v", name, err)
	}
	return res
}

// parseFixtureErr parses the named fixture and fails when it is accepted.
func parseFixtureErr(t *testing.T, name string) error {
	t.Helper()

	_, err := Parse(readFixture(t, name), sink.Factories{fakeType: fakeFactory{}})
	if err == nil {
		t.Fatalf("parsed fixture %s successfully, want it to fail", name)
	}
	return err
}

func fakeSinkConfig(t *testing.T, cfg *Config, name string) *fakeConfig {
	t.Helper()

	component, ok := cfg.Sinks[name]
	if !ok {
		t.Fatalf("got no sink %q", name)
	}
	fake, ok := component.Config.(*fakeConfig)
	if !ok {
		t.Fatalf("got sink config %T, want *fakeConfig", component.Config)
	}
	return fake
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", fmt.Sprintf("%s.yaml", name)))
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	return data
}
