package config

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

// TestJSONSchemaInSync keeps docs/kestrel.schema.json aligned with the Go
// structs: every object in the schema must list exactly the struct's keys.
func TestJSONSchemaInSync(t *testing.T) {
	raw, err := os.ReadFile("../../docs/kestrel.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	defs := schema["$defs"].(map[string]any)
	check := func(name string, props map[string]any, typ reflect.Type) {
		var got []string
		for k := range props {
			got = append(got, k)
		}
		sort.Strings(got)
		want := YAMLFields(typ)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: schema properties %v, struct fields %v", name, got, want)
		}
		if ap, ok := propsParent(schema, name, defs)["additionalProperties"]; !ok || ap != false {
			t.Errorf("%s: schema must set additionalProperties: false", name)
		}
	}
	check("Config", schema["properties"].(map[string]any), reflect.TypeOf(Config{}))
	for name, typ := range schemaTypes {
		if name == "Config" {
			continue
		}
		def, ok := defs[name].(map[string]any)
		if !ok {
			t.Errorf("schema is missing $defs.%s", name)
			continue
		}
		check(name, def["properties"].(map[string]any), typ)
	}
}

func propsParent(schema map[string]any, name string, defs map[string]any) map[string]any {
	if name == "Config" {
		return schema
	}
	return defs[name].(map[string]any)
}
