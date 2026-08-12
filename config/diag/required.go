package diag

import (
	"fmt"
	"reflect"
	"strings"
)

const (
	yamlTag         = "yaml"
	inlineTagOption = "inline"
)

// RequiredError reports one or more configuration fields that must be set.
type RequiredError struct {
	Fields []string
}

// Error implements the error interface.
func (e *RequiredError) Error() string {
	if len(e.Fields) == 1 {
		return e.Fields[0] + " is required"
	}
	return strings.Join(e.Fields, ", ") + " are required"
}

// Required reports the fields of cfg that are required but empty.
//
// A field is considered required when its yaml tag has neither omitempty
// nor omitzero, which is the rule the generated schema derives its own
// required list from.
func Required(cfg any) error {
	fields := findMissingFields(reflect.ValueOf(cfg), "")
	if len(fields) == 0 {
		return nil
	}
	return &RequiredError{Fields: fields}
}

// findMissingFields returns the path of every unset required field in v,
// each prefixed by the path of its parent value.
func findMissingFields(v reflect.Value, prefix string) []string {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	var fields []string

	for i := range v.NumField() {
		field := v.Type().Field(i)
		if !field.IsExported() {
			continue
		}
		// An inlined struct embeds its own keys, so its fields
		// keep the path of the surrounding struct.
		if tagHasOption(field, inlineTagOption) {
			fields = append(fields, findMissingFields(v.Field(i), prefix)...)
			continue
		}
		name, optional := yamlFieldName(field)
		if name == "" {
			continue
		}
		if !optional && v.Field(i).IsZero() {
			fields = append(fields, prefix+name)
			continue
		}
		fields = append(fields, descend(v.Field(i), prefix+name)...)
	}
	return fields
}

// descend walks into a field, indexing the elements of a slice so that
// a nested failure identifies the item it came from.
func descend(v reflect.Value, path string) []string {
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		return findMissingFields(v, path+".")
	}
	var fields []string

	for i := range v.Len() {
		fields = append(fields, findMissingFields(v.Index(i), fmt.Sprintf("%s[%d].", path, i))...)
	}
	return fields
}

// yamlFieldName returns the decode key of a field, and whether it is optional.
// An ignored field, represented by the tag '-', returns an empty name.
func yamlFieldName(field reflect.StructField) (string, bool) {
	tag, ok := field.Tag.Lookup(yamlTag)
	if !ok {
		return "", true
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return "", true
	}
	return name, tagHasOption(field, "omitempty") || tagHasOption(field, "omitzero")
}

// tagHasOption reports whether a field's yaml tag contains the given option.
func tagHasOption(field reflect.StructField, option string) bool {
	_, opts, _ := strings.Cut(field.Tag.Get(yamlTag), ",")

	for opt := range strings.SplitSeq(opts, ",") {
		if opt == option {
			return true
		}
	}
	return false
}
