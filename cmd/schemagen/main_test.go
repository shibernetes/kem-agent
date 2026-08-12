package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/shibernetes/kem-agent/checkpoint/configmap"
	"github.com/shibernetes/kem-agent/checkpoint/file"
	"github.com/shibernetes/kem-agent/cmd/internal/sinks"
	"github.com/shibernetes/kem-agent/config"
)

const (
	configDir  = "../../config"
	schemaFile = "agent.config.schema.json"
	metaSchema = "https://json-schema.org/draft/2020-12/schema"
	defaultKey = "default"
)

// testdataDir is resolved at init because generateSchema changes the
// working directory, so a relative path would not survive it.
var testdataDir = absPath("testdata")

func TestGeneratedSchemaIsAValidSchema(t *testing.T) {
	data := schemaBytes(t)

	if !json.Valid(data) {
		t.Fatal("the generated schema is not valid JSON")
	}
	meta, err := jsonschema.NewCompiler().Compile(metaSchema)
	if err != nil {
		t.Fatalf("failed to compile meta-schema: %v", err)
	}
	if err := meta.Validate(decodeJSON(t, data)); err != nil {
		t.Errorf("the schema does not conform to the 2020-12 draft: %v", err)
	}
}

// TestSchemaConformance checks that the schema accepts every configuration
// the loader accepts. The reverse cannot be checked, since JSON Schema
// cannot express the cross-reference rules, so each case declares on its
// own whether the schema passes the fixture.
func TestSchemaConformance(t *testing.T) {
	cases := map[string]struct {
		fixture string
		loads   bool
		passes  bool
	}{
		"smallest valid configuration":     {fixture: "valid/minimal", loads: true, passes: true},
		"every key of every sink":          {fixture: "valid/full", loads: true, passes: true},
		"both enrichment allowlists":       {fixture: "valid/enrichment", loads: true, passes: true},
		"misspelled sink key":              {fixture: "invalid/unknown-field"},
		"unknown sink type":                {fixture: "invalid/sink-type"},
		"sink missing a required key":      {fixture: "invalid/missing-endpoint"},
		"key belonging to the other store": {fixture: "invalid/store-key-crossed"},
		"batch key graylog does not carry": {fixture: "invalid/graylog-on-oversized"},
		"pipeline naming an unknown sink":  {fixture: "invalid/undeclared-sink", passes: true},
	}
	schema := compileSchema(t)

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if tc.loads && !tc.passes {
				t.Fatal("a configuration the loader accepts must pass the schema")
			}
			raw := readFixture(t, tc.fixture)

			switch _, err := config.Parse(raw, sinks.Factories()); {
			case tc.loads && err != nil:
				t.Fatalf("the loader rejected the fixture: %v", err)
			case !tc.loads && err == nil:
				t.Fatal("the loader accepted the fixture, expected a rejection")
			}
			switch err := schema.Validate(decodeYAML(t, raw)); {
			case tc.passes && err != nil:
				t.Errorf("the schema rejected the fixture: %v", err)
			case !tc.passes && err == nil:
				t.Error("the schema accepted the fixture, expected a rejection")
			}
		})
	}
}

// TestRecordedDefaults checks every default value against its property.
func TestRecordedDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), schemaFile)
	generateSchema(t, path)

	defaults := collectDefaults(decodeJSON(t, readFile(t, path)), "")
	if len(defaults) == 0 {
		t.Fatal("the schema records no default values")
	}
	compiler := jsonschema.NewCompiler()

	for _, pointer := range slices.Sorted(maps.Keys(defaults)) {
		t.Run(pointer, func(t *testing.T) {
			schema, err := compiler.Compile(fmt.Sprintf("%s#%s", path, pointer))
			if err != nil {
				t.Fatalf("failed to compile property: %v", err)
			}
			if err := schema.Validate(defaults[pointer]); err != nil {
				t.Errorf("the recorded default does not satisfy its property: %v", err)
			}
		})
	}
}

func TestSinkSubschemas(t *testing.T) {
	schemas := oneOfSubschemas(t, dig(decodeJSON(t, schemaBytes(t)), "properties", sinksObjectKey, "additionalProperties"))

	want := slices.Sorted(maps.Keys(sinks.Factories()))
	if got := slices.Sorted(maps.Keys(schemas)); !slices.Equal(got, want) {
		t.Fatalf("got subschemas %v, want one per registered sink %v", got, want)
	}
	// Reflected without a namer, a subschema takes the schema
	// of a nested type also called Config, which shows as the
	// sink's own keys missing.
	if _, ok := dig(schemas["otel"], "properties", "endpoint").(map[string]any); !ok {
		t.Error("the otel subschema has no endpoint key, it holds a nested type's schema")
	}
}

func TestStoreSubschemas(t *testing.T) {
	var (
		document   = decodeJSON(t, schemaBytes(t))
		subschemas = oneOfSubschemas(t, dig(document, "$defs", defName[config.Checkpoint](), "properties", storeObjectKey))
	)
	want := []string{configmap.TypeName, file.TypeName}
	if got := slices.Sorted(maps.Keys(subschemas)); !slices.Equal(got, want) {
		t.Fatalf("got subschemas %v, want one per store %v", got, want)
	}
	if _, ok := dig(document, "$defs", defName[config.Component]()).(map[string]any); ok {
		t.Error("the component definition replaced by the subschemas is still declared")
	}
}

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()

	path := filepath.Join(t.TempDir(), schemaFile)
	generateSchema(t, path)

	schema, err := jsonschema.NewCompiler().Compile(path)
	if err != nil {
		t.Fatalf("failed to compile schema: %v", err)
	}
	return schema
}

func generateSchema(t *testing.T, path string) {
	t.Helper()

	t.Chdir(configDir)

	if err := generate(path); err != nil {
		t.Fatalf("failed to generate schema: %v", err)
	}
}

func schemaBytes(t *testing.T) []byte {
	t.Helper()

	path := filepath.Join(t.TempDir(), schemaFile)
	generateSchema(t, path)

	return readFile(t, path)
}

// collectDefaults returns every default value a schema records,
// keyed by the JSON pointer of the property holding it.
func collectDefaults(node any, pointer string) map[string]any {
	defaults := make(map[string]any)

	switch n := node.(type) {
	case map[string]any:
		if v, ok := n[defaultKey]; ok {
			defaults[pointer] = v
		}
		for key, child := range n {
			if key != defaultKey {
				maps.Copy(defaults, collectDefaults(child, fmt.Sprintf("%s/%s", pointer, key)))
			}
		}
	case []any:
		for i, child := range n {
			maps.Copy(defaults, collectDefaults(child, fmt.Sprintf("%s/%d", pointer, i)))
		}
	}
	return defaults
}

// oneOfSubschemas returns the subschemas a component schema accepts,
// keyed by the constant on the type key that selects each one.
func oneOfSubschemas(t *testing.T, component any) map[string]any {
	t.Helper()

	oneOf, ok := dig(component, "oneOf").([]any)
	if !ok {
		t.Fatalf("got %v, want a component with one subschema per type", component)
	}
	subschemas := make(map[string]any, len(oneOf))

	for _, subschema := range oneOf {
		name, ok := dig(subschema, "properties", componentTypeKey, "const").(string)
		if !ok {
			t.Fatalf("a subschema has no constant on its %q key", componentTypeKey)
		}
		subschemas[name] = subschema
	}
	return subschemas
}

func dig(node any, keys ...string) any {
	for _, key := range keys {
		object, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = object[key]
	}
	return node
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	return readFile(t, filepath.Join(testdataDir, name+".yaml"))
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file %q: %v", path, err)
	}
	return data
}

func decodeJSON(t *testing.T, data []byte) any {
	t.Helper()

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("failed to decode document: %v", err)
	}
	return doc
}

func decodeYAML(t *testing.T, raw []byte) any {
	t.Helper()

	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("failed to decode fixture: %v", err)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("failed to encode fixture: %v", err)
	}
	return decodeJSON(t, data)
}

func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		panic(err)
	}
	return abs
}
