package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Aareevs/Kestrel-CLI/internal/events"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Limits on custom patterns. Go's regexp (RE2) is linear-time, so a pattern
// can't hang Kestrel, but a config should stay readable.
const (
	MaxCustomPatterns = 50
	maxRegexLen       = 1000
	maxExamples       = 20
	maxExampleLen     = 1000
)

// CustomPattern teaches Kestrel one error shape that is specific to your
// project. It carries its own evidence: lines it must match and lines it must
// not, and `kestrel config validate` checks them.
type CustomPattern struct {
	// ID is U001-short-name: U, three digits, then lowercase words.
	ID          string `yaml:"id" json:"id"`
	Description string `yaml:"description" json:"description"`
	// Type is log_line, dependency_conn_failed, port_bind_failed or test_failed.
	Type     string `yaml:"type" json:"type"`
	Severity string `yaml:"severity" json:"severity"`
	// Regex is a Go (RE2) regular expression. Named groups (?P<port>…) and
	// (?P<target>…) become the event's port and target, which is how a
	// dependency_conn_failed line links services in the dependency graph.
	Regex   string   `yaml:"regex" json:"regex"`
	Match   []string `yaml:"match" json:"match"`
	NoMatch []string `yaml:"no_match" json:"no_match"`
}

var customIDRe = regexp.MustCompile(`^U\d{3}-[a-z0-9]+(?:-[a-z0-9]+)*$`)

// customTypes are the event types a custom pattern may produce. Lifecycle
// and probe events belong to Kestrel itself.
var customTypes = map[string]api.EventType{
	"log_line":               api.EventLogLine,
	"dependency_conn_failed": api.EventDependencyConnFailed,
	"port_bind_failed":       api.EventPortBindFailed,
	"test_failed":            api.EventTestFailed,
}

var customSeverities = map[string]api.Severity{
	"debug": api.SeverityDebug, "info": api.SeverityInfo, "warn": api.SeverityWarn,
	"error": api.SeverityError, "fatal": api.SeverityFatal,
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// toPattern converts and checks one custom pattern on its own. The returned
// error says which field is wrong (the first word is the field name).
func (p CustomPattern) toPattern() (events.Pattern, string, error) {
	if !customIDRe.MatchString(p.ID) {
		return events.Pattern{}, "id", fmt.Errorf("%q must look like U001-short-name (U, three digits, lowercase words)", p.ID)
	}
	typ, ok := customTypes[p.Type]
	if !ok {
		return events.Pattern{}, "type", fmt.Errorf("unknown type %q (use %s)", p.Type, strings.Join(sortedKeys(customTypes), ", "))
	}
	sev, ok := customSeverities[p.Severity]
	if !ok {
		return events.Pattern{}, "severity", fmt.Errorf("unknown severity %q (use %s)", p.Severity, strings.Join(sortedKeys(customSeverities), ", "))
	}
	if strings.TrimSpace(p.Description) == "" {
		return events.Pattern{}, "description", fmt.Errorf("required: say what this error means")
	}
	if p.Regex == "" {
		return events.Pattern{}, "regex", fmt.Errorf("required")
	}
	if len(p.Regex) > maxRegexLen {
		return events.Pattern{}, "regex", fmt.Errorf("is %d characters long (the limit is %d)", len(p.Regex), maxRegexLen)
	}
	re, err := regexp.Compile(p.Regex)
	if err != nil {
		return events.Pattern{}, "regex", fmt.Errorf("invalid regular expression: %v", err)
	}
	for field, ex := range map[string][]string{"match": p.Match, "no_match": p.NoMatch} {
		if len(ex) > maxExamples {
			return events.Pattern{}, field, fmt.Errorf("has %d examples (the limit is %d)", len(ex), maxExamples)
		}
		for _, line := range ex {
			if len(line) > maxExampleLen {
				return events.Pattern{}, field, fmt.Errorf("has an example longer than %d characters", maxExampleLen)
			}
		}
	}
	return events.Pattern{ID: p.ID, Description: p.Description, Type: typ, Severity: sev, Re: re, Match: p.Match, NoMatch: p.NoMatch}, "", nil
}

// patterns validates c.Patterns: each on its own, then against the library
// built so far, so a pattern shadowed by a built-in (or by an earlier custom
// one) is reported on the pattern that would never run.
func (v *validator) patterns(c *Config) {
	if len(c.Patterns) > MaxCustomPatterns {
		v.add([]any{"patterns"}, "patterns", "has %d patterns (the limit is %d)", len(c.Patterns), MaxCustomPatterns)
		return
	}
	lib := events.DefaultLibrary()
	seen := map[string]int{}
	for i, p := range c.Patterns {
		at := []any{"patterns", i}
		label := fmt.Sprintf("patterns[%d]", i)
		pat, field, err := p.toPattern()
		if err != nil {
			v.add(append(append([]any{}, at...), field), label+"."+field, "%v", err)
			continue
		}
		if first, dup := seen[p.ID]; dup {
			v.add(append(append([]any{}, at...), "id"), label+".id", "duplicate pattern ID %s (first used by patterns[%d])", p.ID, first)
			continue
		}
		seen[p.ID] = i
		if len(p.Match) == 0 {
			v.add(append(append([]any{}, at...), "match"), label+".match", "needs at least one line the pattern must match")
			continue
		}
		if len(p.NoMatch) == 0 {
			v.add(append(append([]any{}, at...), "no_match"), label+".no_match", "needs at least one similar line the pattern must not match")
			continue
		}
		next, err := lib.With(pat)
		if err != nil {
			v.add(at, label, "%v", err)
			continue
		}
		lib = next
	}
}

// PatternLibrary returns the patterns this config adds to Kestrel's built-in
// ones: nil (use the default) when it defines none. Call it on a validated
// config; an invalid pattern is reported as an error.
func (c *Config) PatternLibrary() (*events.Library, error) {
	if len(c.Patterns) == 0 {
		return nil, nil
	}
	lib := events.DefaultLibrary()
	for _, p := range c.Patterns {
		pat, _, err := p.toPattern()
		if err != nil {
			return nil, fmt.Errorf("pattern %s: %w", p.ID, err)
		}
		if lib, err = lib.With(pat); err != nil {
			return nil, err
		}
	}
	return lib, nil
}
