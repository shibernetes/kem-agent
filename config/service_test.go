package config

import (
	"testing"
)

func TestSharePort(t *testing.T) {
	cases := map[string]struct {
		a, b string
		want bool
	}{
		"same port on every interface": {a: ":8080", b: ":8080", want: true},
		"every interface and one host": {a: "0.0.0.0:8080", b: "127.0.0.1:8080", want: true},
		"same port on two hosts":       {a: "127.0.0.1:8080", b: "10.0.0.1:8080", want: false},
		"two ports":                    {a: ":8080", b: ":8081", want: false},
		"free port picked twice":       {a: ":0", b: ":0", want: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			switch got := sharePort(tc.a, tc.b); {
			case got && !tc.want:
				t.Errorf("%q and %q were reported on the same port, want them apart", tc.a, tc.b)
			case !got && tc.want:
				t.Errorf("%q and %q were reported apart, want them on the same port", tc.a, tc.b)
			}
		})
	}
}
