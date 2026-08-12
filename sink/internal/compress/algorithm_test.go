package compress

import (
	"testing"
)

func TestAlgorithmValidate(t *testing.T) {
	cases := map[string]struct {
		alg   Algorithm
		valid bool
	}{
		"none":        {None, true},
		"gzip":        {Gzip, true},
		"zstd":        {Zstd, true},
		"unset":       {"", false},
		"misspelled":  {"gzipp", false},
		"uppercase":   {"GZIP", false},
		"unsupported": {"deflate", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.alg.Validate()
			switch {
			case tc.valid && err != nil:
				t.Errorf("got %v, want the algorithm accepted", err)
			case !tc.valid && err == nil:
				t.Error("got no error, want the algorithm rejected")
			}
		})
	}
}

func TestAlgorithmString(t *testing.T) {
	cases := map[Algorithm]string{
		None: "none",
		Gzip: "gzip",
		Zstd: "zstd",
	}
	for algorithm, want := range cases {
		if got := algorithm.String(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestAlgorithm_JSONSchema(t *testing.T) {
	schema := Algorithm("").JSONSchema()

	if schema.Type != "string" {
		t.Errorf("got type %q, want %q", schema.Type, "string")
	}
	if len(schema.Enum) != len(algorithms) {
		t.Fatalf("got %d algorithms, want %d", len(schema.Enum), len(algorithms))
	}
	for _, v := range schema.Enum {
		name, ok := v.(string)
		if !ok {
			t.Fatalf("got type %T, want a string", v)
		}
		if err := Algorithm(name).Validate(); err != nil {
			t.Errorf("the schema offers %q, which validation rejects: %v", name, err)
		}
	}
}
