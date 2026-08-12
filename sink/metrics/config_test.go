package metrics

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/shibernetes/kem-agent/internal/version"
	"github.com/shibernetes/kem-agent/sink"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if got := cfg.DefaultMetricsNamespace; got != defaultMetricsNamespace {
		t.Errorf("got namespace %q, want %q", got, defaultMetricsNamespace)
	}
	if got := cfg.MetricNameValidationScheme; got != NameValidationLegacy {
		t.Errorf("got validation scheme %q, want %q", got, NameValidationLegacy)
	}
}

// TestDefaultConfigIsRejected asserts that a sink config with no metric
// is a load error, since it would record nothing and produce no series.
func TestDefaultConfigIsRejected(t *testing.T) {
	if err := DefaultConfig().Validate(); err == nil {
		t.Error("the default configuration was accepted, want a metric required")
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Config){
		"no metric":                 func(c *Config) { c.Metrics = nil },
		"negative series limit":     func(c *Config) { c.MaxSeries = -1 },
		"unknown validation scheme": func(c *Config) { c.MetricNameValidationScheme = "utf-8" },
		"nameless metric":           func(c *Config) { c.Metrics[0].Name = "" },
		"metric with no help": func(c *Config) {
			c.Metrics[0].Help = ""
		},
		"duplicate metric name": func(c *Config) {
			c.Metrics = append(c.Metrics, testMetric())
		},
		"invalid metric name": func(c *Config) {
			c.Metrics[0].Name = "my-warnings"
		},
		"invalid metric namespace": func(c *Config) {
			c.Metrics[0].Namespace = "my-ns"
		},
		"colon in metric name": func(c *Config) {
			c.Metrics[0].Name = "warnings:rate"
		},
		"colon in metric namespace": func(c *Config) {
			c.Metrics[0].Namespace = "kem:events"
		},
		"reserved agent namespace": func(c *Config) {
			c.Metrics[0].Namespace = version.MetricNamespace
		},
		"Go collector namespace": func(c *Config) {
			c.Metrics[0].Namespace = "go"
		},
		"process collector namespace": func(c *Config) {
			c.Metrics[0].Namespace = "process"
		},
		"reserved label": func(c *Config) {
			c.Metrics[0].Labels = map[string]string{"__ns": "namespace"}
		},
		"invalid labels": func(c *Config) {
			c.Metrics[0].Labels = map[string]string{"my-ns": "namespace"}
		},
		"unknown event field": func(c *Config) {
			c.Metrics[0].Labels = map[string]string{"ns": "nowhere"}
		},
		"label defined twice": func(c *Config) {
			c.Metrics[0].ConstLabels = map[string]string{"ns": "team-a"}
		},
		"reserved constant label": func(c *Config) {
			c.Metrics[0].ConstLabels = map[string]string{"__source": "kem-agent"}
		},
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			if err := testConfig(opt).Validate(); err == nil {
				t.Error("the configuration was accepted, want it rejected")
			}
		})
	}
}

func TestValidateAccepts(t *testing.T) {
	cases := map[string]func(*Config){
		"base configuration": func(*Config) {},
		"unlimited series": func(c *Config) {
			c.MaxSeries = 0
		},
		"bare namespace": func(c *Config) {
			c.DefaultMetricsNamespace = ""
		},
		"subsystem": func(c *Config) {
			c.Metrics[0].Subsystem = "pods"
		},
		"no label": func(c *Config) {
			c.Metrics[0].Labels = nil
		},
		"constant labels": func(c *Config) {
			c.Metrics[0].ConstLabels = map[string]string{"source": "kem-agent"}
		},
		"two metrics": func(c *Config) {
			c.Metrics = append(c.Metrics, Metric{Name: "events", Help: "Events by namespace."})
		},
		"utf-8 metric name": func(c *Config) {
			c.MetricNameValidationScheme = NameValidationUTF8
			c.Metrics[0].Name = "my-warnings"
		},
		"utf-8 label": func(c *Config) {
			c.MetricNameValidationScheme = NameValidationUTF8
			c.Metrics[0].Labels = map[string]string{"my-ns": "namespace"}
		},
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			if err := testConfig(opt).Validate(); err != nil {
				t.Errorf("the configuration was rejected: %v", err)
			}
		})
	}
}

// TestValidateAcceptsEventFields asserts that a metric label can map any
// field of an event, including a label or annotation key.
func TestValidateAcceptsEventFields(t *testing.T) {
	cases := map[string]string{
		"plain field":    "reason",
		"nested field":   "regarding.kind",
		"series field":   "series.count",
		"label key":      "labels.app.kubernetes.io/name",
		"annotation key": "annotations.kubernetes.io/change-cause",
		"enriched label": "regardingObject.labels.team",
		"enriched owner": "regardingObject.owner.kind",
	}
	for name, field := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig(func(c *Config) {
				c.Metrics[0].Labels = map[string]string{"value": field}
			})
			if err := cfg.Validate(); err != nil {
				t.Errorf("the field was rejected: %v", err)
			}
		})
	}
}

// TestValidateRejectsEscapedReservedName asserts that a metric name
// is refused when escaping moves it under a reserved prefix.
func TestValidateRejectsEscapedReservedName(t *testing.T) {
	cases := map[string]string{
		"dash":              "kem-agent_events_read_total",
		"dot":               "kem.agent_events_read_total",
		"space":             "kem agent_events_read_total",
		"Go collector":      "go-goroutines",
		"process collector": "process-cpu",
	}
	for name, metric := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig(func(c *Config) {
				c.MetricNameValidationScheme = NameValidationUTF8
				c.DefaultMetricsNamespace = ""
				c.Metrics[0].Name = metric
			})
			if err := cfg.Validate(); err == nil {
				t.Error("the name was accepted, want it rejected")
			}
		})
	}
}

// TestValidateRejectsEscapedNameCollision asserts that two escaped metric names
// that resolve to the same value are refused. A scraper that does not accept
// UTF-8 names is served the same metric family twice, and rejects the whole scrape.
func TestValidateRejectsEscapedNameCollision(t *testing.T) {
	cfg := testConfig(func(c *Config) {
		c.MetricNameValidationScheme = NameValidationUTF8
		c.Metrics[0].Name = "my-warnings"
		c.Metrics = append(c.Metrics, Metric{
			Name: "my_warnings",
			Help: "Events by namespace.",
		})
	})
	if err := cfg.Validate(); err == nil {
		t.Error("the names were accepted, want them rejected")
	}
}

// TestValidateRejectsEscapedLabelCollision asserts that two escaped label names
// that resolve to the same value are refused., in either map. A series carrying
// the same label name twice is rejected by a scraper.
func TestValidateRejectsEscapedLabelCollision(t *testing.T) {
	cases := map[string]func(*Config){
		"two labels": func(c *Config) {
			c.Metrics[0].Labels = map[string]string{
				"a-b": "reason",
				"a_b": "namespace",
			}
		},
		"label and constant label": func(c *Config) {
			c.Metrics[0].Labels = map[string]string{"a-b": "reason"}
			c.Metrics[0].ConstLabels = map[string]string{"a_b": "team-a"}
		},
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig(func(c *Config) {
				c.MetricNameValidationScheme = NameValidationUTF8
				opt(c)
			})
			if err := cfg.Validate(); err == nil {
				t.Error("the labels were accepted, want them rejected")
			}
		})
	}
}

func TestValidateRejectsEscapedReservedLabel(t *testing.T) {
	cfg := testConfig(func(c *Config) {
		c.MetricNameValidationScheme = NameValidationUTF8
		c.Metrics[0].Labels = map[string]string{"-_reserved": "namespace"}
	})
	if err := cfg.Validate(); err == nil {
		t.Error("the label was accepted, want it rejected")
	}
}

// TestValidateReportsSameInvalidLabel asserts that a configuration
// defining several invalid labels always reports the first by name,
// since a map is iterated in an unspecified order.
func TestValidateReportsSameInvalidLabel(t *testing.T) {
	cfg := testConfig(func(c *Config) {
		c.Metrics[0].Labels = map[string]string{
			"zone": "nowhere",
			"app":  "nowhere",
			"team": "nowhere",
		}
	})
	for range 20 {
		err := cfg.Validate()
		if err == nil {
			t.Fatal("the configuration was accepted, want it rejected")
		}
		if !strings.Contains(err.Error(), `"app"`) {
			t.Fatalf("the error does not name the first label: %v", err)
		}
	}
}

// TestBareNamespaceAddsNoPrefix asserts that a sink configuration with an
// empty namespace registers a metric by its name, rather than with a
// leading separator.
func TestBareNamespaceAddsNoPrefix(t *testing.T) {
	s := newTestSink(t, testConfig(func(c *Config) {
		c.DefaultMetricsNamespace = ""
		c.Metrics[0].Labels = nil
	}))
	_ = s.Handle(testEvent())

	want := []string{"warnings 1"}
	if diff := cmp.Diff(want, scrape(t, s)); diff != "" {
		t.Errorf("the series differ (-want +got):\n%s", diff)
	}
}

func TestConfigFieldsReportsLabelPaths(t *testing.T) {
	cfg := Config{Metrics: []Metric{
		{Labels: map[string]string{"ns": "namespace", "team": "regardingObject.labels.team"}},
		{Labels: map[string]string{"ns": "namespace"}},
	}}
	want := []sink.FieldRef{
		{Field: "namespace", Path: "metrics[0].labels.ns"},
		{Field: "regardingObject.labels.team", Path: "metrics[0].labels.team"},
		{Field: "namespace", Path: "metrics[1].labels.ns"},
	}
	if got := cfg.Fields(); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestConfigFieldsIsEmptyWithoutLabels(t *testing.T) {
	cfg := Config{Metrics: []Metric{{Name: "warnings"}}}

	if got := cfg.Fields(); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}
