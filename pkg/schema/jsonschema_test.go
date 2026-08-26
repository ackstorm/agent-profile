package schema

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// jsonschema_test.go asserts the STRUCTURE of the emitted JSON Schema rather
// than running documents through a general JSON Schema validator: this
// package has no dependency and standard library only, and a validator
// covering oneOf/propertyNames/$ref would itself be a second, untested
// implementation of the rules JSONSchema() states. Each test below targets
// exactly the mechanism that would make one §36 document invalid or valid,
// so reverting the corresponding piece of jsonschema.go fails the matching
// test — the same property a real validator run would have.

func decodeSchema(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(JSONSchema(), &m); err != nil {
		t.Fatalf("JSONSchema() did not marshal to valid JSON: %v", err)
	}
	return m
}

func at(t *testing.T, m map[string]any, path ...string) any {
	t.Helper()
	var cur any = m
	for _, p := range path {
		cm, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("path %v: %q is not an object (stopped at %q)", path, p, p)
		}
		cur, ok = cm[p]
		if !ok {
			t.Fatalf("path %v: missing key %q", path, p)
		}
	}
	return cur
}

func TestJSONSchemaIsValidJSON(t *testing.T) {
	if !json.Valid(JSONSchema()) {
		t.Fatal("JSONSchema() is not valid JSON")
	}
}

// F5: byte-stable across runs — a consumer that hashes it, or diffs two
// fetches, must see the same bytes every time.
func TestJSONSchemaIsByteStableAcrossCalls(t *testing.T) {
	a, b := JSONSchema(), JSONSchema()
	if !bytes.Equal(a, b) {
		t.Error("JSONSchema() is not byte-stable across two calls")
	}
}

// §36 "unknown key": the top level must name every key Decode's onlyKeys
// call allows and refuse everything else via additionalProperties: false.
func TestJSONSchemaRejectsAnUnknownTopLevelKey(t *testing.T) {
	m := decodeSchema(t)
	if v := at(t, m, "additionalProperties"); v != false {
		t.Errorf("top-level additionalProperties = %v, want false — an unknown key would be accepted", v)
	}
	props, ok := at(t, m, "properties").(map[string]any)
	if !ok {
		t.Fatal("properties is not an object")
	}
	want := map[string]bool{
		"version": true, "name": true, "extends": true, "targets": true,
		"model": true, "prompt": true, "inputs": true, "marketplaces": true,
		"skills": true, "mcps": true, "artifacts": true, "runtimes": true,
	}
	if len(props) != len(want) {
		t.Fatalf("top-level properties = %v, want exactly %v", keysOf(props), keysOf(want))
	}
	for k := range want {
		if _, ok := props[k]; !ok {
			t.Errorf("top-level properties missing %q", k)
		}
	}
	req, ok := at(t, m, "required").([]any)
	if !ok {
		t.Fatal("required is not an array")
	}
	if len(req) != 2 || !containsAny(req, "version") || !containsAny(req, "name") {
		t.Errorf("required = %v, want exactly [version name]", req)
	}
}

// §36 Model: "Authorization absent from model.headers" — model auth has
// exactly one representation and a literal header must not smuggle a second.
func TestJSONSchemaForbidsAuthorizationAsAModelHeaderKey(t *testing.T) {
	m := decodeSchema(t)
	pn, ok := at(t, m, "$defs", "model", "properties", "headers", "propertyNames").(map[string]any)
	if !ok {
		t.Fatal("$defs.model.properties.headers has no propertyNames restriction")
	}
	not, ok := pn["not"].(map[string]any)
	if !ok {
		t.Fatal("propertyNames has no \"not\" clause")
	}
	if not["const"] != "Authorization" {
		t.Errorf("propertyNames.not.const = %v, want \"Authorization\"", not["const"])
	}
}

// §36 Prompt: "exactly one of content | source". oneOf with exactly the two
// single-key required branches is what turns "both" and "neither" invalid,
// given additionalProperties: false already fixes the property set.
func TestJSONSchemaPromptRequiresExactlyOneOfContentOrSource(t *testing.T) {
	m := decodeSchema(t)
	oneOf, ok := at(t, m, "$defs", "prompt", "oneOf").([]any)
	if !ok || len(oneOf) != 2 {
		t.Fatalf("$defs.prompt.oneOf = %v, want two branches", oneOf)
	}
	if !branchRequires(oneOf, "content") || !branchRequires(oneOf, "source") {
		t.Errorf("oneOf branches = %v, want one requiring content and one requiring source", oneOf)
	}
	if got := at(t, m, "$defs", "prompt", "additionalProperties"); got != false {
		t.Errorf("$defs.prompt.additionalProperties = %v, want false", got)
	}
}

// §36 Secrets and inputs / §13: a binding is exactly one of env | file.
func TestJSONSchemaBindingRequiresExactlyOneOfEnvOrFile(t *testing.T) {
	m := decodeSchema(t)
	oneOf, ok := at(t, m, "$defs", "binding", "oneOf").([]any)
	if !ok || len(oneOf) != 2 {
		t.Fatalf("$defs.binding.oneOf = %v, want two branches", oneOf)
	}
	if !branchRequires(oneOf, "env") || !branchRequires(oneOf, "file") {
		t.Errorf("oneOf branches = %v, want one requiring env and one requiring file", oneOf)
	}
}

// §36 Prompt: "mode is append or replace".
func TestJSONSchemaModeIsRestrictedToAppendOrReplace(t *testing.T) {
	m := decodeSchema(t)
	enum, ok := at(t, m, "$defs", "prompt", "properties", "mode", "enum").([]any)
	if !ok {
		t.Fatal("$defs.prompt.properties.mode has no enum")
	}
	if len(enum) != 2 || !containsAny(enum, "append") || !containsAny(enum, "replace") {
		t.Errorf("mode enum = %v, want exactly [append replace]", enum)
	}
}

// The schema's allowed top-level keys must be a superset of every key the
// spec's own §35 fixture actually uses — the closest this suite comes to
// "accepts the real example" without a full validator: if the fixture used a
// key the schema did not list, this fails exactly like additionalProperties
// would reject it.
func TestJSONSchemaAcceptsEveryTopLevelKeyTheSpecFixtureUses(t *testing.T) {
	b, err := os.ReadFile("testdata/spec-35-execute.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseYAML(b)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeSchema(t)
	props, ok := at(t, m, "properties").(map[string]any)
	if !ok {
		t.Fatal("properties is not an object")
	}
	for _, k := range doc.Keys {
		if _, ok := props[k]; !ok {
			t.Errorf("fixture uses top-level key %q, which the schema's properties do not list", k)
		}
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func containsAny(items []any, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}

// branchRequires reports whether one of oneOf's branches is
// {"required": [key]}.
func branchRequires(oneOf []any, key string) bool {
	for _, b := range oneOf {
		bm, ok := b.(map[string]any)
		if !ok {
			continue
		}
		req, ok := bm["required"].([]any)
		if !ok {
			continue
		}
		if len(req) == 1 && req[0] == key {
			return true
		}
	}
	return false
}
