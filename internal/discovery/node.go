package discovery

import (
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/kestrel-cli/kestrel/pkg/api"
)

type packageJSON struct {
	Name            string            `json:"name"`
	Main            string            `json:"main"`
	PackageManager  string            `json:"packageManager"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Proxy           string            `json:"proxy"`
}

type nodeFramework struct {
	dep, name, bin string
	port           int
}

// nodeFrameworks are checked in order; the first match wins.
var nodeFrameworks = []nodeFramework{
	{"next", "next", "next", 3000},
	{"nuxt", "nuxt", "nuxt", 3000},
	{"@remix-run/dev", "remix", "remix", 3000},
	{"astro", "astro", "astro", 4321},
	{"@sveltejs/kit", "sveltekit", "vite", 5173},
	{"@angular/core", "angular", "ng", 4200},
	{"react-scripts", "create-react-app", "react-scripts", 3000},
	{"vite", "vite", "vite", 5173},
	{"@nestjs/core", "nestjs", "nest", 3000},
	{"express", "express", "", 0},
	{"fastify", "fastify", "", 0},
	{"koa", "koa", "", 0},
	{"hono", "hono", "", 0},
}

var nodeLibs = libMap{
	"pg": "postgres", "postgres": "postgres", "pg-promise": "postgres", "@neondatabase/serverless": "postgres",
	"mysql": "mysql", "mysql2": "mysql",
	"redis": "redis", "ioredis": "redis", "@redis/client": "redis",
	"mongodb": "mongodb", "mongoose": "mongodb",
	"amqplib": "rabbitmq", "kafkajs": "kafka",
}

var (
	scriptPortRe = regexp.MustCompile(`(?:--port[ =]|-p[ =]?|PORT=)(\d{2,5})\b`)
	vitePortRe   = regexp.MustCompile(`\bport\s*:\s*(\d{2,5})\b`)
)

var nodeLockfiles = []struct{ file, pm string }{
	{"pnpm-lock.yaml", "pnpm"},
	{"yarn.lock", "yarn"},
	{"bun.lockb", "bun"},
	{"bun.lock", "bun"},
	{"package-lock.json", "npm"},
}

func (s *scanner) analyzeNode(dir string) []*candidate {
	file := joinRel(dir, "package.json")
	raw, ok := s.readSeen(file)
	if !ok {
		return nil
	}
	var pkg packageJSON
	if err := json.Unmarshal(raw, &pkg); err != nil {
		s.warnf("could not parse %s: %v", file, err)
		return nil
	}
	allDeps := map[string]bool{}
	for d := range pkg.Dependencies {
		allDeps[d] = true
	}
	for d := range pkg.DevDependencies {
		allDeps[d] = true
	}

	name := cleanName(pkg.Name)
	if name == "" {
		name = s.dirName(dir)
	}
	c := newCandidate(dir, name, "node", originNative)
	c.addSource(api.Source{File: file})
	c.PackageManager = s.nodePackageManager(dir, pkg)

	var fw *nodeFramework
	for i := range nodeFrameworks {
		if allDeps[nodeFrameworks[i].dep] {
			fw = &nodeFrameworks[i]
			break
		}
	}
	if fw != nil {
		c.Framework = fw.name
		c.httpish = true
	}

	// Start command: the first conventional script that exists.
	var script, scriptBody string
	for _, k := range []string{"dev", "start", "serve", "develop"} {
		if body, ok := pkg.Scripts[k]; ok {
			script, scriptBody = k, body
			break
		}
	}
	switch {
	case script != "":
		src := api.Source{File: file, Line: lineOf(raw, `"`+script+`"`), Note: "script: " + scriptBody}
		c.setCommand(nodeRun(c.PackageManager, script), api.ConfidenceHigh, src)
		if p, _ := findPort([]byte(scriptBody), scriptPortRe); p > 0 {
			c.offerPort(p, api.ConfidenceHigh, api.Source{File: file, Line: src.Line, Note: "port flag in \"" + script + "\" script"})
		}
	case pkg.Main != "" && s.has(joinRel(dir, path.Clean(pkg.Main))):
		c.setCommand("node "+path.Clean(pkg.Main), api.ConfidenceLow, api.Source{File: file, Line: lineOf(raw, `"main"`), Note: "main entry; no start script"})
	case s.has(joinRel(dir, "server.js")):
		c.setCommand("node server.js", api.ConfidenceLow, api.Source{File: joinRel(dir, "server.js"), Note: "conventional entry file; no start script"})
	}
	if c.Command == nil && fw == nil {
		// A library package: worth noting as a technology, not a service.
		s.extraTech = append(s.extraTech, "node")
		return nil
	}

	// Framework config files can pin the port and declare proxies.
	for _, cfg := range s.files[dir] {
		if !isNodeFrameworkConfig(cfg) {
			continue
		}
		rel := joinRel(dir, cfg)
		content, ok := s.readSeen(rel)
		if !ok {
			continue
		}
		c.addSource(api.Source{File: rel})
		if p, line := findPort(content, vitePortRe); p > 0 {
			c.offerPort(p, api.ConfidenceHigh, api.Source{File: rel, Line: line, Note: "dev server port"})
		}
		c.needLocalhostURLs(content, rel)
	}
	if fw != nil && fw.port > 0 {
		conf := api.ConfidenceLow
		if fw.bin != "" && strings.Contains(scriptBody, fw.bin) {
			conf = api.ConfidenceMedium
		}
		c.offerPort(fw.port, conf, api.Source{File: file, Line: lineOf(raw, `"`+fw.dep+`"`), Note: fw.name + " default port"})
	}
	if pkg.Proxy != "" {
		c.needLocalhostURLs([]byte(pkg.Proxy), file)
		for i := range c.needs {
			if c.needs[i].source.File == file {
				c.needs[i].source.Line = lineOf(raw, `"proxy"`)
			}
		}
	}

	deps := make([]string, 0, len(allDeps))
	for d := range allDeps {
		deps = append(deps, d)
	}
	sort.Strings(deps)
	for _, d := range deps {
		if kind, ok := nodeLibs.lookup(d); ok {
			c.needKind(kind, api.Source{File: file, Line: lineOf(raw, `"`+d+`"`), Note: "uses " + d})
		}
	}
	return []*candidate{c}
}

func isNodeFrameworkConfig(name string) bool {
	for _, prefix := range []string{"vite.config.", "next.config.", "nuxt.config.", "astro.config.", "svelte.config.", "webpack.config."} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func (s *scanner) nodePackageManager(dir string, pkg packageJSON) string {
	if pm, _, ok := strings.Cut(pkg.PackageManager, "@"); ok && pm != "" {
		return pm
	}
	// Workspaces keep the lockfile at the workspace root.
	for d := dir; ; d = path.Dir(d) {
		for _, lf := range nodeLockfiles {
			if s.has(joinRel(d, lf.file)) {
				return lf.pm
			}
		}
		if d == "." {
			break
		}
	}
	return "npm"
}

func nodeRun(pm, script string) string {
	switch pm {
	case "npm":
		if script == "start" {
			return "npm start"
		}
		return "npm run " + script
	case "bun":
		return "bun run " + script
	default: // pnpm, yarn
		return pm + " " + script
	}
}
