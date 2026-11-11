package schemas

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite docs/schemas/*.schema.json")

const apiDir = "../../pkg/api"

func schemaPath(name string) string {
	return filepath.Join("..", "..", "docs", "schemas", name+".schema.json")
}

// The committed schemas are exactly what the types generate. Regenerate with:
//
//	go test ./internal/schemas -update      (or: make schemas)
func TestCommittedSchemasAreCurrent(t *testing.T) {
	for _, name := range Names() {
		got, err := Generate(name, apiDir)
		if err != nil {
			t.Fatal(err)
		}
		path := schemaPath(name)
		if *update {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (run: go test ./internal/schemas -update)", err)
		}
		if string(want) != string(got) {
			t.Errorf("docs/schemas/%s.schema.json is out of date with pkg/api (run: go test ./internal/schemas -update)", name)
		}
	}
}

func TestGeneratedSchemasHaveTheExpectedShape(t *testing.T) {
	for _, name := range Names() {
		raw, err := Generate(name, apiDir)
		if err != nil {
			t.Fatal(err)
		}
		var s map[string]any
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatal(err)
		}
		if s["$schema"] != draft || s["$id"] != idBase+name+".schema.json" || s["title"] == "" || s["description"] == "" || s["type"] != "object" {
			t.Errorf("%s: metadata: %v %v %v", name, s["$schema"], s["$id"], s["type"])
		}
		props := s["properties"].(map[string]any)
		if sv := props["schema_version"].(map[string]any); sv["const"] != float64(1) {
			t.Errorf("%s: schema_version = %v", name, sv)
		}
		defs := s["$defs"].(map[string]any)
		if len(defs) == 0 {
			t.Errorf("%s: no $defs", name)
		}
		text := string(raw)
		// Closed-world is deliberately not claimed, and nothing is nullable.
		if strings.Contains(text, `"additionalProperties": false`) || strings.Contains(text, `"null"`) {
			t.Errorf("%s: contains additionalProperties:false or null", name)
		}
		// Every $ref resolves to a def.
		for _, ref := range refsIn(text) {
			if _, ok := defs[strings.TrimPrefix(ref, "#/$defs/")]; !ok {
				t.Errorf("%s: dangling $ref %s", name, ref)
			}
		}
	}
	if _, err := Generate("nope", apiDir); err == nil {
		t.Error("an unknown schema should be an error")
	}
}

func refsIn(s string) []string {
	var out []string
	for _, part := range strings.Split(s, `"$ref": "`)[1:] {
		out = append(out, part[:strings.Index(part, `"`)])
	}
	return out
}
