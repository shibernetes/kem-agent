package otel

import (
	"testing"
)

func TestCompressionValidate(t *testing.T) {
	cases := map[string]struct {
		compression Compression
		valid       bool
	}{
		"none":       {CompressionNone, true},
		"gzip":       {CompressionGzip, true},
		"unset":      {"", false},
		"misspelled": {"gzp", false},
		"uppercase":  {"GZIP", false},
		"zstd":       {"zstd", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.compression.Validate()
			switch {
			case tc.valid && err != nil:
				t.Errorf("got %v, want the compression accepted", err)
			case !tc.valid && err == nil:
				t.Error("got no error, want the compression rejected")
			}
		})
	}
}

func TestCompressionString(t *testing.T) {
	cases := map[Compression]string{
		CompressionNone: "none",
		CompressionGzip: "gzip",
	}
	for compression, want := range cases {
		if got := compression.String(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestCompression_JSONSchema(t *testing.T) {
	schema := Compression("").JSONSchema()

	if schema.Type != "string" {
		t.Errorf("got type %q, want %q", schema.Type, "string")
	}
	if len(schema.Enum) != len(compressions) {
		t.Fatalf("got %d algorithms, want %d", len(schema.Enum), len(compressions))
	}
	for _, v := range schema.Enum {
		name, ok := v.(string)
		if !ok {
			t.Fatalf("got type %T, want a string", v)
		}
		if err := Compression(name).Validate(); err != nil {
			t.Errorf("the schema offers %q, which validation rejects: %v", name, err)
		}
	}
}
