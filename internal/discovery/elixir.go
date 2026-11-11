package discovery

import (
	"regexp"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func init() { registerAnalyzer("elixir", 100, (*scanner).analyzeElixir) }

var (
	mixAppRe      = regexp.MustCompile(`app:\s*:([a-z][a-z0-9_]*)`)
	phoenixPortRe = regexp.MustCompile(`port:\s*(\d{2,5})\b`)
	// String.to_integer(System.get_env("PORT") || "4000")
	envPortRe = regexp.MustCompile(`get_env\("PORT"\)\s*\|\|\s*"?(\d{2,5})`)
)

// mixLibs maps Hex dependencies to an infrastructure kind.
var mixLibs = []struct{ dep, kind string }{
	{"postgrex", "postgres"}, {"myxql", "mysql"}, {"redix", "redis"},
	{"mongodb_driver", "mongodb"}, {"mongodb", "mongodb"}, {"amqp", "rabbitmq"},
	{"brod", "kafka"}, {"kafka_ex", "kafka"}, {"broadway_kafka", "kafka"},
}

func mixDepRe(dep string) *regexp.Regexp {
	return regexp.MustCompile(`\{\s*:` + regexp.QuoteMeta(dep) + `\b`)
}

// analyzeElixir reads mix.exs. Phoenix is a service; a plain Plug/Bandit
// application is a lower-confidence one; anything else is a library or a CLI.
func (s *scanner) analyzeElixir(dir string) []*candidate {
	file := joinRel(dir, "mix.exs")
	raw, ok := s.readSeen(file)
	if !ok {
		return nil
	}
	name := s.dirName(dir)
	if m := mixAppRe.FindSubmatch(raw); m != nil {
		name = cleanName(string(m[1]))
	}
	c := newCandidate(dir, name, "elixir", originNative)
	c.addSource(api.Source{File: file})
	c.PackageManager = "mix"

	switch {
	case mixDepRe("phoenix").Match(raw):
		c.Framework, c.httpish = "phoenix", true
		loc := mixDepRe("phoenix").FindIndex(raw)
		c.setCommand("mix phx.server", api.ConfidenceHigh, api.Source{File: file, Line: lineAtOffset(raw, loc[0]), Note: "phoenix dependency"})
		s.phoenixPort(c, dir, file, lineAtOffset(raw, loc[0]))
	case mixDepRe("plug_cowboy").Match(raw) || mixDepRe("bandit").Match(raw):
		c.Framework, c.httpish = "plug", true
		dep := "plug_cowboy"
		if !mixDepRe(dep).Match(raw) {
			dep = "bandit"
		}
		loc := mixDepRe(dep).FindIndex(raw)
		c.setCommand("mix run --no-halt", api.ConfidenceMedium, api.Source{File: file, Line: lineAtOffset(raw, loc[0]), Note: dep + " dependency; web server started by the application"})
		c.offerPort(4000, api.ConfidenceLow, api.Source{File: file, Line: lineAtOffset(raw, loc[0]), Note: "Plug default port"})
	}
	if c.Command == nil {
		s.extraTech = append(s.extraTech, "elixir")
		return nil
	}
	for _, lib := range mixLibs {
		if loc := mixDepRe(lib.dep).FindIndex(raw); loc != nil {
			c.needKind(lib.kind, api.Source{File: file, Line: lineAtOffset(raw, loc[0]), Note: "uses " + lib.dep})
		}
	}
	return []*candidate{c}
}

// phoenixPort reads the dev endpoint's port from config/dev.exs (or
// config/runtime.exs), else offers Phoenix's default.
func (s *scanner) phoenixPort(c *candidate, dir, mixFile string, mixLine int) {
	for _, cfg := range []string{"config/dev.exs", "config/runtime.exs", "config/config.exs"} {
		rel := joinRel(dir, cfg)
		raw, ok := s.readSeen(rel)
		if !ok {
			// config/ sits one level below the project: read it even when the
			// walk didn't list it (depth limit), under the usual safety rules.
			if raw, ok = s.readOptional(rel); !ok {
				continue
			}
		}
		c.addSource(api.Source{File: rel})
		for _, re := range []*regexp.Regexp{phoenixPortRe, envPortRe} {
			if p, line := findPort(raw, re); p > 0 {
				c.offerPort(p, api.ConfidenceHigh, api.Source{File: rel, Line: line, Note: "endpoint port"})
				return
			}
		}
	}
	c.offerPort(4000, api.ConfidenceMedium, api.Source{File: mixFile, Line: mixLine, Note: "Phoenix default port"})
}
