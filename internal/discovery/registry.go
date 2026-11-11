package discovery

import (
	"fmt"
	"regexp"
	"sort"
)

// An analyzer looks at one scanned directory and returns the service
// candidates its ecosystem finds there (Node, Go, Python, …). Analyzers only
// read what the scanner already collected; they never run anything.
//
// To add an ecosystem, write the analyzer in its own file and register it from
// init():
//
//	func init() { registerAnalyzer("elixir", 80, (*scanner).analyzeElixir) }
//
// See docs/testing.md ("Adding a discovery fixture") for the tests it needs.
type analyzer struct {
	name  string
	order int
	run   func(s *scanner, dir string) []*candidate
}

// Built-in analyzers are spaced 10 apart; a new analyzer without a strong
// reason to run earlier takes an order after them.
const (
	orderNode   = 10
	orderGo     = 20
	orderPython = 30
	orderJVM    = 40
	orderPHP    = 50
	orderRuby   = 60
	orderRust   = 70
)

var (
	analyzers  []analyzer
	analyzerRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// registerAnalyzer adds an analyzer. It is meant for init(), and panics on an
// invalid or duplicate registration (a programming error).
//
// Analyzers run in (order, name) order, never in registration order, so the
// result doesn't depend on file names. The order is observable: candidates
// that share a name get numbered suffixes in that order.
func registerAnalyzer(name string, order int, run func(s *scanner, dir string) []*candidate) {
	if err := addAnalyzer(&analyzers, name, order, run); err != nil {
		panic(err)
	}
}

func addAnalyzer(list *[]analyzer, name string, order int, run func(s *scanner, dir string) []*candidate) error {
	if !analyzerRe.MatchString(name) {
		return fmt.Errorf("discovery: analyzer name %q must be lowercase letters, digits and dashes", name)
	}
	if run == nil {
		return fmt.Errorf("discovery: analyzer %s has no function", name)
	}
	for _, a := range *list {
		if a.name == name {
			return fmt.Errorf("discovery: analyzer %s is already registered", name)
		}
	}
	*list = append(*list, analyzer{name: name, order: order, run: run})
	return nil
}

// registeredAnalyzers returns the analyzers in running order.
func registeredAnalyzers() []analyzer { return sortedAnalyzers(analyzers) }

func sortedAnalyzers(list []analyzer) []analyzer {
	out := append([]analyzer(nil), list...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].order != out[j].order {
			return out[i].order < out[j].order
		}
		return out[i].name < out[j].name
	})
	return out
}

func init() {
	registerAnalyzer("node", orderNode, (*scanner).analyzeNode)
	registerAnalyzer("go", orderGo, (*scanner).analyzeGo)
	registerAnalyzer("python", orderPython, (*scanner).analyzePython)
	registerAnalyzer("jvm", orderJVM, (*scanner).analyzeJVM)
	registerAnalyzer("php", orderPHP, (*scanner).analyzePHP)
	registerAnalyzer("ruby", orderRuby, (*scanner).analyzeRuby)
	registerAnalyzer("rust", orderRust, (*scanner).analyzeRust)
}
