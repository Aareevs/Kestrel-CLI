package discovery

import (
	"strings"
	"testing"
)

func noop(*scanner, string) []*candidate { return nil }

func TestAnalyzerRegistryValidatesAndOrders(t *testing.T) {
	var list []analyzer
	for _, a := range []struct {
		name  string
		order int
	}{{"late", 90}, {"b-tie", 10}, {"a-tie", 10}, {"early", 1}} {
		if err := addAnalyzer(&list, a.name, a.order, noop); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, a := range sortedAnalyzers(list) {
		got = append(got, a.name)
	}
	if strings.Join(got, ",") != "early,a-tie,b-tie,late" {
		t.Fatalf("order = %v (ties break by name, never by registration order)", got)
	}
	for _, bad := range []string{"", "Go", "two words", "-lead", "1abc"} {
		if err := addAnalyzer(&list, bad, 1, noop); err == nil {
			t.Errorf("name %q should be rejected", bad)
		}
	}
	if err := addAnalyzer(&list, "early", 5, noop); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Errorf("duplicate: %v", err)
	}
	if err := addAnalyzer(&list, "nofunc", 5, nil); err == nil {
		t.Error("nil function")
	}
}

// The built-ins keep the order they always ran in, so existing candidates and
// the numbered suffixes of same-named ones don't change.
func TestBuiltInAnalyzersRunInTheirOriginalOrder(t *testing.T) {
	var got []string
	for _, a := range registeredAnalyzers() {
		got = append(got, a.name)
	}
	if strings.Join(got, ",") != "node,go,python,jvm,php,ruby,rust" {
		t.Fatalf("built-in order = %v", got)
	}
}
