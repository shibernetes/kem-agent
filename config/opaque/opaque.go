package opaque

const (
	masked = "**REDACTED**"
)

// String holds a sensitive value read from the configuration.
// It is redacted wherever its printed or encoded, so reaching the real value
// requires an explicit conversion to string.
type String string

// String implements the [fmt.Stringer] interface, returning a redacted value.
func (s String) String() string {
	return masked
}

// MarshalText implements the [encoding.TextMarshaler] interface
// It returns a redacted value, and keeps the original value out of any output
// encoded as JSON or YAML.
func (s String) MarshalText() ([]byte, error) {
	return []byte(masked), nil
}
