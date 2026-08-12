package units

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
)

type durationDoc struct {
	Timeout Duration `yaml:"timeout"`
}

var validDurationCases = map[string]struct {
	text string
	want time.Duration
}{
	"zero":         {text: "0", want: 0},
	"milliseconds": {text: "250ms", want: 250 * time.Millisecond},
	"seconds":      {text: "30s", want: 30 * time.Second},
	"compound":     {text: "1m30s", want: 90 * time.Second},
	"fractional":   {text: "1.5h", want: 90 * time.Minute},
	"leading dot":  {text: ".5s", want: 500 * time.Millisecond},
	"trailing dot": {text: "1.h", want: time.Hour},
	"microseconds": {text: "5us", want: 5 * time.Microsecond},
	"negative":     {text: "-5s", want: -5 * time.Second},
	"quoted":       {text: `"30s"`, want: 30 * time.Second},
	"greek mu":     {text: "5μs", want: 5 * time.Microsecond},
}

func TestDurationDecode(t *testing.T) {
	for name, tc := range validDurationCases {
		t.Run(name, func(t *testing.T) {
			got, err := decodeDuration(tc.text)
			if err != nil {
				t.Fatalf("failed to decode %q: %v", tc.text, err)
			}
			if time.Duration(got) != tc.want {
				t.Errorf("got %v, want %v", time.Duration(got), tc.want)
			}
		})
	}
}

func TestDurationDecodeErrors(t *testing.T) {
	cases := map[string]struct {
		text string
		want string
	}{
		"unknown unit":     {text: "30x", want: `invalid duration "30x": unknown unit "x" in duration "30x"`},
		"no unit":          {text: "30", want: `invalid duration "30": missing unit in duration "30"`},
		"invalid duration": {text: "soon", want: `invalid duration "soon"`},
		"empty value":      {text: `""`, want: "expected a Go time.Duration string"},
		"mapping":          {text: "{a: 1}", want: "expected a Go time.Duration string"},
		"sequence":         {text: "[1, 2]", want: "expected a Go time.Duration string"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := decodeDuration(tc.text)
			if err == nil {
				t.Fatalf("decoding %q succeeded, want an error", tc.text)
			}
			if got := causeOf(t, err); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDurationPatternMatch(t *testing.T) {
	re := regexp.MustCompile(durationPattern)

	for _, s := range []string{
		"+0", "-0", "1m0.5s", "5µs",
	} {
		if _, err := decodeDuration(s); err != nil {
			t.Fatalf("failed to decode %q: %v", s, err)
		}
		if !re.MatchString(s) {
			t.Errorf("pattern rejected %q", s)
		}
	}
	for _, tc := range validDurationCases {
		if s := strings.Trim(tc.text, `"`); !re.MatchString(s) {
			t.Errorf("pattern rejected %q", s)
		}
	}
}

func TestDurationString(t *testing.T) {
	if got := Duration(90 * time.Second).String(); got != "1m30s" {
		t.Errorf("got %q, want %q", got, "1m30s")
	}
}

func TestDurationSchema(t *testing.T) {
	schema := reflectSchema(t, durationDoc{})

	p, ok := schema.Properties.Get("timeout")
	if !ok {
		t.Fatalf("got %s, want timeout property", mustEncodeSchema(t, schema))
	}
	if p.Type != "string" {
		t.Errorf("got type %q, want %q", p.Type, "string")
	}
	if p.Pattern != durationPattern {
		t.Errorf("got pattern %q, want %q", p.Pattern, durationPattern)
	}
}

func decodeDuration(s string) (Duration, error) {
	var doc durationDoc

	err := yaml.Unmarshal([]byte("timeout: "+s+"\n"), &doc)
	return doc.Timeout, err
}
