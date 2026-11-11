package config

import (
	"strings"
	"testing"

	"github.com/Aareevs/Kestrel-CLI/internal/events"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

const goodPattern = `  - id: U001-shard-down
    description: a database shard is unreachable
    type: dependency_conn_failed
    severity: error
    regex: 'shard (?P<target>\S+) unreachable'
    match: ["PRISMA: shard db-7:5432 unreachable"]
    no_match: ["shard db-7:5432 healthy"]
`

func parse(t *testing.T, patterns string) (*Config, error) {
	t.Helper()
	return Parse("kestrel.yaml", []byte("version: 1\npatterns:\n"+patterns), "")
}

func TestCustomPatternsLoadAndBecomeALibrary(t *testing.T) {
	cfg, err := parse(t, goodPattern+`  - id: U002-quota
    description: the billing quota was hit
    type: log_line
    severity: warn
    regex: 'QUOTA_EXCEEDED'
    match: ["code=QUOTA_EXCEEDED plan=free"]
    no_match: ["quota ok"]
`)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := cfg.PatternLibrary()
	if err != nil || lib == nil {
		t.Fatalf("library: %v", err)
	}
	c := lib.Classify("PRISMA: shard db-7:5432 unreachable")
	if c.PatternID != "U001-shard-down" || c.Type != api.EventDependencyConnFailed || c.Severity != api.SeverityError || c.Target != "db-7:5432" || c.Port != 5432 {
		t.Fatalf("classify = %+v", c)
	}
	if c := lib.Classify("code=QUOTA_EXCEEDED plan=free"); c.PatternID != "U002-quota" || c.Severity != api.SeverityWarn {
		t.Fatalf("classify = %+v", c)
	}
	// Built-ins still apply, and ahead of the project's patterns.
	if c := lib.Classify("dial tcp 127.0.0.1:5432: connect: connection refused"); c.PatternID != "P006-connection-refused" {
		t.Fatalf("built-in lost: %+v", c)
	}
	// The project's patterns don't leak into the default library.
	if got, base := len(lib.Patterns()), len(events.DefaultLibrary().Patterns()); got != base+2 {
		t.Fatalf("project library has %d patterns, default has %d", got, base)
	}
	if events.Classify("PRISMA: shard db-7:5432 unreachable").PatternID != "" {
		t.Fatal("a project's pattern leaked into the default library")
	}
}

func TestNoCustomPatternsMeansTheDefaultLibrary(t *testing.T) {
	cfg, err := Parse("kestrel.yaml", []byte("version: 1\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	if lib, err := cfg.PatternLibrary(); lib != nil || err != nil {
		t.Fatalf("got %v, %v", lib, err)
	}
}

func TestCustomPatternValidation(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    string
	}{
		{"id prefix", strings.Replace(goodPattern, "U001-shard-down", "P001-shard-down", 1), "line 3: patterns[0].id: \"P001-shard-down\" must look like U001-short-name"},
		{"id uppercase name", strings.Replace(goodPattern, "U001-shard-down", "U001-Shard", 1), "must look like U001-short-name"},
		{"unknown type", strings.Replace(goodPattern, "dependency_conn_failed", "crashed", 1), "line 5: patterns[0].type: unknown type \"crashed\" (use dependency_conn_failed, log_line, port_bind_failed, test_failed)"},
		{"lifecycle type refused", strings.Replace(goodPattern, "dependency_conn_failed", "process_crashed", 1), "unknown type \"process_crashed\""},
		{"unknown severity", strings.Replace(goodPattern, "severity: error", "severity: bad", 1), "patterns[0].severity: unknown severity \"bad\""},
		{"no description", strings.Replace(goodPattern, "description: a database shard is unreachable\n", "description: \"\"\n", 1), "patterns[0].description: required"},
		{"regex does not compile", strings.Replace(goodPattern, `'shard (?P<target>\S+) unreachable'`, `'shard ('`, 1), "patterns[0].regex: invalid regular expression"},
		{"regex too long", strings.Replace(goodPattern, `'shard (?P<target>\S+) unreachable'`, "'"+strings.Repeat("a", 1001)+"'", 1), "the limit is 1000"},
		{"unknown capture group", strings.Replace(goodPattern, "(?P<target>", "(?P<host>", 1), "capture group \"host\" is not a known attribute"},
		{"match example not matched", strings.Replace(goodPattern, `["PRISMA: shard db-7:5432 unreachable"]`, `["nothing relevant"]`, 1), "should match \"nothing relevant\" but doesn't"},
		{"no_match example matched", strings.Replace(goodPattern, `["shard db-7:5432 healthy"]`, `["shard x unreachable"]`, 1), "should not match \"shard x unreachable\" but does"},
		{"no match examples", strings.Replace(goodPattern, `match: ["PRISMA: shard db-7:5432 unreachable"]`, "match: []", 1), "patterns[0].match: needs at least one line"},
		{"no no_match examples", strings.Replace(goodPattern, `no_match: ["shard db-7:5432 healthy"]`, "no_match: []", 1), "patterns[0].no_match: needs at least one similar line"},
		{"duplicate id", goodPattern + goodPattern, "line 10: patterns[1].id: duplicate pattern ID U001-shard-down (first used by patterns[0])"},
		{"shadowed by a built-in", strings.Replace(strings.Replace(goodPattern, `'shard (?P<target>\S+) unreachable'`, `'connection refused'`, 1), `"PRISMA: shard db-7:5432 unreachable"`, `"dial tcp 10.0.0.1:80: connect: connection refused"`, 1), "already recognized by P006-connection-refused"},
		{"unknown field", goodPattern + "    colour: red\n", "unknown field \"colour\""},
	}
	for _, tt := range tests {
		_, err := parse(t, tt.pattern)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: got %v\nwant it to contain %q", tt.name, err, tt.want)
		}
	}
}

func TestTooManyCustomPatterns(t *testing.T) {
	var b strings.Builder
	for i := 0; i <= MaxCustomPatterns; i++ {
		b.WriteString(strings.Replace(goodPattern, "U001-shard-down", "U"+string(rune('0'+i/100%10))+string(rune('0'+i/10%10))+string(rune('0'+i%10))+"-x", 1))
	}
	if _, err := parse(t, b.String()); err == nil || !strings.Contains(err.Error(), "has 51 patterns (the limit is 50)") {
		t.Fatalf("got %v", err)
	}
}
