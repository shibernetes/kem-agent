package units

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

type bytesDoc struct {
	MaxBytes Bytes `yaml:"max_bytes"`
}

var validBytesCases = map[string]struct {
	text string
	want Bytes
}{
	"zero":                      {text: "0", want: 0},
	"plain count":               {text: "4096", want: 4096},
	"binary unit":               {text: "32Mi", want: 33554432},
	"decimal unit":              {text: "64M", want: 64000000},
	"gibibytes":                 {text: "1Gi", want: 1073741824},
	"lowercase kilo":            {text: "512k", want: 512000},
	"fractional mantissa":       {text: "1.5Gi", want: 1610612736},
	"fraction to whole bytes":   {text: "1.25Ki", want: 1280},
	"largest binary unit":       {text: "7Ei", want: 8070450532247928832},
	"explicit plus sign":        {text: "+5Mi", want: 5242880},
	"trailing zero in fraction": {text: "5.0Mi", want: 5242880},
	"quoted value":              {text: `"32Mi"`, want: 33554432},
}

func TestBytesDecode(t *testing.T) {
	for name, tc := range validBytesCases {
		t.Run(name, func(t *testing.T) {
			got, err := decodeBytes(tc.text)
			if err != nil {
				t.Fatalf("failed to decode %q: %v", tc.text, err)
			}
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestBytesDecodeErrors(t *testing.T) {
	cases := map[string]struct {
		text string
		want string
	}{
		"binary B form": {
			text: "32MiB",
			want: `invalid quantity "32MiB": quantities take no B unit, write 32Mi`,
		},
		"decimal B form names both units": {
			text: "1MB",
			want: `invalid quantity "1MB": quantities take no B unit, write 1M for SI or 1Mi for IEC`,
		},
		"bare B": {
			text: "4096B",
			want: `invalid quantity "4096B": quantities take no B unit, write 4096`,
		},
		"uppercase K": {
			text: "64K",
			want: `invalid quantity "64K": units are case sensitive, write 64k for SI or 64Ki for IEC`,
		},
		"sub-unit": {
			text: "64m",
			want: `invalid quantity "64m": "m" is a sub-unit and reads as less than one byte`,
		},
		"exponent": {
			text: "1e6",
			want: `invalid quantity "1e6": exponents are not accepted`,
		},
		"unknown unit": {
			text: "5tb",
			want: `invalid quantity "5tb": unknown unit "tb", accepted units are k, M, G, T, P, E, Ki, Mi, Gi, Ti, Pi and Ei`,
		},
		"fractional byte count": {
			text: "1.5",
			want: `invalid quantity "1.5": not a whole number`,
		},
		"negative value": {
			text: "-1",
			want: `invalid quantity "-1": a quantity cannot be negative`,
		},
		"past the int64 limit": {
			text: "8Ei",
			want: `invalid quantity "8Ei": a quantity must be less than 8Ei`,
		},
		"far past the int64 limit": {
			text: "999999E",
			want: `invalid quantity "999999E": a quantity must be less than 8Ei`,
		},
		"exact int64 limit": {
			text: "9223372036854775807",
			want: `invalid quantity "9223372036854775807": a quantity must be less than 8Ei`,
		},
		"unit with no number": {
			text: "Mi",
			want: `invalid quantity "Mi"`,
		},
		"missing leading digit": {
			text: ".5Ki",
			want: `invalid quantity ".5Ki"`,
		},
		"trailing dot": {
			text: "1.",
			want: `invalid quantity "1."`,
		},
		"two dots": {
			text: "1.2.3",
			want: `invalid quantity "1.2.3"`,
		},
		"lone sign": {
			text: `"-"`,
			want: `invalid quantity "-"`,
		},
		"empty value": {
			text: `""`,
			want: "expected a quantity string",
		},
		"mapping": {
			text: "{a: 1}",
			want: "expected a quantity string",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := decodeBytes(tc.text)
			if err == nil {
				t.Fatalf("decoding %q succeeded, want an error", tc.text)
			}
			if got := causeOf(t, err); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestQuantityPatternMatch(t *testing.T) {
	re := regexp.MustCompile(quantityPattern)

	for _, unit := range slices.Concat([]string{""}, unitsSI, unitsIEC) {
		s := "1" + unit
		if _, err := decodeBytes(s); err != nil {
			t.Fatalf("failed to decode %q: %v", s, err)
		}
		if !re.MatchString(s) {
			t.Errorf("pattern rejected %q", s)
		}
	}
	for _, tc := range validBytesCases {
		if s := strings.Trim(tc.text, `"`); !re.MatchString(s) {
			t.Errorf("pattern rejected %q", s)
		}
	}
}

func TestBytesString(t *testing.T) {
	cases := map[string]struct {
		bytes Bytes
		want  string
	}{
		"zero":             {bytes: 0, want: "0"},
		"plain count":      {bytes: 512, want: "512"},
		"binary unit":      {bytes: 33554432, want: "32Mi"},
		"decimal unit":     {bytes: 1000, want: "1k"},
		"nearest unit":     {bytes: 64000000, want: "62500Ki"},
		"multiple of none": {bytes: 33554433, want: "33554433"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.bytes.String(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBytesStringRoundTrip(t *testing.T) {
	for name, tc := range validBytesCases {
		t.Run(name, func(t *testing.T) {
			written := tc.want.String()

			got, err := decodeBytes(written)
			if err != nil {
				t.Fatalf("failed to decode %q: %v", written, err)
			}
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestBytesSchema(t *testing.T) {
	schema := reflectSchema(t, bytesDoc{})

	p, ok := schema.Properties.Get("max_bytes")
	if !ok {
		t.Fatalf("got %s, want max_bytes property", mustEncodeSchema(t, schema))
	}
	if len(p.OneOf) != 2 {
		t.Fatalf("got %s, want two accepted forms", mustEncodeSchema(t, p))
	}
	if got := p.OneOf[0].Type; got != "integer" {
		t.Errorf("got first form %q, want %q", got, "integer")
	}
	if got := p.OneOf[1].Pattern; got != quantityPattern {
		t.Errorf("got second form pattern %q, want %q", got, quantityPattern)
	}
}

func decodeBytes(s string) (Bytes, error) {
	var doc bytesDoc

	err := yaml.Unmarshal([]byte("max_bytes: "+s+"\n"), &doc)
	return doc.MaxBytes, err
}
