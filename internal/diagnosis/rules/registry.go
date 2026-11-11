package rules

import (
	"fmt"
	"regexp"
	"sort"
	"sync"
)

// idRe is the rule ID scheme: K, three digits, then a short name.
var idRe = regexp.MustCompile(`^K\d{3}-[a-z0-9]+(?:-[a-z0-9]+)*$`)

// registry holds rules by ID. The package-level one is std.
type registry struct {
	mu    sync.RWMutex
	rules map[string]Rule
}

var std = &registry{rules: map[string]Rule{}}

func (r *registry) add(rule Rule) error {
	if rule == nil {
		return fmt.Errorf("rules: cannot register a nil rule")
	}
	id := rule.ID()
	if !idRe.MatchString(id) {
		return fmt.Errorf("rules: rule ID %q must look like K007-short-name", id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.rules[id]; dup {
		return fmt.Errorf("rules: rule %s is already registered", id)
	}
	r.rules[id] = rule
	return nil
}

func (r *registry) all() []Rule {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Rule, 0, len(r.rules))
	for _, rule := range r.rules {
		out = append(out, rule)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// Register adds a rule to the engine, normally from init(). The ID must follow
// the scheme K007-short-name and be unique. Rules always run in ID order, so a
// rule's place never depends on registration order.
//
// A rule is only half of the contract: docs/diagnosis-rules.md must document
// it, and every hypothesis it proposes must cite supporting events (the engine
// drops any that don't). Both are enforced by tests.
func Register(rule Rule) error { return std.add(rule) }

// MustRegister is Register for init(): it panics on an invalid or duplicate
// rule, which is a programming error.
func MustRegister(rule Rule) {
	if err := Register(rule); err != nil {
		panic(err)
	}
}

func init() {
	for _, r := range []Rule{PortConflict{}, DependencyDown{}, CrashAfterStart{}, StartupFailure{}, DependencyNotRunning{}, MissingModule{}} {
		MustRegister(r)
	}
}
