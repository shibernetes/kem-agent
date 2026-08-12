package enricher

import (
	"maps"
	"testing"
	"time"

	"github.com/shibernetes/kem-agent/config/units"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()

	if got, want := time.Duration(cfg.SyncTimeout), time.Minute; got != want {
		t.Errorf("got sync timeout %s, want %s", got, want)
	}
	if !cfg.Labels.Enabled {
		t.Error("labels are left out by default, want them copied")
	}
	if cfg.Annotations.Enabled {
		t.Error("annotations are copied by default, want them left out")
	}
}

func TestConfigAcceptsResources(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		"nothing declared":       nil,
		"core resource":          {"pods"},
		"grouped resource":       {"deployments.apps"},
		"custom resource":        {"applications.argoproj.io"},
		"dash in the name":       {"my-widgets.example.com"},
		"several resources":      {"pods", "nodes", "deployments.apps"},
		"one name in two groups": {"pods", "pods.metrics.k8s.io"},
	}
	for name, resources := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := DefaultConfig()
			cfg.Resources = resources

			if err := cfg.Validate(); err != nil {
				t.Errorf("the configuration was rejected, want it accepted: %v", err)
			}
		})
	}
}

func TestConfigRejectsResources(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		"kind":                     {"Pod"},
		"pluralized kind":          {"Pods"},
		"underscore in name":       {"my_crd"},
		"leading digit":            {"1pods"},
		"subresource":              {"pods/log"},
		"empty name":               {""},
		"no resource before a dot": {".apps"},
		"doubled dot":              {"pods..apps"},
		"uppercased group":         {"deployments.Apps"},
		"same name twice":          {"pods", "pods"},
		"two spellings of one":     {"pods", "pods."},
	}
	for name, resources := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := DefaultConfig()
			cfg.Resources = resources

			if err := cfg.Validate(); err == nil {
				t.Error("the configuration was accepted, want it rejected")
			}
		})
	}
}

func TestConfigRejectsSyncTimeout(t *testing.T) {
	t.Parallel()

	cases := map[string]units.Duration{
		"unbounded": 0,
		"negative":  units.Duration(-time.Second),
	}
	for name, timeout := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := DefaultConfig()
			cfg.SyncTimeout = timeout

			if err := cfg.Validate(); err == nil {
				t.Error("the configuration was accepted, want it rejected")
			}
		})
	}
}

func TestConfigAcceptsAllowlists(t *testing.T) {
	t.Parallel()

	cases := map[string]Allowlist{
		"empty":     {},
		"every key": {Enabled: true},
		"list":      {Enabled: true, Keys: []string{"app", "team"}},
	}
	for name, allowlist := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := DefaultConfig()
			cfg.Labels = allowlist
			cfg.Annotations = allowlist

			if err := cfg.Validate(); err != nil {
				t.Errorf("the configuration was rejected, want it accepted: %v", err)
			}
		})
	}
}

func TestConfigRejectsAllowlists(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		labels      Allowlist
		annotations Allowlist
	}{
		"labels listed but disabled":      {labels: Allowlist{Keys: []string{"app"}}},
		"annotations listed but disabled": {annotations: Allowlist{Keys: []string{"app"}}},
		"empty label key":                 {labels: Allowlist{Enabled: true, Keys: []string{""}}},
		"empty annotation key":            {annotations: Allowlist{Enabled: true, Keys: []string{"app", ""}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := DefaultConfig()
			cfg.Labels = tc.labels
			cfg.Annotations = tc.annotations

			if err := cfg.Validate(); err == nil {
				t.Error("the configuration was accepted, want it rejected")
			}
		})
	}
}

func TestAllowlistApplies(t *testing.T) {
	t.Parallel()

	metadata := map[string]string{"app": "web", "team": "hosting", "tier": "front"}

	cases := map[string]struct {
		allowlist Allowlist
		metadata  map[string]string
		want      map[string]string
	}{
		"missing":            {metadata: metadata},
		"every key":          {allowlist: Allowlist{Enabled: true}, metadata: metadata, want: metadata},
		"listed key":         {allowlist: Allowlist{Enabled: true, Keys: []string{"app"}}, metadata: metadata, want: map[string]string{"app": "web"}},
		"two listed keys":    {allowlist: Allowlist{Enabled: true, Keys: []string{"app", "tier"}}, metadata: metadata, want: map[string]string{"app": "web", "tier": "front"}},
		"unset key":          {allowlist: Allowlist{Enabled: true, Keys: []string{"zone"}}, metadata: metadata},
		"one key of two set": {allowlist: Allowlist{Enabled: true, Keys: []string{"app", "zone"}}, metadata: metadata, want: map[string]string{"app": "web"}},
		"empty map":          {allowlist: Allowlist{Enabled: true}, metadata: map[string]string{}},
		"no map at all":      {allowlist: Allowlist{Enabled: true}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := tc.allowlist.apply(tc.metadata); !maps.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAllowlistAppliesWithoutCopying(t *testing.T) {
	t.Parallel()

	m := map[string]string{"app": "web"}

	// An allowlist that keeps every key returns the map as-is.
	got := Allowlist{Enabled: true}.apply(m)
	if got == nil {
		t.Fatal("the returned map is nil, want the map itself")
	}
	got["app"] = "api"

	if m["app"] != "api" {
		t.Error("the returned map is a copy, want the map itself")
	}
}
