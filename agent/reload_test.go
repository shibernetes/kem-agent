package agent

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shibernetes/kem-agent/internal/kube"
)

// TestReloadConfigSkipsUnchangedFile asserts that a reload does nothing
// when the config file has not changed since startup.
func TestReloadConfigSkipsUnchangedFile(t *testing.T) {
	a, _ := newReloadAgent(t, "literal-lookalike", slog.New(slog.DiscardHandler))
	startup := a.filters.Load()

	a.reloadConfig(t.Context())

	if a.filters.Load() != startup {
		t.Error("the reload swapped the filters of an unchanged file")
	}
}

// TestReloadConfigLogsOnlyFilterChanges asserts that a reload logs the
// filters as reloaded only when one of them changed.
func TestReloadConfigLogsOnlyFilterChanges(t *testing.T) {
	var (
		logs      bytes.Buffer
		startup   = readFixture(t, "literal-lookalike")
		edited    = bytes.ReplaceAll(startup, []byte("regardingObject"), []byte("owner"))
		commented = append(slices.Clone(edited), "# a comment\n"...)
	)
	a, path := newReloadAgent(t, "literal-lookalike", slog.New(slog.NewTextHandler(&logs, nil)))

	steps := []struct {
		name     string
		data     []byte
		reloaded bool
	}{
		{name: "filter edited", data: edited, reloaded: true},
		{name: "comment added", data: commented, reloaded: false},
		{name: "edit reverted", data: startup, reloaded: true},
	}
	for _, step := range steps {
		if err := os.WriteFile(path, step.data, 0o600); err != nil {
			t.Fatalf("failed to write config file: %v", err)
		}
		logs.Reset()
		a.reloadConfig(t.Context())

		logged := strings.Contains(logs.String(), "filters reloaded")
		switch {
		case logged && !step.reloaded:
			t.Errorf("%s: the filters were logged as reloaded, want nothing logged", step.name)
		case !logged && step.reloaded:
			t.Errorf("%s: the filters were not logged as reloaded", step.name)
		}
	}
}

func newReloadAgent(t *testing.T, fixture string, logger *slog.Logger) (*Agent, string) {
	t.Helper()

	var (
		data = readFixture(t, fixture)
		path = filepath.Join(t.TempDir(), "config.yaml")
	)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}
	a, err := New(parseFixture(t, fixture).Config, Options{
		Factories:  testFactories(),
		Logger:     logger,
		ConfigPath: path,
		ConfigData: data,
		KubeConfig: kube.OfflineConfig(),
	})
	if err != nil {
		t.Fatalf("failed to build agent: %v", err)
	}
	t.Cleanup(func() {
		_ = a.Close(context.Background())
	})
	return a, path
}
