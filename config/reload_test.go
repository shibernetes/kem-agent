package config

import "testing"

func TestClassifyDelta(t *testing.T) {
	cases := map[string]struct {
		change func(*Config)
		want   ReloadDelta
	}{
		"filter added": {
			change: func(c *Config) {
				p := c.Pipelines["all"]
				p.Filters = []string{"event.type == 'Warning'"}
				c.Pipelines["all"] = p
			},
			want: DeltaFilters,
		},
		"service field modified": {
			change: func(c *Config) {
				c.Service.ClusterName = "prod-eu-1"
			},
			want: DeltaStructural,
		},
		"sink field modified": {
			change: func(c *Config) {
				c.Sinks["out"].Config.(*fakeConfig).Endpoint = "other:4317"
			},
			want: DeltaStructural,
		},
		"pipeline removed": {
			change: func(c *Config) {
				delete(c.Pipelines, "all")
			},
			want: DeltaStructural,
		},
		"nothing changed": {want: DeltaNone},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			current, next := parseFixture(t, "minimal").Config, parseFixture(t, "minimal").Config
			if tc.change != nil {
				tc.change(next)
			}
			if got, _ := ClassifyDelta(current, next); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestClassifyDeltaIgnoresFormatting(t *testing.T) {
	var (
		current = parseFixture(t, "minimal").Config
		next    = parseFixture(t, "minimal-reformatted").Config
	)
	if got, _ := ClassifyDelta(current, next); got != DeltaNone {
		t.Errorf("got %s, want none", got)
	}
}
