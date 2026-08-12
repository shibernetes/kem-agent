package metrics

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/shibernetes/kem-agent/sink"
)

// TestCreateDefaultConfigIsFresh asserts that each configured sink
// is decoded onto a config of its own, rather than sharing one.
func TestCreateDefaultConfigIsFresh(t *testing.T) {
	f := NewFactory()
	first, second := f.CreateDefaultConfig(), f.CreateDefaultConfig()

	if first == second {
		t.Fatal("two calls returned one config, want a fresh one each time")
	}
	if diff := cmp.Diff(config(t, first), config(t, second)); diff != "" {
		t.Errorf("the two configs differ (-first +second):\n%s", diff)
	}
}

func TestCreate(t *testing.T) {
	s := createSink(t, testConfig(nil))

	if got := s.Name(); got != "warn-count" {
		t.Errorf("got name %q, want %q", got, "warn-count")
	}
	if _, ok := s.(sink.EventSink); !ok {
		t.Errorf("got type %T, want an EventSink", s)
	}
	if _, ok := s.(sink.Instrumented); !ok {
		t.Errorf("got type %T, want a type implementing the Instrumented interface", s)
	}
}

// TestCreateConsumesEventsDirectly asserts that the sink records events
// itself rather than through a drainer.
func TestCreateConsumesEventsDirectly(t *testing.T) {
	s := createSink(t, testConfig(nil))

	if _, ok := s.(sink.BatchSink); ok {
		t.Errorf("got type %T, want it to not implement BatchSink", s)
	}
	if _, ok := s.(sink.Opener); ok {
		t.Errorf("got type %T, want it to not implement Opener", s)
	}
}

// TestCreateRejectsForeignConfig asserts that a miswired registry
// is a startup error rather than a panic.
func TestCreateRejectsForeignConfig(t *testing.T) {
	if _, err := NewFactory().Create(sink.Options{Name: "warn-count"}, struct{}{}); err == nil {
		t.Error("another type's config was accepted, want it rejected")
	}
}

// TestCreateWiresLogger asserts that the factory passes a logger.
// Nothing guards against a nil one, so a sink built without it panics
// at first use rather than at startup.
func TestCreateWiresLogger(t *testing.T) {
	var (
		rec = &logRecorder{}
		cfg = testConfig(func(c *Config) { c.MaxSeries = 1 })
	)
	s, err := NewFactory().Create(sink.Options{Name: "warn-count", Logger: rec.logger()}, &cfg)
	if err != nil {
		t.Fatalf("failed to create sink: %v", err)
	}
	events, ok := s.(sink.EventSink)
	if !ok {
		t.Fatalf("got type %T, want an EventSink", s)
	}
	ev := testEvent()
	_ = events.Handle(ev)

	// A second combination of label values is one past the limit.
	ev.Namespace = "team-b"
	_ = events.Handle(ev)

	if got := rec.messages(); len(got) != 1 {
		t.Errorf("got %d log lines, want the refusal reported once: %v", len(got), got)
	}
}

// TestCreateMatchesValidate asserts that Create does not fail on a
// configuration that validates successfully.
func TestCreateMatchesValidate(t *testing.T) {
	cases := map[string]func(*Config){
		"defaults": func(*Config) {},
		"series limit": func(c *Config) {
			c.MaxSeries = 1000
		},
		"no labels": func(c *Config) {
			c.Metrics[0].Labels = nil
		},
		"constant labels": func(c *Config) {
			c.Metrics[0].ConstLabels = map[string]string{"source": "kem-agent"}
		},
		"bare namespace": func(c *Config) {
			c.DefaultMetricsNamespace = ""
		},
		"several metrics": func(c *Config) {
			c.Metrics = append(c.Metrics, Metric{Name: "events", Help: "Events by namespace."})
		},
		"name refused by legacy scheme": func(c *Config) {
			c.MetricNameValidationScheme = NameValidationUTF8
			c.Metrics[0].Name = "my-warnings"
		},
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig(opt)

			if err := cfg.Validate(); err != nil {
				t.Fatalf("configuration was rejected: %v", err)
			}
			if _, err := NewFactory().Create(sink.Options{Name: "warn-count"}, &cfg); err != nil {
				t.Errorf("failed to build valid configuration: %v", err)
			}
		})
	}
}

// createSink builds a sink through its factory.
func createSink(t *testing.T, cfg Config) sink.Sink {
	t.Helper()

	s, err := NewFactory().Create(sink.Options{Name: "warn-count"}, &cfg)
	if err != nil {
		t.Fatalf("failed to create sink: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Shutdown(context.Background())
	})
	return s
}

// config returns the sink's own configuration type.
func config(t *testing.T, cfg sink.Config) *Config {
	t.Helper()

	c, ok := cfg.(*Config)
	if !ok {
		t.Fatalf("got config of type %T, want %T", cfg, (*Config)(nil))
	}
	return c
}
