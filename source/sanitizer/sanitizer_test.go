package sanitizer

import (
	"maps"
	"strings"
	"testing"
	"unicode/utf8"
	"unsafe"
)

func TestTruncate(t *testing.T) {
	cases := map[string]struct {
		in   string
		n    int
		want string
	}{
		"under the limit":      {in: "pulling", n: 16, want: "pulling"},
		"exactly at the limit": {in: "pulling", n: 7, want: "pulling"},
		"over the limit":       {in: "pulling image", n: 7, want: "pulling"},
		"an empty string":      {in: "", n: 7, want: ""},
		"a limit of zero":      {in: "pulling", n: 0, want: ""},
		"a two-byte rune":      {in: strings.Repeat("é", 8), n: 5, want: strings.Repeat("é", 2)},
		"a three-byte rune":    {in: strings.Repeat("€", 8), n: 5, want: "€"},
		"a four-byte rune":     {in: strings.Repeat("𝄞", 8), n: 5, want: "𝄞"},
	}
	for name, tc := range cases {
		got := truncate(tc.in, tc.n)

		if got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("%s: the shortened value is not valid UTF-8: %q", name, got)
		}
		// Only a value that was cut is copied, and it must not
		// point into the original, since a slice of it would
		// pin the backing array.
		if got != tc.in && unsafe.StringData(got) == unsafe.StringData(tc.in) {
			t.Errorf("%s: the shortened value points into the original", name)
		}
	}
}

func TestTruncateValues(t *testing.T) {
	cases := map[string]struct {
		m    map[string]string
		want map[string]string
		n    int
	}{
		"a value over the limit": {
			m:    map[string]string{"a": "foobar", "b": "baz"},
			want: map[string]string{"a": "foo", "b": "baz"},
			n:    3,
		},
		"every value under the limit": {
			m:    map[string]string{"a": "foo", "b": "bar"},
			want: map[string]string{"a": "foo", "b": "bar"},
			n:    3,
		},
		"a limit of zero": {
			m:    map[string]string{"a": "foobar"},
			want: map[string]string{"a": "foobar"},
			n:    0,
		},
		"a negative limit": {
			m:    map[string]string{"a": "foobar"},
			want: map[string]string{"a": "foobar"},
			n:    -1,
		},
		"no values at all": {
			m:    nil,
			want: nil,
			n:    3,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			truncateValues(tc.m, tc.n)

			if !maps.Equal(tc.m, tc.want) {
				t.Errorf("got %v, want %v", tc.m, tc.want)
			}
		})
	}
}

func TestCapMapEntries(t *testing.T) {
	cases := map[string]struct {
		m    map[string]string
		want map[string]string
		n    int
	}{
		"under the limit": {
			m:    map[string]string{"a": "foo", "b": "bar"},
			want: map[string]string{"a": "foo", "b": "bar"},
			n:    3,
		},
		"exactly at the limit": {
			m:    map[string]string{"a": "foo", "b": "bar", "c": "baz"},
			want: map[string]string{"a": "foo", "b": "bar", "c": "baz"},
			n:    3,
		},
		"over the limit": {
			m:    map[string]string{"d": "four", "b": "two", "e": "five", "a": "one", "c": "three"},
			want: map[string]string{"a": "one", "b": "two"},
			n:    2,
		},
		"zero limit": {
			m:    map[string]string{"a": "foo", "b": "bar"},
			want: map[string]string{"a": "foo", "b": "bar"},
			n:    0,
		},
		"negative limit": {
			m:    map[string]string{"a": "foo", "b": "bar"},
			want: map[string]string{"a": "foo", "b": "bar"},
			n:    -1,
		},
		"no entries at all": {
			m:    nil,
			want: nil,
			n:    3,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var (
				original = maps.Clone(tc.m)
				capped   = capMapEntries(tc.m, tc.n)
			)
			if !maps.Equal(capped, tc.want) {
				t.Errorf("got %v, want %v", capped, tc.want)
			}
			if !maps.Equal(tc.m, original) {
				t.Errorf("the original map changed to %v, want %v", tc.m, original)
			}
		})
	}
}
