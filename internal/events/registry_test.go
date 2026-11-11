package events

import (
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func sample(id, re string) Pattern {
	return Pattern{
		ID:          id,
		Description: "a test pattern",
		Type:        api.EventLogLine,
		Severity:    api.SeverityError,
		Re:          regexp.MustCompile(re),
		Match:       []string{"boom happened"},
		NoMatch:     []string{"all fine"},
	}
}

func TestRegisterValidatesEveryPattern(t *testing.T) {
	good := sample("P900-boom", `boom`)
	tests := []struct {
		name string
		edit func(*Pattern)
		want string
	}{
		{"bad ID", func(p *Pattern) { p.ID = "boom" }, "must look like"},
		{"uppercase name", func(p *Pattern) { p.ID = "P900-Boom" }, "must look like"},
		{"no description", func(p *Pattern) { p.Description = " " }, "needs a description"},
		{"no type", func(p *Pattern) { p.Type = "" }, "type and a severity"},
		{"no severity", func(p *Pattern) { p.Severity = "" }, "type and a severity"},
		{"no regex", func(p *Pattern) { p.Re = nil }, "regular expression"},
		{"negative priority", func(p *Pattern) { p.Priority = -1 }, "negative priority"},
		{"no Match example", func(p *Pattern) { p.Match = nil }, "at least one Match"},
		{"no NoMatch example", func(p *Pattern) { p.NoMatch = nil }, "at least one Match"},
		{"Match example it doesn't match", func(p *Pattern) { p.Match = []string{"nothing here"} }, "should match"},
		{"NoMatch example it matches", func(p *Pattern) { p.NoMatch = []string{"a boom"} }, "should not match"},
		{"unknown capture group", func(p *Pattern) { p.Re = regexp.MustCompile(`(?P<host>boom)`) }, "not a known attribute"},
	}
	for _, tt := range tests {
		p := good
		tt.edit(&p)
		if _, err := withPattern(empty, p); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: got %v, want an error containing %q", tt.name, err, tt.want)
		}
	}
	set, err := withPattern(empty, good)
	if err != nil || set.byID["P900-boom"] == nil || len(set.list) != 1 {
		t.Fatalf("a valid pattern should register: %v", err)
	}
	if _, err := withPattern(set, good); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate ID: %v", err)
	}
}

func TestRegisterOrdersByPriorityThenRegistration(t *testing.T) {
	set := empty
	add := func(id string, prio int) {
		p := sample(id, `boom`)
		p.Priority = prio
		var err error
		if set, err = withPattern(set, p); err != nil {
			t.Fatal(err)
		}
	}
	add("P901-first-default", 0)
	add("P902-second-default", 0)
	add("P903-urgent", 5)
	add("P904-between", PriorityStep+50)
	add("P905-also-urgent", 5)
	var got []string
	for _, p := range set.list {
		got = append(got, p.ID)
	}
	want := "P903-urgent,P905-also-urgent,P904-between,P901-first-default,P902-second-default"
	if strings.Join(got, ",") != want {
		t.Fatalf("order = %v\nwant    %s", got, want)
	}
	// withPattern never mutates the set it was given.
	if len(empty.list) != 0 {
		t.Fatal("the shared empty set was modified")
	}
}

func TestBuiltinsAreRegisteredInOrderAndNewPatternsGoAfter(t *testing.T) {
	all := Patterns()
	if len(all) != len(builtinPatterns) || len(all) == 0 {
		t.Fatalf("%d registered, %d built in", len(all), len(builtinPatterns))
	}
	for i, p := range all {
		if p.ID != builtinPatterns[i].ID || p.Priority != (i+1)*PriorityStep {
			t.Fatalf("pattern %d is %s (priority %d), want %s", i, p.ID, p.Priority, builtinPatterns[i].ID)
		}
	}
	// A pattern registered without a priority runs after every built-in.
	set, err := withPattern(load(), sample("P906-late", `boom`))
	if err != nil || set.list[len(set.list)-1].ID != "P906-late" {
		t.Fatalf("late pattern placement: %v", err)
	}
}

func TestRegisterGlobalRejectsDuplicatesWithoutChangingAnything(t *testing.T) {
	before := len(Patterns())
	dup := *PatternByID("P001-port-in-use")
	if err := Register(dup); err == nil {
		t.Fatal("registering an existing ID must fail")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("MustRegister must panic on an invalid pattern")
			}
		}()
		MustRegister(Pattern{ID: "nope"})
	}()
	if len(Patterns()) != before {
		t.Fatal("a failed registration changed the library")
	}
}

// Classify reads the library without a lock while Register stores new sets:
// that must be race-free (go test -race), and no registration may be lost.
func TestClassifyWhileRegistering(t *testing.T) {
	saved := current.Load()
	t.Cleanup(func() { current.Store(saved) })

	const writers, perWriter = 4, 25
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if Classify("dial tcp 127.0.0.1:5432: connect: connection refused").PatternID != "P006-connection-refused" {
					t.Error("lost a pattern while registering")
				}
			}
		}()
		go func(w int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				p := sample("P"+strings.Repeat(string(rune('0'+w)), 3)+"-race-"+strings.Repeat("x", j+1), `zzz-never-`+strings.Repeat("x", j+1))
				p.Match = []string{"zzz-never-" + strings.Repeat("x", j+1)}
				if err := Register(p); err != nil {
					t.Error(err)
				}
			}
		}(w)
	}
	wg.Wait()
	if got := len(Patterns()); got != len(builtinPatterns)+writers*perWriter {
		t.Fatalf("%d patterns registered, want %d (a registration was lost)", got, len(builtinPatterns)+writers*perWriter)
	}
}
