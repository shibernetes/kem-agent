package agent

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/shibernetes/kem-agent/internal/kube"
)

const configFile = "config.yaml"

func TestConfigFailureHasPositionAttrs(t *testing.T) {
	var (
		err   = parseFixtureErr(t, "bad-field-selector")
		attrs = attrMap(t, ConfigFailureAttrs(nil, configFile, err))
	)
	if got := attrs["error"]; got == "" || got[0] == '[' {
		t.Errorf("got error %q, want the position stripped from it", got)
	}
	for key, want := range map[string]string{
		"file":   configFile,
		"line":   "6",
		"column": "23",
	} {
		if got := attrs[key]; got != want {
			t.Errorf("got %s=%q, want %q", key, got, want)
		}
	}
}

// TestConfigFailurePositionsFilter asserts that a filter issue is
// positioned using a document line, and an expression column char.
func TestConfigFailurePositionsFilter(t *testing.T) {
	res := parseFixture(t, "bad-filter")

	built, err := New(res.Config, Options{
		Factories: testFactories(),
		Logger:    slog.New(slog.DiscardHandler),
		Kube:      kube.OfflineConfig(),
	})
	if err == nil {
		t.Fatalf("agent was build, expected an error: %v", built)
	}
	attrs := attrMap(t, ConfigFailureAttrs(res, configFile, err))

	for key, want := range map[string]string{
		"file":       configFile,
		"line":       "12",
		"column":     "7",
		"expression": "event.foo == 'Warning'",
	} {
		if got := attrs[key]; got != want {
			t.Errorf("got %s=%q, want %q", key, got, want)
		}
	}
}

func TestConfigFailureLeavesUnpositionedError(t *testing.T) {
	attrs := attrMap(t, ConfigFailureAttrs(nil, configFile, errors.New("no kubeconfig")))

	if got := attrs["error"]; got != "no kubeconfig" {
		t.Errorf("got error %q, want it unchanged", got)
	}
	if got, ok := attrs["file"]; ok {
		t.Errorf("got file %q, want none", got)
	}
}

func TestConfigWarningPositionsNode(t *testing.T) {
	res := parseFixture(t, "unknown-resource")

	if len(res.Warnings) != 1 {
		t.Fatalf("got %d warnings, want 1", len(res.Warnings))
	}
	attrs := attrMap(t, ConfigWarningAttrs(configFile, res.Warnings[0]))

	for key, want := range map[string]string{
		"file":   configFile,
		"line":   "6",
		"column": "9",
	} {
		if got := attrs[key]; got != want {
			t.Errorf("got %s=%q, want %q", key, got, want)
		}
	}
}

// TestConfigFailurePositionsFilterLine asserts that a filter written
// over several lines is positioned at the line holding the issue,
// and the expression reported is only that line.
func TestConfigFailurePositionsFilterLine(t *testing.T) {
	attrs := buildFailureAttrs(t, "bad-filter-lines")

	for key, want := range map[string]string{
		"file":       configFile,
		"line":       "14",
		"column":     "7",
		"expression": "event.unknown == 'x'",
	} {
		if got := attrs[key]; got != want {
			t.Errorf("got %s %q, want %q", key, got, want)
		}
	}
}

func TestConfigFailureOmitsUnpositionedColumn(t *testing.T) {
	attrs := buildFailureAttrs(t, "non-bool-filter")

	if got, ok := attrs["column"]; ok {
		t.Errorf("got column %q, want none", got)
	}
	if got := attrs["line"]; got != "12" {
		t.Errorf("got line %q, want %q", got, "12")
	}
}

func attrMap(t *testing.T, attrs []slog.Attr) map[string]string {
	t.Helper()

	out := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		out[attr.Key] = attr.Value.String()
	}
	return out
}

func buildFailureAttrs(t *testing.T, fixture string) map[string]string {
	t.Helper()

	res := parseFixture(t, fixture)
	built, err := New(res.Config, Options{
		Factories: testFactories(),
		Logger:    slog.New(slog.DiscardHandler),
		Kube:      kube.OfflineConfig(),
	})
	if err == nil {
		t.Fatalf("agent was built from fixture %s, want it rejected: %v", fixture, built)
	}
	return attrMap(t, ConfigFailureAttrs(res, configFile, err))
}
