// Package schemas generates the JSON Schemas for Kestrel's report output
// (`diagnose`, `timeline`, `verify` with --json), from the Go types in pkg/api
// that produce it. The committed files in docs/schemas are the output of
// Generate; a test fails when they drift from the types.
//
// The types are the source of truth. Descriptions come from their doc
// comments, and enums from their constant blocks (both read from the source of
// pkg/api at generation time), so a new field or value cannot be forgotten.
//
// The schemas describe what Kestrel prints today. They don't set
// additionalProperties to false: a later minor release may add fields, and a
// consumer validating against an older copy must not start failing because of
// it.
package schemas

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

const draft = "https://json-schema.org/draft/2020-12/schema"

// idBase is where the schemas live in the repository.
const idBase = "https://github.com/Aareevs/Kestrel-CLI/docs/schemas/"

// Doc is one published schema.
type Doc struct {
	Name        string // diagnose, timeline, verify
	Title       string
	Description string
	typ         reflect.Type
}

var docs = []Doc{
	{"diagnose", "kestrel diagnose --json",
		"The report `kestrel diagnose --json` prints (and the MCP tool get_diagnosis returns): each incident in a session with ranked, evidence-backed hypotheses.",
		reflect.TypeFor[api.DiagnosisReport]()},
	{"timeline", "kestrel timeline --json",
		"The report `kestrel timeline --json` prints (and the MCP tool get_failure_timeline returns): each incident's timeline, the dependency graph, and with --service that service's recorded output.",
		reflect.TypeFor[api.TimelineReport]()},
	{"verify", "kestrel verify --json",
		"The report `kestrel verify --json` prints: the checks run across the stability window, what recurred, and the verdict.",
		reflect.TypeFor[api.VerificationReport]()},
}

// stringEnums are the named string types that have a closed set of values,
// which Generate reads from their const blocks in pkg/api.
var stringEnums = map[string]reflect.Type{
	"ClaimLevel":     reflect.TypeFor[api.ClaimLevel](),
	"Confidence":     reflect.TypeFor[api.Confidence](),
	"DependencyKind": reflect.TypeFor[api.DependencyKind](),
	"EventType":      reflect.TypeFor[api.EventType](),
	"Severity":       reflect.TypeFor[api.Severity](),
	"EventSource":    reflect.TypeFor[api.EventSource](),
	"Liveness":       reflect.TypeFor[api.Liveness](),
	"Readiness":      reflect.TypeFor[api.Readiness](),
	"Verdict":        reflect.TypeFor[api.Verdict](),
}

// fieldEnums are fields typed as plain string whose values are a closed set
// (declared as untyped constants in pkg/api).
var fieldEnums = map[string][]string{
	"Incident.correlation_strength": {api.CorrelationStrong, api.CorrelationWeak},
	"TimelineEntry.role":            {api.RoleHealthy, api.RoleFirstAnomaly, api.RoleKey, api.RoleSymptom, api.RoleRecovery, api.RoleOutput},
	"CheckResult.kind":              {api.CheckKindProbe, api.CheckKindTCP, api.CheckKindLiveness, api.CheckKindCommand},
	"CheckResult.status":            {api.CheckPassed, api.CheckFlaky, api.CheckFailed, api.CheckTimedOut, api.CheckSkipped},
	"SessionSummary.status":         {api.SessionActive, api.SessionClosed, api.SessionInterrupted},
}

// Title is a schema's short title, for listings.
func Title(name string) (string, error) {
	for _, d := range docs {
		if d.Name == name {
			return d.Title, nil
		}
	}
	return "", fmt.Errorf("no schema %q", name)
}

// Names lists the published schemas.
func Names() []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.Name
	}
	return out
}

// meta is what the pkg/api source says: doc comments and constant values.
type meta struct {
	typeDoc  map[string]string   // type name -> doc
	fieldDoc map[string]string   // "Type.json_name" -> doc
	consts   map[string][]string // type name -> values, in source order
}

func parseAPI(dir string) (*meta, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	m := &meta{typeDoc: map[string]string{}, fieldDoc: map[string]string{}, consts: map[string][]string{}}
	pkg, ok := pkgs["api"]
	if !ok {
		return nil, fmt.Errorf("no package api in %s", dir)
	}
	names := make([]string, 0, len(pkg.Files))
	for n := range pkg.Files {
		names = append(names, n)
	}
	sort.Strings(names) // a stable order for constants
	for _, n := range names {
		for _, decl := range pkg.Files[n].Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			switch gd.Tok {
			case token.TYPE:
				for _, spec := range gd.Specs {
					ts := spec.(*ast.TypeSpec)
					doc := ts.Doc
					if doc == nil {
						doc = gd.Doc
					}
					m.typeDoc[ts.Name.Name] = clean(doc)
					if st, ok := ts.Type.(*ast.StructType); ok {
						for _, f := range st.Fields.List {
							if len(f.Names) == 0 || f.Tag == nil {
								continue
							}
							tag, _ := strconv.Unquote(f.Tag.Value)
							jsonName := strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
							if jsonName == "" || jsonName == "-" {
								continue
							}
							d := clean(f.Doc)
							if d == "" {
								d = clean(f.Comment)
							}
							if d != "" {
								m.fieldDoc[ts.Name.Name+"."+jsonName] = d
							}
						}
					}
				}
			case token.CONST:
				for _, spec := range gd.Specs {
					vs := spec.(*ast.ValueSpec)
					id, ok := vs.Type.(*ast.Ident)
					if !ok || len(vs.Values) != 1 {
						continue
					}
					if lit, ok := vs.Values[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						v, _ := strconv.Unquote(lit.Value)
						m.consts[id.Name] = append(m.consts[id.Name], v)
					}
				}
			}
		}
	}
	return m, nil
}

// clean turns a comment group into one line of prose.
func clean(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	return strings.Join(strings.Fields(cg.Text()), " ")
}

// structsIn returns the named struct types reachable from t, sorted by name.
func structsIn(t reflect.Type) []reflect.Type {
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			walk(t.Elem())
		case reflect.Map:
			walk(t.Elem())
		case reflect.Struct:
			if t == reflect.TypeFor[time.Time]() || seen[t] {
				return
			}
			seen[t] = true
			for i := 0; i < t.NumField(); i++ {
				if f := t.Field(i); f.IsExported() {
					walk(f.Type)
				}
			}
		}
	}
	walk(t)
	out := make([]reflect.Type, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Generate returns the schema called name as indented JSON. apiDir is the
// directory of the pkg/api sources, for doc comments and constants.
func Generate(name, apiDir string) ([]byte, error) {
	var doc *Doc
	for i := range docs {
		if docs[i].Name == name {
			doc = &docs[i]
		}
	}
	if doc == nil {
		return nil, fmt.Errorf("no schema %q (have: %s)", name, strings.Join(Names(), ", "))
	}
	m, err := parseAPI(apiDir)
	if err != nil {
		return nil, err
	}

	// Types that stand for something specific: timestamps and enums.
	base := map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[time.Time](): {Type: "string", Format: "date-time"},
	}
	for tn, rt := range stringEnums {
		vals := m.consts[tn]
		if len(vals) == 0 {
			return nil, fmt.Errorf("enum type %s has no constants in %s", tn, apiDir)
		}
		enum := make([]any, len(vals))
		for i, v := range vals {
			enum[i] = v
		}
		base[rt] = &jsonschema.Schema{Type: "string", Description: m.typeDoc[tn], Enum: enum}
	}

	structs := structsIn(doc.typ)
	refs := map[reflect.Type]*jsonschema.Schema{}
	for _, st := range structs {
		refs[st] = &jsonschema.Schema{Ref: "#/$defs/" + st.Name()}
	}

	build := func(st reflect.Type) (*jsonschema.Schema, error) {
		ts := map[reflect.Type]*jsonschema.Schema{}
		for k, v := range base {
			ts[k] = v
		}
		for k, v := range refs {
			if k != st { // the type being defined is not a reference to itself
				ts[k] = v
			}
		}
		s, err := jsonschema.ForType(st, &jsonschema.ForOptions{TypeSchemas: ts})
		if err != nil {
			return nil, err
		}
		tidy(s)
		s.Description = m.typeDoc[st.Name()]
		for jn, ps := range s.Properties {
			key := st.Name() + "." + jn
			if d := m.fieldDoc[key]; d != "" && ps.Description == "" {
				ps.Description = d
			}
			if vals, ok := fieldEnums[key]; ok {
				for _, v := range vals {
					ps.Enum = append(ps.Enum, v)
				}
				ps.Type = "string"
				ps.Types = nil
			}
		}
		return s, nil
	}

	root, err := build(doc.typ)
	if err != nil {
		return nil, err
	}
	root.Schema = draft
	root.ID = idBase + doc.Name + ".schema.json"
	root.Title = doc.Title
	root.Description = doc.Description
	if sv, ok := root.Properties["schema_version"]; ok {
		sv.Const = ptr[any](api.SchemaVersion)
		sv.Type = ""
		sv.Types = nil
		sv.Description = "The version of this report format. Fields are added within a version; a change that removes or changes one bumps it."
	}
	root.Defs = map[string]*jsonschema.Schema{}
	for _, st := range structs {
		if st == doc.typ {
			continue
		}
		def, err := build(st)
		if err != nil {
			return nil, err
		}
		root.Defs[st.Name()] = def
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func ptr[T any](v T) *T { return &v }

// tidy applies the choices that make the schemas describe the contract
// rather than Go's representation: no additionalProperties:false (see the
// package comment) and no "null" alternatives (Kestrel prints [] and omits
// absent fields; a test validates real output against these schemas).
func tidy(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	// For marks structs closed with {"not": {}}; maps carry their value schema
	// here, which stays.
	if ap := s.AdditionalProperties; ap != nil && ap.Not != nil && reflect.DeepEqual(*ap.Not, jsonschema.Schema{}) {
		s.AdditionalProperties = nil
	}
	if len(s.Types) > 0 {
		var keep []string
		for _, t := range s.Types {
			if t != "null" {
				keep = append(keep, t)
			}
		}
		switch len(keep) {
		case 0:
			s.Types = nil
		case 1:
			s.Type, s.Types = keep[0], nil
		default:
			s.Types = keep
		}
	}
	for _, p := range s.Properties {
		tidy(p)
	}
	tidy(s.Items)
	if s.AdditionalProperties != nil {
		tidy(s.AdditionalProperties)
	}
}
