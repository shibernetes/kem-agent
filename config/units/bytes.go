package units

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode"

	"github.com/goccy/go-yaml/ast"
	"github.com/invopop/jsonschema"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/shibernetes/kem-agent/config/diag"
)

// quantityPattern is the Regex validating a memory quantity.
const quantityPattern = `^[+-]?[0-9]+(\.[0-9]+)?(Ki|Mi|Gi|Ti|Pi|Ei|k|M|G|T|P|E)?$`

// Bytes represents a memory quantity expressed in bytes.
// It can be written as a plain number or with either a binary unit
// (Ki, Mi, Gi, Ti, Pi, Ei) or a decimal one (k, M, G, T, P, E).
type Bytes int64

// Unit lists a memory quantity may use, aligned by index so each decimal unit
// maps to the binary one of the same magnitude. They whitelist the input before
// parsing, which excludes the sub-unit and exponent [resource.ParseQuantity]
// would otherwise accept for CPU and scientific notation. A quantity may also
// carry no unit at all, which denotes a plain count of bytes.
var (
	unitsSI  = []string{"k", "M", "G", "T", "P", "E"}
	unitsIEC = []string{"Ki", "Mi", "Gi", "Ti", "Pi", "Ei"}
)

// String implements the [fmt.Stringer] interface.
// It writes the canonical form of the quantity, which prefers a binary unit,
// so a value reads back as itself but not always as it was written.
func (b Bytes) String() string {
	return resource.NewQuantity(int64(b), resource.BinarySI).String()
}

// JSONSchema satisfies the [github.com/invopop/jsonschema] reflector.
// It describes the written form of a memory quantity, in the JSON Schema spec.
func (Bytes) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Description: "A memory quantity expressed in bytes.",
		Examples: []any{
			4096, "32Mi", "1Gi",
		},
		OneOf: []*jsonschema.Schema{
			{Type: "integer", Minimum: "0"},
			{Type: "string", Pattern: quantityPattern},
		},
	}
}

// UnmarshalYAML implements the [github.com/goccy/go-yaml.NodeUnmarshaler] interface.
// It reports a malformed quantity at the line it was written on.
func (b *Bytes) UnmarshalYAML(node ast.Node) error {
	s, ok := asScalar(node)
	if !ok || s == "" {
		return diag.Atf(node, "expected a quantity string")
	}
	unit := unitOf(s)
	if !isAcceptedUnit(unit) {
		if hint := unitHint(s, unit); hint != "" {
			return diag.Atf(node, "invalid quantity %q: %s", s, hint)
		}
		return diag.Atf(node, "invalid quantity %q", s)
	}
	// The number is checked here rather than left to the parser because it
	// reads a bare unit as zero, which would be interpreted as no limit.
	if !isNumber(strings.TrimSuffix(s, unit)) {
		return diag.Atf(node, "invalid quantity %q", s)
	}
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return diag.Atf(node, "invalid quantity %q: %w", s, err)
	}
	if q.CmpInt64(0) < 0 {
		return diag.Atf(node, "invalid quantity %q: a quantity cannot be negative", s)
	}
	if q.CmpInt64(math.MaxInt64) >= 0 {
		return diag.Atf(node, "invalid quantity %q: a quantity must be less than 8Ei", s)
	}
	// The value is only meaningful once the two checks above have passed.
	// It saturates past the int64 limit, and past a few hundred digits it
	// returns an undefined value. Within the limit, it rounds a fraction
	// away from zero, which is what the comparison below is reading.
	value := q.Value()
	if q.CmpInt64(value) != 0 {
		return diag.Atf(node, "invalid quantity %q: not a whole number", s)
	}
	*b = Bytes(value)

	return nil
}

// unitHint explains a rejected unit in terms of the form that would have been
// accepted. The wrong forms are predictable, so most of them earn a suggestion
// rather than a list to read. It returns nothing when the unit is really the
// tail of a malformed number, which the value alone already shows.
func unitHint(s, unit string) string {
	num := strings.TrimSuffix(s, unit)

	if len(unit) > 1 && (unit[0] == 'e' || unit[0] == 'E') && isDigit(unit[1]) {
		return "exponents are not accepted"
	}
	if !allLetters(unit) {
		// A second dot or a stray digit leaves a unit that is really the
		// tail of a malformed number, so there is no unit to explain.
		return ""
	}
	base, ok := strings.CutSuffix(unit, "B")

	if ok && isAcceptedUnit(base) {
		if i := slices.Index(unitsSI, base); i >= 0 {
			return fmt.Sprintf("quantities take no B unit, write %[1]s%[2]s for SI or %[1]s%[3]s for IEC", num, base, unitsIEC[i])
		}
		return fmt.Sprintf("quantities take no B unit, write %s%s", num, base)
	}
	switch unit {
	case "K":
		return fmt.Sprintf("units are case sensitive, write %[1]sk for SI or %[1]sKi for IEC", num)
	case "m", "u", "µ", "n":
		return fmt.Sprintf("%q is a sub-unit and reads as less than one byte", unit)
	}
	return fmt.Sprintf("unknown unit %q, accepted units are %s", unit, unitList())
}

// unitOf returns the unit following a number, if any.
// It deliberately does not take the trailing letters because an exponent ends
// with a digit, and taking letters alone would read 1e6 as a plain number.
func unitOf(s string) string {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && isDigit(s[i]) {
			i++
		}
	}
	return s[i:]
}

// isAcceptedUnit reports whether a memory quantity may be expressed
// with the given unit.
func isAcceptedUnit(unit string) bool {
	return unit == "" || slices.Contains(unitsSI, unit) || slices.Contains(unitsIEC, unit)
}

// unitList renders every accepted unit as a string, in the order the
// two systems are declared in.
func unitList() string {
	units := slices.Concat(unitsSI, unitsIEC)
	return strings.Join(units[:len(units)-1], ", ") + " and " + units[len(units)-1]
}

// isNumber reports whether s is a whole or fractional decimal number.
func isNumber(s string) bool {
	digits, fraction, dotted := strings.Cut(strings.TrimLeft(s, "+-"), ".")
	if dotted && !allDigits(fraction) {
		return false
	}
	return allDigits(digits)
}

func allLetters(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}
