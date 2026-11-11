package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

var (
	schemaMu    sync.Mutex
	schemaCache = map[string]*jsonschema.Resolved{}
)

// validateSchema checks that out (what a command printed with --json)
// conforms to the published schema docs/schemas/<name>.schema.json. It is the
// guarantee that the schemas describe what Kestrel really prints, not only what
// the Go types say.
func validateSchema(t *testing.T, name, out string) {
	t.Helper()
	schemaMu.Lock()
	rs := schemaCache[name]
	if rs == nil {
		raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "schemas", name+".schema.json"))
		if err != nil {
			schemaMu.Unlock()
			t.Fatal(err)
		}
		var s jsonschema.Schema
		if err := json.Unmarshal(raw, &s); err != nil {
			schemaMu.Unlock()
			t.Fatalf("%s schema: %v", name, err)
		}
		if rs, err = s.Resolve(nil); err != nil {
			schemaMu.Unlock()
			t.Fatalf("%s schema does not resolve: %v", name, err)
		}
		schemaCache[name] = rs
	}
	schemaMu.Unlock()
	var instance any
	if err := json.Unmarshal([]byte(out), &instance); err != nil {
		t.Fatalf("not JSON: %v\n%.500s", err, out)
	}
	if err := rs.Validate(instance); err != nil {
		t.Errorf("output does not match docs/schemas/%s.schema.json: %v\n%.1500s", name, err, out)
	}
}
