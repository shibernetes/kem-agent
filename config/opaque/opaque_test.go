package opaque

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/goccy/go-yaml"
)

const (
	secretValue        = "s3cr3t"
	secret      String = secretValue
)

func TestStringRedactsWhenFormatted(t *testing.T) {
	cases := map[string]struct {
		format string
		want   string
	}{
		"%v":  {format: "%v", want: masked},
		"%s":  {format: "%s", want: masked},
		"%q":  {format: "%q", want: `"` + masked + `"`},
		"%x":  {format: "%x", want: fmt.Sprintf("%x", masked)},
		"%+v": {format: "%+v", want: masked},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := fmt.Sprintf(tc.format, secret); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestStringRedactsInsideAStruct(t *testing.T) {
	auth := struct {
		Token String
	}{Token: secret}

	want := "{" + masked + "}"
	if got := fmt.Sprintf("%v", auth); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestStringRedactsWhenMarshalled(t *testing.T) {
	text, err := secret.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText() failed: %v", err)
	}
	if string(text) != masked {
		t.Errorf("got %q, want %q", text, masked)
	}

	encoded, err := json.Marshal(secret)
	if err != nil {
		t.Fatalf("json.Marshal() failed: %v", err)
	}
	if want := `"` + masked + `"`; string(encoded) != want {
		t.Errorf("got %s, want %s", encoded, want)
	}
}

func TestStringDecodes(t *testing.T) {
	var auth struct {
		Token String `yaml:"token"`
	}
	if err := yaml.Unmarshal([]byte("token: "+secretValue+"\n"), &auth); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if string(auth.Token) != secretValue {
		t.Errorf("got %q, want %q", string(auth.Token), secretValue)
	}
}
