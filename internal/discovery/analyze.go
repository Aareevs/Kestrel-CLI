package discovery

import (
	"fmt"
	"path"
	"sort"
	"strconv"

	"github.com/kestrel-cli/kestrel/pkg/api"
)

// analyze runs every analyzer over the scanned directories and then
// reconciles the results.
func (s *scanner) analyze() []api.ServiceCandidate {
	var cands []*candidate
	for _, dir := range s.sortedDirs() {
		cands = append(cands, s.analyzeNode(dir)...)
		cands = append(cands, s.analyzeGo(dir)...)
		cands = append(cands, s.analyzePython(dir)...)
		cands = append(cands, s.analyzeJVM(dir)...)
		cands = append(cands, s.analyzePHP(dir)...)
		cands = append(cands, s.analyzeRuby(dir)...)
		cands = append(cands, s.analyzeRust(dir)...)
		var composeNames []string
		for _, n := range s.files[dir] {
			if composeFiles[n] {
				composeNames = append(composeNames, n)
			}
		}
		sort.Strings(composeNames)
		for _, n := range composeNames {
			cands = append(cands, s.analyzeCompose(dir, n)...)
		}
	}

	cands = mergeCompose(cands)
	cands = s.applyDockerfiles(cands)
	s.applyEnvExamples(cands)
	uniqueNames(cands)
	resolveNeeds(cands)

	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].dir != cands[j].dir {
			return cands[i].dir < cands[j].dir
		}
		return cands[i].Name < cands[j].Name
	})
	out := make([]api.ServiceCandidate, 0, len(cands))
	for _, c := range cands {
		c.Health = suggestHealth(c)
		c.Confidence = overallConfidence(c)
		if c.Sources == nil {
			c.Sources = []api.Source{}
		}
		out = append(out, c.ServiceCandidate)
	}
	return out
}

// mergeCompose folds a compose service into the native candidate it builds,
// so one service is not reported twice. The compose command stays primary
// because the project declares it; the native command becomes an alternative.
func mergeCompose(cands []*candidate) []*candidate {
	drop := map[*candidate]bool{}
	for _, cc := range cands {
		if cc.origin != originCompose || cc.buildDir == "" {
			continue
		}
		var natives []*candidate
		for _, n := range cands {
			if n.origin == originNative && n.dir == cc.buildDir {
				natives = append(natives, n)
			}
		}
		if len(natives) != 1 {
			continue
		}
		n := natives[0]
		if n.Command != nil {
			n.Alternatives = append(n.Alternatives, *n.Command)
		}
		n.Command = cc.Command
		n.Name = cc.Name
		if cc.Port != nil {
			n.Port = cc.Port
		}
		n.needs = append(cc.needs, n.needs...)
		n.EnvKeys = append(n.EnvKeys, cc.EnvKeys...)
		n.Sources = append(cc.Sources, n.Sources...)
		n.origin = originCompose
		n.buildDir = cc.buildDir
		drop[cc] = true
	}
	out := cands[:0]
	for _, c := range cands {
		if !drop[c] {
			out = append(out, c)
		}
	}
	return out
}

// uniqueNames makes candidate names unique. Compose services keep their
// declared names because depends_on refers to them.
func uniqueNames(cands []*candidate) {
	order := make([]*candidate, 0, len(cands))
	for _, c := range cands {
		if c.origin == originCompose {
			order = append(order, c)
		}
	}
	for _, c := range cands {
		if c.origin != originCompose {
			order = append(order, c)
		}
	}
	used := map[string]bool{}
	for _, c := range order {
		name := c.Name
		if name == "" {
			name = "service"
		}
		if used[name] {
			if alt := cleanName(name + "-" + path.Base(c.dir)); c.dir != "." && alt != name && !used[alt] {
				name = alt
			} else {
				for i := 2; used[name]; i++ {
					name = fmt.Sprintf("%s-%d", c.Name, i)
				}
			}
		}
		used[name] = true
		c.Name = name
	}
}

// resolveNeeds turns dependency hints into named dependencies, pointing at
// other candidates where the evidence is unambiguous. It never adds an edge
// just because two things exist side by side.
func resolveNeeds(cands []*candidate) {
	byName := map[string]*candidate{}
	byInfra := map[string][]*candidate{}
	byPort := map[int][]*candidate{}
	for _, c := range cands {
		byName[c.Name] = c
		if c.infra != "" {
			byInfra[c.infra] = append(byInfra[c.infra], c)
		}
		if c.Port != nil {
			byPort[c.Port.Value] = append(byPort[c.Port.Value], c)
		}
	}
	for _, c := range cands {
		needs := append([]need(nil), c.needs...)
		sort.SliceStable(needs, func(i, j int) bool { return needs[i].declared && !needs[j].declared })
		seen := map[string]bool{}
		var deps []api.Dependency
		add := func(d api.Dependency) {
			if d.Name == c.Name || seen[d.Name] {
				return
			}
			seen[d.Name] = true
			deps = append(deps, d)
		}
		for _, n := range needs {
			switch {
			case n.declared:
				_, ok := byName[n.name]
				add(api.Dependency{Name: n.name, Kind: api.DependencyDeclared, Resolved: ok, Source: n.source})
			case n.kind != "":
				if m := byInfra[n.kind]; len(m) == 1 {
					add(api.Dependency{Name: m[0].Name, Kind: api.DependencyInferred, Resolved: true, Source: n.source})
				} else {
					add(api.Dependency{Name: n.kind, Kind: api.DependencyInferred, Source: n.source})
				}
			case n.port > 0:
				m := byPort[n.port]
				if len(m) == 1 && m[0] == c {
					continue
				}
				if len(m) == 1 {
					add(api.Dependency{Name: m[0].Name, Kind: api.DependencyInferred, Resolved: true, Source: n.source})
				} else {
					add(api.Dependency{Name: "localhost:" + strconv.Itoa(n.port), Kind: api.DependencyInferred, Source: n.source})
				}
			}
		}
		c.DependsOn = deps
	}
}

func suggestHealth(c *candidate) *api.HealthGuess {
	if c.Port == nil {
		return nil
	}
	conf := c.Port.Confidence
	port := strconv.Itoa(c.Port.Value)
	switch {
	case c.infra != "":
		return &api.HealthGuess{Type: "tcp", Target: "localhost:" + port, Confidence: conf, Reason: "TCP connect to the " + c.infra + " port"}
	case c.httpish && c.healthPath != "":
		return &api.HealthGuess{Type: "http", Target: "http://localhost:" + port + c.healthPath, Confidence: conf,
			Reason: fmt.Sprintf("health route %q found in %s:%d", c.healthPath, c.healthPathFrom.File, c.healthPathFrom.Line)}
	case c.httpish:
		if conf == api.ConfidenceHigh {
			conf = api.ConfidenceMedium
		}
		return &api.HealthGuess{Type: "http", Target: "http://localhost:" + port + "/", Confidence: conf, Reason: "no health endpoint detected; probes the root path"}
	default:
		return &api.HealthGuess{Type: "tcp", Target: "localhost:" + port, Confidence: conf, Reason: "no HTTP framework detected; TCP connect is the safest check"}
	}
}

// overallConfidence: high needs a high-confidence command and at least a
// medium-confidence port; medium needs a medium-or-better command.
func overallConfidence(c *candidate) api.Confidence {
	cmd, port := 0, 0
	if c.Command != nil {
		cmd = c.Command.Confidence.Rank()
	}
	if c.Port != nil {
		port = c.Port.Confidence.Rank()
	}
	switch {
	case cmd == 3 && port >= 2:
		return api.ConfidenceHigh
	case cmd >= 2:
		return api.ConfidenceMedium
	default:
		return api.ConfidenceLow
	}
}
