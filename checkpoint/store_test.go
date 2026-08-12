package checkpoint

import (
	"encoding/json"
	"maps"
	"testing"
)

func TestStateEncodes(t *testing.T) {
	cases := map[string]struct {
		state State
		want  string
	}{
		"existing positions": {
			state: State{Watches: map[string]string{"": "100", "team-a": "42"}},
			want:  `{"watches":{"":"100","team-a":"42"}}`,
		},
		"empty positions": {
			state: State{},
			want:  `{"watches":null}`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			b, err := json.Marshal(tc.state)
			if err != nil {
				t.Fatalf("failed to encode state: %v", err)
			}
			if got := string(b); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestStateDecodes(t *testing.T) {
	const encoded = `{"watches":{"":"100","team-a":"42"}}`

	var (
		state = decode(t, encoded)
		want  = map[string]string{"": "100", "team-a": "42"}
	)
	if !maps.Equal(state.Watches, want) {
		t.Errorf("got watches %v, want %v", state.Watches, want)
	}
}

func decode(t *testing.T, s string) State {
	t.Helper()

	var state State
	if err := json.Unmarshal([]byte(s), &state); err != nil {
		t.Fatalf("failed to decode state: %v", err)
	}
	return state
}
