package diag

import "testing"

type item struct {
	Name string `yaml:"name"`
	Note string `yaml:"note,omitempty"`
}

type fields struct {
	Endpoint string `yaml:"endpoint"`
	Optional string `yaml:"optional,omitempty"`
	Ignored  string `yaml:"-"`
	Count    int    `yaml:"count,omitzero"`
	Nested   item   `yaml:"nested"`
	Inlined  item   `yaml:",inline"`
	List     []item `yaml:"list,omitempty"`
	Untagged string
}

// filled returns a value with every field set, so each case empties the one
// it is about and nothing is already zero by accident.
func filled() fields {
	return fields{
		Endpoint: "collector:4317",
		Optional: "optional",
		Ignored:  "ignored",
		Count:    1,
		Nested:   item{Name: "nested"},
		Inlined:  item{Name: "inlined"},
		List:     []item{{Name: "listed"}},
		Untagged: "untagged",
	}
}

func TestRequired(t *testing.T) {
	cases := map[string]struct {
		empty func(*fields)
		want  string
	}{
		"nothing missing":     {},
		"scalar":              {empty: func(d *fields) { d.Endpoint = "" }, want: "endpoint is required"},
		"whole nested block":  {empty: func(d *fields) { d.Nested = item{} }, want: "nested is required"},
		"field inside one":    {empty: func(d *fields) { d.Nested = item{Note: "kept"} }, want: "nested.name is required"},
		"inlined field":       {empty: func(d *fields) { d.Inlined.Name = "" }, want: "name is required"},
		"item of a list":      {empty: func(d *fields) { d.List = append(d.List, item{}) }, want: "list[1].name is required"},
		"optional field":      {empty: func(d *fields) { d.Optional = "" }},
		"zeroable field":      {empty: func(d *fields) { d.Count = 0 }},
		"ignored field":       {empty: func(d *fields) { d.Ignored = "" }},
		"untagged field":      {empty: func(d *fields) { d.Untagged = "" }},
		"two of them at once": {empty: func(d *fields) { d.Endpoint, d.Nested = "", item{} }, want: "endpoint, nested are required"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d := filled()
			if tc.empty != nil {
				tc.empty(&d)
			}
			err := Required(d)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("got %v, want no missing field", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("got no error, want %q", tc.want)
			}
			if got := err.Error(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRequiredSkipsNil(t *testing.T) {
	var v struct {
		Item *item `yaml:"item,omitempty"`
	}
	if err := Required(v); err != nil {
		t.Errorf("got %v, want no missing field", err)
	}
}
