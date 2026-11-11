package events

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Priorities order patterns. The first matching pattern wins.
const (
	// PriorityStep is the spacing between built-in patterns (100, 200, …), so
	// a new pattern can be placed between two of them.
	PriorityStep = 100
	// PriorityDefault is where a pattern with no Priority goes: after every
	// built-in pattern.
	PriorityDefault = 100_000
)

// idRe is the pattern ID scheme: a letter and three digits, then a short name.
// P… recognizes an error shape, L… recognizes an informational line.
var idRe = regexp.MustCompile(`^[A-Z]\d{3}-[a-z0-9]+(?:-[a-z0-9]+)*$`)

// patternSet is an immutable, ordered set of patterns. Classify reads the
// current set without locking, and Register replaces it.
type patternSet struct {
	list []Pattern
	byID map[string]*Pattern
}

var (
	current atomic.Pointer[patternSet]
	regMu   sync.Mutex // serializes writers
)

var empty = &patternSet{byID: map[string]*Pattern{}}

// load returns the current set. It is safe to call from any init(): before
// the first registration there is simply nothing in it.
func load() *patternSet {
	if set := current.Load(); set != nil {
		return set
	}
	return empty
}

// Patterns returns every registered pattern in match order. The result is
// shared: do not modify it.
func Patterns() []Pattern { return load().list }

// PatternByID returns the pattern with id, or nil. IDs are referenced from
// diagnosis rules and docs, so they are stable.
func PatternByID(id string) *Pattern { return load().byID[id] }

// Register adds a pattern to the library. Packages do it from init(), which
// is how the built-in patterns are registered too.
//
// A pattern must carry its own evidence: a description, a type and severity,
// a regex whose named groups are port* or target*, at least one line it
// matches and one it doesn't. Register checks that the regex really matches
// every Match line and none of the NoMatch lines. IDs are unique and follow
// the scheme P001-short-name.
//
// Order matters because the first match wins. Patterns are sorted by Priority
// (zero means PriorityDefault); equal priorities keep registration order,
// which follows file names within a package. Give a distinct Priority when a
// pattern must run before another. The test suite also checks that no
// pattern's examples are claimed by an earlier one.
func Register(p Pattern) error {
	regMu.Lock()
	defer regMu.Unlock()
	next, err := withPattern(load(), p)
	if err != nil {
		return err
	}
	current.Store(next)
	return nil
}

// MustRegister is Register for init(): it panics on an invalid pattern, which
// is a programming error caught the first time the program (or its tests) runs.
func MustRegister(p Pattern) {
	if err := Register(p); err != nil {
		panic("events: " + err.Error())
	}
}

// withPattern returns a copy of set with p added, or an error. It doesn't
// touch the global state, so it can be tested in isolation.
func withPattern(set *patternSet, p Pattern) (*patternSet, error) {
	if err := validate(p); err != nil {
		return nil, err
	}
	if set.byID[p.ID] != nil {
		return nil, fmt.Errorf("pattern %s is already registered", p.ID)
	}
	if p.Priority == 0 {
		p.Priority = PriorityDefault
	}
	list := make([]Pattern, 0, len(set.list)+1)
	list = append(list, set.list...)
	list = append(list, p)
	sort.SliceStable(list, func(i, j int) bool { return list[i].Priority < list[j].Priority })
	byID := make(map[string]*Pattern, len(list))
	for i := range list {
		cp := list[i]
		byID[cp.ID] = &cp
	}
	return &patternSet{list: list, byID: byID}, nil
}

func validate(p Pattern) error {
	switch {
	case !idRe.MatchString(p.ID):
		return fmt.Errorf("pattern ID %q must look like P015-short-name", p.ID)
	case strings.TrimSpace(p.Description) == "":
		return fmt.Errorf("pattern %s needs a description", p.ID)
	case p.Type == "" || p.Severity == "":
		return fmt.Errorf("pattern %s needs a type and a severity", p.ID)
	case p.Re == nil:
		return fmt.Errorf("pattern %s needs a regular expression", p.ID)
	case p.Priority < 0:
		return fmt.Errorf("pattern %s has a negative priority", p.ID)
	case len(p.Match) == 0 || len(p.NoMatch) == 0:
		return fmt.Errorf("pattern %s needs at least one Match and one NoMatch example", p.ID)
	}
	for _, name := range p.Re.SubexpNames()[1:] {
		if name != "" && !strings.HasPrefix(name, "port") && !strings.HasPrefix(name, "target") {
			return fmt.Errorf("pattern %s: capture group %q is not a known attribute (port*, target*)", p.ID, name)
		}
	}
	for _, line := range p.Match {
		if !p.Re.MatchString(line) {
			return fmt.Errorf("pattern %s should match %q but doesn't", p.ID, line)
		}
	}
	for _, line := range p.NoMatch {
		if p.Re.MatchString(line) {
			return fmt.Errorf("pattern %s should not match %q but does", p.ID, line)
		}
	}
	return nil
}

// A Library is an immutable set of patterns to classify lines with. The
// default library is every registered pattern. A project's library is that
// plus the patterns in its kestrel.yaml, which apply to that project only.
type Library struct{ set *patternSet }

// DefaultLibrary is the registered patterns (what Classify uses).
func DefaultLibrary() *Library { return &Library{set: load()} }

// Classify assigns a type, severity and attributes to one log line.
func (l *Library) Classify(line string) Classification {
	if l == nil {
		return Classify(line)
	}
	return classify(l.set, line)
}

// Patterns returns the library's patterns in match order (shared; read-only).
func (l *Library) Patterns() []Pattern {
	if l == nil {
		return Patterns()
	}
	return l.set.list
}

// With returns a library with extra patterns added, or an error naming the
// first problem. The receiver isn't changed.
//
// Extra patterns run after the library's existing ones, so they can recognize
// shapes the built-ins don't, but never take a line a built-in already claims
// (diagnosis rules depend on the built-in IDs). To keep that honest, With
// rejects a pattern whose own Match examples are claimed by an earlier
// pattern, since it would never run on them.
func (l *Library) With(extra ...Pattern) (*Library, error) {
	set := l.base()
	for _, p := range extra {
		next, err := withPattern(set, p)
		if err != nil {
			return nil, err
		}
		for _, line := range p.Match {
			if got := classify(next, line).PatternID; got != p.ID {
				return nil, fmt.Errorf("pattern %s: %q is already recognized by %s, so this pattern would never run on it (custom patterns match what the built-ins don't)", p.ID, line, got)
			}
		}
		set = next
	}
	return &Library{set: set}, nil
}

func (l *Library) base() *patternSet {
	if l == nil {
		return load()
	}
	return l.set
}
