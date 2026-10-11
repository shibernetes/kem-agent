package validate

import (
	"errors"
	"io"
	"path/filepath"
	"testing"
)

func TestRun(t *testing.T) {
	cases := map[string]struct {
		fixture string
		want    error
	}{
		"valid":            {fixture: "valid"},
		"warning only":     {fixture: "enrichment-warning"},
		"rejected setting": {fixture: "bad-field-selector", want: ErrInvalidConfig},
		"rejected filter":  {fixture: "bad-filter", want: ErrInvalidConfig},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("testdata", tc.fixture+".yaml")

			err := run(io.Discard, path, outputText, colorOff)
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
}
