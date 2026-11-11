package rules

import (
	"strings"
	"testing"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

type fakeRule string

func (f fakeRule) ID() string                       { return string(f) }
func (fakeRule) Evaluate(*Context) []api.Hypothesis { return nil }

func TestRegistryValidatesAndOrdersByID(t *testing.T) {
	r := &registry{rules: map[string]Rule{}}
	for _, id := range []string{"K020-zeta", "K003-alpha", "K011-mid"} {
		if err := r.add(fakeRule(id)); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, rule := range r.all() {
		got = append(got, rule.ID())
	}
	if strings.Join(got, ",") != "K003-alpha,K011-mid,K020-zeta" {
		t.Fatalf("rules must run in ID order whatever the registration order: %v", got)
	}
	for _, bad := range []string{"", "port-conflict", "K1-short", "K007", "K007-Upper", "k007-lower", "K007-two words"} {
		if err := r.add(fakeRule(bad)); err == nil || !strings.Contains(err.Error(), "must look like") {
			t.Errorf("ID %q: got %v", bad, err)
		}
	}
	if err := r.add(fakeRule("K003-alpha")); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Errorf("duplicate: %v", err)
	}
	if err := r.add(nil); err == nil {
		t.Error("nil rule")
	}
}

func TestBuiltInRulesGoThroughTheRegistry(t *testing.T) {
	want := "K001-port-conflict,K002-dependency-down,K003-crash-after-start,K004-startup-failure,K005-dependency-not-running,K006-missing-module"
	var got []string
	for _, r := range All() {
		got = append(got, r.ID())
	}
	if strings.Join(got, ",") != want {
		t.Fatalf("All() = %v", got)
	}
	// The returned slice is a copy: callers can't reorder the engine's rules.
	all := All()
	all[0] = nil
	if All()[0] == nil {
		t.Fatal("All must return a copy")
	}
	if err := Register(PortConflict{}); err == nil {
		t.Fatal("re-registering a built-in must fail")
	}
}
