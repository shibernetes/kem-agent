package source

import (
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/shibernetes/kem-agent/config/units"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	// The default declares no watch, so it is not a configuration
	// the agent can run with.
	if err := cfg.Validate(); err == nil {
		t.Error("the default configuration was accepted, want it rejected")
	}
	if !cfg.Sanitizers.FieldLimits.Enabled {
		t.Error("field limits are off by default, want them on")
	}
	if cfg.BootstrapEventMaxAge != 0 {
		t.Errorf("got bootstrap event max age %s, want it unbounded", time.Duration(cfg.BootstrapEventMaxAge))
	}
}

func TestConfigAcceptsBootstrapEventMaxAge(t *testing.T) {
	cases := map[string]units.Duration{
		"unbounded": 0,
		"bounded":   units.Duration(2 * time.Hour),
	}
	for name, maxAge := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			cfg.BootstrapEventMaxAge = maxAge

			if err := cfg.Validate(); err != nil {
				t.Errorf("the configuration was rejected, want it accepted: %v", err)
			}
		})
	}
}

func TestConfigRejectsBootstrapEventMaxAge(t *testing.T) {
	cfg := testConfig()
	cfg.BootstrapEventMaxAge = units.Duration(-time.Second)

	if err := cfg.Validate(); err == nil {
		t.Error("a negative event max age was accepted, want it rejected")
	}
}

// TestConfigValidatesComponents asserts that the source reports what its
// own components reject, rather than validating only its own fields.
func TestConfigValidatesComponents(t *testing.T) {
	cases := map[string]func(*Config){
		"sanitizers": func(c *Config) { c.Sanitizers.MetadataCountLimit.MaxLabels = -1 },
		"enrichment": func(c *Config) { c.Enrichment.SyncTimeout = 0 },
	}
	for name, invalidate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			invalidate(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Error("the configuration was accepted, want it rejected")
			}
		})
	}
}

func TestValidateWatchesAccepts(t *testing.T) {
	cases := map[string][]WatchConfig{
		"all namespaces":      {{}},
		"one namespace":       {{Namespace: "team-a"}},
		"several namespaces":  {{Namespace: "team-a"}, {Namespace: "team-b"}},
		"with selectors":      {{Namespace: "team-a", LabelSelector: "app=web", FieldSelector: "type=Warning"}},
		"selectors differing": {{Namespace: "team-a", FieldSelector: "type=Warning"}, {Namespace: "team-b"}},
	}
	for name, watches := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateWatches(watches); err != nil {
				t.Errorf("the watches were rejected, want them accepted: %v", err)
			}
		})
	}
}

func TestValidateWatchesRejects(t *testing.T) {
	cases := map[string][]WatchConfig{
		"nil":                          nil,
		"empty list":                   {},
		"all namespaces first":         {{}, {Namespace: "team-a"}},
		"all namespaces last":          {{Namespace: "team-a"}, {}},
		"duplicate namespace":          {{Namespace: "team-a"}, {Namespace: "team-a"}},
		"one namespace repeated apart": {{Namespace: "team-a"}, {Namespace: "team-b"}, {Namespace: "team-a"}},
		"invalid namespace":            {{Namespace: "Team-A"}},
	}
	for name, watches := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateWatches(watches); err == nil {
				t.Error("the watches were accepted, want them rejected")
			}
		})
	}
}

// TestValidateWatchesRejectsRepeatedEmptyWatch asserts that the empty watch
// check runs ahead of the duplicate one. Two watches over all namespacess are
// both a duplicate and a watch that must stand alone.
func TestValidateWatchesRejectsRepeatedEmptyWatch(t *testing.T) {
	err := validateWatches([]WatchConfig{{}, {}})
	if err == nil {
		t.Fatal("the watches were accepted, want them rejected")
	}
	if !strings.Contains(err.Error(), "declared alone") {
		t.Errorf("got %q, want it to report a watch that must be declared alone", err)
	}
}

func TestWatchConfigAcceptsNamespace(t *testing.T) {
	cases := map[string]string{
		"all namespaces":  "",
		"one word":        "platform",
		"dashed":          "team-a",
		"digits":          "team-1",
		"longest allowed": strings.Repeat("a", validation.DNS1123LabelMaxLength),
	}
	for name, namespace := range cases {
		t.Run(name, func(t *testing.T) {
			if err := (WatchConfig{Namespace: namespace}).Validate(); err != nil {
				t.Errorf("the watch was rejected, want it accepted: %v", err)
			}
		})
	}
}

func TestWatchConfigRejectsNamespace(t *testing.T) {
	cases := map[string]string{
		"uppercased":    "Team-A",
		"underscored":   "team_a",
		"leading dash":  "-team",
		"trailing dash": "team-",
		"dotted":        "team.a",
		"too long":      strings.Repeat("a", validation.DNS1123LabelMaxLength+1),
	}
	for name, namespace := range cases {
		t.Run(name, func(t *testing.T) {
			if err := (WatchConfig{Namespace: namespace}).Validate(); err == nil {
				t.Error("the watch was accepted, want it rejected")
			}
		})
	}
}

func TestWatchConfigAcceptsLabelSelector(t *testing.T) {
	cases := map[string]string{
		"none":         "",
		"equality":     "app=web",
		"inequality":   "app!=web",
		"double equal": "app==web",
		"set":          "app in (web, api)",
		"exists":       "app",
		"absent":       "!app",
		"two terms":    "app=web,tier=front",
	}
	for name, selector := range cases {
		t.Run(name, func(t *testing.T) {
			if err := (WatchConfig{LabelSelector: selector}).Validate(); err != nil {
				t.Errorf("the watch was rejected, want it accepted: %v", err)
			}
		})
	}
}

func TestWatchConfigRejectsLabelSelector(t *testing.T) {
	cases := map[string]string{
		"no key":            "=web",
		"set unenclosed":    "app in web",
		"set unclosed":      "app in (web",
		"operator alone":    "!",
		"uppercased prefix": "App/=web",
	}
	for name, selector := range cases {
		t.Run(name, func(t *testing.T) {
			if err := (WatchConfig{LabelSelector: selector}).Validate(); err == nil {
				t.Error("the watch was accepted, want it rejected")
			}
		})
	}
}

// TestWatchConfigAcceptsSelectableFields walks the fields the APIServer
// accepts, so one added to the list is proven to parse as a selector.
func TestWatchConfigAcceptsSelectableFields(t *testing.T) {
	for _, field := range selectableFields {
		t.Run(field, func(t *testing.T) {
			for _, selector := range []string{field + "=x", field + "==x", field + "!=x"} {
				if err := (WatchConfig{FieldSelector: selector}).Validate(); err != nil {
					t.Errorf("the watch was rejected for %q, want it accepted: %v", selector, err)
				}
			}
		})
	}
}

func TestWatchConfigAcceptsFieldSelector(t *testing.T) {
	cases := map[string]string{
		"none":        "",
		"two terms":   "type=Warning,reason=Failed",
		"negated":     "type!=Normal",
		"empty value": "regarding.fieldPath=",
	}
	for name, selector := range cases {
		t.Run(name, func(t *testing.T) {
			if err := (WatchConfig{FieldSelector: selector}).Validate(); err != nil {
				t.Errorf("the watch was rejected, want it accepted: %v", err)
			}
		})
	}
}

func TestWatchConfigRejectsFieldSelector(t *testing.T) {
	cases := map[string]string{
		"no operator": "type",
		"spaced":      "type Warning",
	}
	for name, selector := range cases {
		t.Run(name, func(t *testing.T) {
			if err := (WatchConfig{FieldSelector: selector}).Validate(); err == nil {
				t.Error("the watch was accepted, want it rejected")
			}
		})
	}
}

// TestWatchConfigRejectsUnselectableFields asserts that a field selector is
// checked against the selectable set rather than only parsed, so a field the
// APIServer does not serve is caught at load rather than at runtime.
func TestWatchConfigRejectsUnselectableFields(t *testing.T) {
	cases := map[string]string{
		"empty field":          "=Warning",
		"involved object kind": "involvedObject.kind=Pod",
		"involved object name": "involvedObject.name=web",
		"source":               "source=kubelet",
		"source component":     "source.component=kubelet",
		"action":               "action=Binding",
		"note":                 "note=OOMKilled",
		"reporting instance":   "reportingInstance=node-1",
		"series":               "series.count=3",
		"uppercased":           "Type=Warning",
		"multiple fields":      "type=Warning,note=OOMKilled",
	}
	for name, selector := range cases {
		t.Run(name, func(t *testing.T) {
			if err := (WatchConfig{FieldSelector: selector}).Validate(); err == nil {
				t.Error("the watch was accepted, want it rejected")
			}
		})
	}
}

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.Watches = []WatchConfig{{Namespace: "team-a"}}

	return cfg
}
