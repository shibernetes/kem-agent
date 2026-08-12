package pipeline

import (
	"testing"
)

func TestConfigAccepts(t *testing.T) {
	cases := map[string]Config{
		"one sink":         {Sinks: []string{"otel"}},
		"several sinks":    {Sinks: []string{"otel", "slack"}},
		"bound watches":    {Sinks: []string{"otel"}, Watches: []string{"team-a", "team-b"}},
		"filters":          {Sinks: []string{"otel"}, Filters: []string{"event.type == 'Warning'"}},
		"repeated filters": {Sinks: []string{"otel"}, Filters: []string{"a", "a"}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err != nil {
				t.Errorf("the configuration was rejected, want it accepted: %v", err)
			}
		})
	}
}

func TestConfigRejects(t *testing.T) {
	cases := map[string]Config{
		"no sinks":        {},
		"empty sink list": {Sinks: []string{}},
		"duplicate sink":  {Sinks: []string{"otel", "otel"}},
		"duplicate watch": {Sinks: []string{"otel"}, Watches: []string{"team-a", "team-a"}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Error("the configuration was accepted, want it rejected")
			}
		})
	}
}

// TestConfigDistinguishesEmptyWatchList asserts the difference between
// a nil watch list and an empty one. The former takes every watch, while
// the latter narrows the pipeline down to nothing.
func TestConfigDistinguishesEmptyWatchList(t *testing.T) {
	omitted := Config{Sinks: []string{"otel"}}
	if err := omitted.Validate(); err != nil {
		t.Errorf("a nil watch list was rejected, want it accepted: %v", err)
	}
	empty := Config{Sinks: []string{"otel"}, Watches: []string{}}
	if err := empty.Validate(); err == nil {
		t.Error("an empty watch list was accepted, want it rejected")
	}
}
