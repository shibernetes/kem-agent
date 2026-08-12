// The schemagen command writes the JSON schema of the agent configuration.
// The schema is intended as an authoring aid for editors and for the chart
// values, and is never loaded by the agent to validate the configuration.
//
// It runs from the config package, through a go:generate directive.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/shibernetes/kem-agent/checkpoint/configmap"
	"github.com/shibernetes/kem-agent/checkpoint/file"
	"github.com/shibernetes/kem-agent/cmd/internal/sinks"
	"github.com/shibernetes/kem-agent/config"
)

const (
	defaultPath      = "schema/agent.config.schema.json"
	componentTypeKey = "type"
	sinksObjectKey   = "sinks"
	storeObjectKey   = "store"
	yamlTag          = "yaml"
	refKey           = "$ref"
	defsPrefix       = "#/$defs/"
)

var (
	rootType   = reflect.TypeFor[config.Config]()
	modulePath = path.Dir(rootType.PkgPath())
)

func main() {
	p := defaultPath

	if len(os.Args) > 1 {
		p = os.Args[1]
	}
	if err := generate(p); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "schemagen:", err.Error())
		os.Exit(1)
	}
}

// generate reflects the root configuration type into a JSON schema
// and writes it to a file at the given path. The keys come from the
// yaml tags and the descriptions from the doc comments, so both follow
// the types on their own.
func generate(p string) error {
	r := &jsonschema.Reflector{
		BaseSchemaID:   jsonschema.ID("https://" + modulePath),
		FieldNameTag:   yamlTag,
		ExpandedStruct: true,
		Namer:          typeNamer,
	}
	if err := r.AddGoComments(rootType.PkgPath(), "..", jsonschema.WithFullComment()); err != nil {
		return fmt.Errorf("failed to add doc comments: %w", err)
	}
	key := fmt.Sprintf("%s.%s", rootType.PkgPath(), rootType.Name())

	if r.CommentMap[key] == "" {
		return errors.New("no doc comments were found")
	}
	for name, comment := range r.CommentMap {
		r.CommentMap[name] = strings.Join(strings.Fields(comment), " ")
	}
	schema := r.Reflect(&config.Config{})

	refs, err := countReferences(schema)
	if err != nil {
		return err
	}
	d := defaulter{
		reflector:   r,
		definitions: schema.Definitions,
		references:  refs,
	}
	// Expanding also stops the reflector from referencing definitions,
	// which the defaults below rely on when they copy a shared one.
	if err := expandComponents(d, schema); err != nil {
		return err
	}
	d.apply(schema, reflect.ValueOf(config.DefaultConfig()))

	if err := pruneDefinitions(schema); err != nil {
		return err
	}
	b, err := json.MarshalIndent(schema, "", strings.Repeat(" ", 4))
	if err != nil {
		return fmt.Errorf("failed to marshal schema: %w", err)
	}
	if err := os.WriteFile(filepath.Clean(p), append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("failed to write schema file %q: %w", p, err)
	}
	return nil
}

// expandComponents replaces each component reference with one
// subschema per type that can be declared in its place, so a sink
// and a store describe their own keys.
//
// Inlining gives each type its own defaults rather than one shared
// set. The reflector is reused for its namer, or a nested type named
// Config replaces the one reflected.
func expandComponents(d defaulter, schema *jsonschema.Schema) error {
	d.reflector.DoNotReference = true
	d.reflector.Anonymous = true

	sinkSchema, err := componentSchema(d, defaultSinkConfigs())
	if err != nil {
		return err
	}
	storeSchema, err := componentSchema(d, defaultStoreConfigs())
	if err != nil {
		return err
	}
	prop, ok := schema.Properties.Get(sinksObjectKey)
	if !ok {
		return fmt.Errorf("the schema has no %q property", sinksObjectKey)
	}
	prop.AdditionalProperties = sinkSchema

	checkpoint, ok := schema.Definitions[defName[config.Checkpoint]()]
	if !ok {
		return errors.New("the schema has no checkpoint definition")
	}
	if _, ok := checkpoint.Properties.Get(storeObjectKey); !ok {
		return fmt.Errorf("the checkpoint definition has no %q property", storeObjectKey)
	}
	checkpoint.Properties.Set(storeObjectKey, storeSchema)
	delete(schema.Definitions, defName[config.Component]())

	return nil
}

// componentSchema returns the schema of a component block, with one
// subschema per type it can declare. Each pins the type key to a constant,
// so the declared type selects which one applies.
func componentSchema(d defaulter, configs map[string]any) (*jsonschema.Schema, error) {
	s := new(jsonschema.Schema)

	for _, name := range slices.Sorted(maps.Keys(configs)) {
		typeSchema := d.reflector.Reflect(configs[name])
		typeSchema.Version = ""

		d.apply(typeSchema, reflect.ValueOf(configs[name]))

		t, ok := typeSchema.Properties.Get(componentTypeKey)
		if !ok {
			return nil, fmt.Errorf("the %q configuration has no %q key", name, componentTypeKey)
		}
		// The type is pinned rather than defaulted, since a document
		// only reaches this schema once it has written the type.
		t.Const = name
		t.Default = nil
		s.OneOf = append(s.OneOf, typeSchema)
	}
	return s, nil
}

// A defaulter records on a schema what a default configuration contains.
type defaulter struct {
	reflector   *jsonschema.Reflector
	definitions jsonschema.Definitions
	references  map[string]int
}

// apply sets each property's default to the value of the field
// with the same key.
func (d defaulter) apply(s *jsonschema.Schema, v reflect.Value) {
	if v = indirect(v); v.Kind() != reflect.Struct || s.Properties == nil {
		return
	}
	for i := range v.NumField() {
		field := v.Type().Field(i)
		if !field.IsExported() || skipField(v.Field(i)) {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get(yamlTag), ",")

		switch name {
		case "-":
		case "":
			// An inlined struct writes its keys into the schema of
			// its parent, so descend into it.
			d.apply(s, v.Field(i))
		default:
			if prop, ok := s.Properties.Get(name); ok {
				d.set(prop, v.Field(i))
			}
		}
	}
}

// set records a value on a property, descending into it when the value is
// a struct. A property referencing a definition that others also reference
// takes a copy of it first, since a default written into the definition
// would reach every one of them.
func (d defaulter) set(prop *jsonschema.Schema, v reflect.Value) {
	target := prop

	if def, ok := d.definitions[strings.TrimPrefix(prop.Ref, defsPrefix)]; ok {
		target = def
	}
	if v = indirect(v); v.Kind() != reflect.Struct {
		prop.Default = scalarDefault(target, v)
		return
	}
	if d.references[prop.Ref] > 1 {
		*prop = *d.reflector.Reflect(v.Interface())
		prop.Version = ""
		target = prop
	}
	d.apply(target, v)
}

// pruneDefinitions removes the definitions that are not
// referenced by any schema.
func pruneDefinitions(schema *jsonschema.Schema) error {
	refCounts, err := countReferences(schema)
	if err != nil {
		return err
	}
	maps.DeleteFunc(schema.Definitions, func(name string, _ *jsonschema.Schema) bool {
		return refCounts[defsPrefix+name] == 0
	})
	return nil
}

// skipField reports whether a field has no default to record.
//
// A zero value is skipped, since it looks the same whether the default
// configuration set it or left it alone. A bool is kept because false
// is a real value, a non-nil pointer because it was set on purpose, and
// a struct because its own fields may hold defaults.
func skipField(v reflect.Value) bool {
	switch indirect(v).Kind() {
	case reflect.Bool, reflect.Struct:
		return false
	default:
		return v.IsZero()
	}
}

// scalarDefault returns a value in the form its property accepts,
// so a type that decodes from a string is represented by its own
// String method rather than by its underlying value.
func scalarDefault(prop *jsonschema.Schema, v reflect.Value) any {
	if s, ok := v.Interface().(fmt.Stringer); ok && acceptsString(prop) {
		return s.String()
	}
	return v.Interface()
}

// acceptsString reports whether a schema takes a string, as its own
// type or under oneOf, which is where a type accepting more than one
// form declares it.
func acceptsString(s *jsonschema.Schema) bool {
	if s.Type == "string" {
		return true
	}
	return slices.ContainsFunc(s.OneOf, func(schema *jsonschema.Schema) bool {
		return schema.Type == "string"
	})
}

func indirect(v reflect.Value) reflect.Value {
	for (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && !v.IsNil() {
		v = v.Elem()
	}
	return v
}

// countReferences counts how many times a definition is used by
// a schema, keyed by the definition pointer.
func countReferences(schema *jsonschema.Schema) (map[string]int, error) {
	b, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal schema: %w", err)
	}
	var node any

	if err := json.Unmarshal(b, &node); err != nil {
		return nil, fmt.Errorf("failed to unmarshal schema: %w", err)
	}
	counts := make(map[string]int)
	countRefsInNode(node, counts)

	return counts, nil
}

func countRefsInNode(node any, counts map[string]int) {
	switch n := node.(type) {
	case map[string]any:
		if ref, ok := n[refKey].(string); ok {
			counts[ref]++
		}
		for _, v := range n {
			countRefsInNode(v, counts)
		}
	case []any:
		for _, v := range n {
			countRefsInNode(v, counts)
		}
	}
}

func defaultSinkConfigs() map[string]any {
	configs := make(map[string]any)

	for name, factory := range sinks.Factories() {
		configs[name] = factory.CreateDefaultConfig()
	}
	return configs
}

func defaultStoreConfigs() map[string]any {
	return map[string]any{
		configmap.TypeName: configmap.DefaultConfig(),
		file.TypeName:      file.DefaultConfig(),
	}
}

// typeNamer names a definition after the package declaring the type,
// so the multiple types named Config do not collide with each other.
// The root type keeps its plain name, which only feeds the schema id
// since its definition is inlined. A type with no name is also inlined.
func typeNamer(t reflect.Type) string {
	if t.Name() == "" {
		return ""
	}
	if t == rootType {
		return t.Name()
	}
	pkg := strings.TrimPrefix(t.PkgPath(), modulePath+"/")

	return strings.ReplaceAll(pkg, "/", ".") + "." + t.Name()
}

// defName returns the key a type's definition is stored under.
func defName[T any]() string {
	return typeNamer(reflect.TypeFor[T]())
}
