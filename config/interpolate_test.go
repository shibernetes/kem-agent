package config

import (
	"strings"
	"testing"
	"time"

	"github.com/shibernetes/kem-agent/config/units"
)

func TestExpandVar(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"no reference":       {in: "collector:4317", want: "collector:4317"},
		"whole value":        {in: "${NAME}", want: "value"},
		"inside a value":     {in: "${NAME}:4317", want: "value:4317"},
		"twice":              {in: "${NAME}/${NAME}", want: "value/value"},
		"escaped dollar":     {in: "$$NAME", want: "$NAME"},
		"escaped then real":  {in: "$${NAME}", want: "${NAME}"},
		"trailing dollar":    {in: "cost$", want: "cost$"},
		"dollar before text": {in: "$NAME", want: "$NAME"},
	}
	resolve := func(string) (string, error) { return "value", nil }

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := expandVar(tc.in, resolve)
			if err != nil {
				t.Fatalf("failed to expand %q: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExpandVarErrors(t *testing.T) {
	cases := map[string]string{
		"unterminated":     "${NAME",
		"empty name":       "${}",
		"invalid rune":     "${NA-ME}",
		"leading digit":    "${1NAME}",
		"unset in the env": "${KEM_TEST_UNSET}",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := expandVar(in, envVarResolver); err == nil {
				t.Errorf("expanded %q, want a rejection", in)
			}
		})
	}
}

// TestParseExpandsNodeAndToken covers both readers of an expanded scalar,
// a plain field reading the node and a NodeUnmarshaler reading its token.
func TestParseExpandsNodeAndToken(t *testing.T) {
	t.Setenv("KEM_TEST_CLUSTER", "prod-eu-1")
	t.Setenv("KEM_TEST_TIMEOUT", "45s")
	t.Setenv("KEM_TEST_ENDPOINT", "collector:4317")

	cfg := parseFixture(t, "interpolated").Config

	if got := cfg.Service.ClusterName; got != "prod-eu-1" {
		t.Errorf("got cluster name %q, want prod-eu-1", got)
	}
	if got := cfg.Service.ShutdownTimeout; got != units.Duration(45*time.Second) {
		t.Errorf("got shutdown timeout %s, want 45s", got)
	}
	if got := fakeSinkConfig(t, cfg, "out").Endpoint; got != "collector:4317" {
		t.Errorf("got endpoint %q, want collector:4317", got)
	}
}

func TestParseExpandsWithoutCorruptingDocument(t *testing.T) {
	const injected = "host: \"evil\"\npipelines: {}\n"

	t.Setenv("KEM_TEST_CLUSTER", "prod-eu-1")
	t.Setenv("KEM_TEST_TIMEOUT", "45s")
	t.Setenv("KEM_TEST_ENDPOINT", injected)

	cfg := parseFixture(t, "interpolated").Config

	if got := fakeSinkConfig(t, cfg, "out").Endpoint; got != injected {
		t.Errorf("got endpoint %q, want it verbatim", got)
	}
	if got := len(cfg.Pipelines); got != 1 {
		t.Errorf("got %d pipelines, want the document unchanged with 1", got)
	}
}

func TestParseRejectsUnsetVariable(t *testing.T) {
	t.Setenv("KEM_TEST_CLUSTER", "prod-eu-1")
	t.Setenv("KEM_TEST_TIMEOUT", "45s")

	if err := parseFixtureErr(t, "interpolated"); !strings.Contains(err.Error(), "KEM_TEST_ENDPOINT") {
		t.Errorf("got %v, want it to mention KEM_TEST_ENDPOINT", err)
	}
}
