package events

import (
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// TestPatterns enforces each pattern's own evidence: every Match line must
// match it and every NoMatch line must not.
func TestPatterns(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Patterns {
		t.Run(p.ID, func(t *testing.T) {
			if seen[p.ID] {
				t.Fatalf("duplicate pattern ID")
			}
			seen[p.ID] = true
			if p.Description == "" || p.Type == "" || p.Severity == "" {
				t.Fatal("pattern needs a description, type and severity")
			}
			if len(p.Match) == 0 || len(p.NoMatch) == 0 {
				t.Fatal("pattern needs at least one Match and one NoMatch example")
			}
			for _, line := range p.Match {
				if !p.Re.MatchString(line) {
					t.Errorf("should match: %q", line)
				}
			}
			for _, line := range p.NoMatch {
				if p.Re.MatchString(line) {
					t.Errorf("should not match: %q", line)
				}
			}
			for _, name := range p.Re.SubexpNames()[1:] {
				if name != "" && !strings.HasPrefix(name, "port") && !strings.HasPrefix(name, "target") {
					t.Errorf("capture group %q is not a known attribute (port*, target*)", name)
				}
			}
		})
	}
	if PatternByID("P001-port-in-use") == nil {
		t.Fatal("PatternByID")
	}
}

// TestPatternExamplesClassifyAsThemselves checks ordering: each Match example
// must be claimed by its own pattern, not shadowed by an earlier one.
func TestPatternExamplesClassifyAsThemselves(t *testing.T) {
	for _, p := range Patterns {
		for _, line := range p.Match {
			if got := Classify(line).PatternID; got != p.ID {
				t.Errorf("%q classified as %q, want %q", line, got, p.ID)
			}
		}
	}
}

func TestClassifyAttrs(t *testing.T) {
	tests := []struct {
		line     string
		typ      api.EventType
		sev      api.Severity
		port     int
		target   string
		explicit bool
	}{
		{"listen tcp :8080: bind: address already in use", api.EventPortBindFailed, api.SeverityError, 8080, "", false},
		{"Error: listen EADDRINUSE: address already in use :::3000", api.EventPortBindFailed, api.SeverityError, 3000, "", true},
		{"dial tcp 127.0.0.1:5432: connect: connection refused", api.EventDependencyConnFailed, api.SeverityError, 5432, "127.0.0.1:5432", false},
		{`time=2024-01-01T00:00:00Z level=warn msg="dial tcp 127.0.0.1:6379: connect: connection refused, retrying"`, api.EventDependencyConnFailed, api.SeverityWarn, 6379, "127.0.0.1:6379", true},
		// Node puts "connect" before ECONNREFUSED; the leftmost alternative must still capture the target.
		{"Error: connect ECONNREFUSED 127.0.0.1:8080", api.EventDependencyConnFailed, api.SeverityError, 8080, "127.0.0.1:8080", true},
		{"Could not connect to Redis at 127.0.0.1:6379: Connection refused", api.EventDependencyConnFailed, api.SeverityError, 6379, "127.0.0.1:6379", false},
		{`INFO panic: boom`, api.EventLogLine, api.SeverityInfo, 0, "", true}, // not at line start: not a panic
		{"panic: runtime error: index out of range", api.EventLogLine, api.SeverityFatal, 0, "", false},
		{"  ➜  Local:   http://localhost:5173/", api.EventLogLine, api.SeverityInfo, 5173, "", false},
		{"just a normal line", api.EventLogLine, api.SeverityInfo, 0, "", false},
	}
	for _, tt := range tests {
		c := Classify(tt.line)
		if c.Type != tt.typ || c.Severity != tt.sev || c.Port != tt.port || c.Target != tt.target || c.Explicit != tt.explicit {
			t.Errorf("Classify(%q) = %+v", tt.line, c)
		}
	}
}

func TestLevel(t *testing.T) {
	tests := map[string]api.Severity{
		`{"level":"error","msg":"x"}`:                          api.SeverityError,
		`{"level":50,"msg":"x"}`:                               api.SeverityError,
		`{"level":30,"msg":"x"}`:                               api.SeverityInfo,
		`time=… level=WARN msg=slow`:                           api.SeverityWarn,
		`2024/01/02 15:04:05 ERROR failed to load`:             api.SeverityError,
		`[DEBUG] cache miss`:                                   api.SeverityDebug,
		`ERROR:root:something broke`:                           api.SeverityError,
		`error: could not compile`:                             api.SeverityError,
		`warning: deprecated flag`:                             api.SeverityWarn,
		`I1015 12:00:00.000 main.go:10] CRITICAL thing`:        api.SeverityFatal,
		`INFO     uvicorn.error: Application startup complete`: api.SeverityInfo,
	}
	for line, want := range tests {
		if got, ok := Level(line); !ok || got != want {
			t.Errorf("Level(%q) = %q, %v; want %q", line, got, ok, want)
		}
	}
	for _, line := range []string{
		"GET /errors 200",
		"compiled successfully",
		"handled " + strings.Repeat("x", 90) + " ERROR far into the message",
	} {
		if got, ok := Level(line); ok {
			t.Errorf("Level(%q) = %q, want none", line, got)
		}
	}
}

func TestIDGenOrderedAndUnique(t *testing.T) {
	var g IDGen
	t0 := time.UnixMilli(1_700_000_000_000)
	var ids []string
	for i := 0; i < 1000; i++ {
		// Many IDs in the same millisecond, then one that goes backwards in time.
		ts := t0
		if i == 500 {
			ts = t0.Add(-time.Second)
		}
		if i > 900 {
			ts = t0.Add(time.Duration(i) * time.Millisecond)
		}
		ids = append(ids, g.New(ts))
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatal("IDs are not monotonically increasing")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if len(id) != 26 || strings.Trim(id, crockford) != "" {
			t.Fatalf("bad ULID %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate %q", id)
		}
		seen[id] = true
	}
	// The timestamp is encoded in the first 10 characters.
	var fresh IDGen
	if ids[0][:10] != fresh.New(t0)[:10] {
		t.Fatal("timestamp prefix mismatch")
	}
}

func TestFingerprint(t *testing.T) {
	same := [][2]string{
		{"retry 3 of 5: dial tcp 127.0.0.1:5432", "retry 4 of 5: dial tcp 127.0.0.1:5432"},
		{"2024-01-02T03:04:05Z request a1b2c3d4-e5f6-7890-abcd-ef0123456789 failed", "2024-01-02T03:04:09.123Z request 00000000-1111-2222-3333-444444444444 failed"},
		{"trace 0xdeadbeef", "trace 0xcafe"},
	}
	for _, p := range same {
		if Fingerprint(p[0]) != Fingerprint(p[1]) {
			t.Errorf("%q and %q should share a fingerprint (%q vs %q)", p[0], p[1], Normalize(p[0]), Normalize(p[1]))
		}
	}
	if Fingerprint("connection refused") == Fingerprint("connection reset") {
		t.Error("different messages must not share a fingerprint")
	}
}

// TestPatternsDocumented keeps docs/events.md in sync with the library.
func TestPatternsDocumented(t *testing.T) {
	doc, err := os.ReadFile("../../docs/events.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range Patterns {
		if !strings.Contains(string(doc), "`"+p.ID+"`") {
			t.Errorf("docs/events.md does not document %s", p.ID)
		}
	}
}
