package discovery

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func init() { registerAnalyzer("deno", 80, (*scanner).analyzeDeno) }

type denoConfig struct {
	Name    string            `json:"name"`
	Tasks   map[string]string `json:"tasks"`
	Imports map[string]string `json:"imports"`
}

var (
	denoServeRe = regexp.MustCompile(`Deno\.serve\(`)
	denoPortRe  = regexp.MustCompile(`\bport\s*:\s*(\d{2,5})\b`)
)

// denoLibs maps substrings of an import specifier to an infrastructure kind.
var denoLibs = []struct{ marker, kind string }{
	{"postgres", "postgres"}, {"npm:pg", "postgres"}, {"mysql", "mysql"},
	{"redis", "redis"}, {"mongo", "mongodb"}, {"amqp", "rabbitmq"}, {"kafka", "kafka"},
}

var denoFrameworks = []struct{ marker, name string }{
	{"fresh", "fresh"}, {"oak", "oak"}, {"hono", "hono"}, {"fastify", "fastify"}, {"express", "express"},
}

// denoEntries are conventional server entry files, in order of preference.
var denoEntries = []string{"main.ts", "server.ts", "mod.ts", "main.js", "server.js", "src/main.ts", "src/server.ts"}

// analyzeDeno reads deno.json / deno.jsonc. A directory that also has a
// package.json belongs to the Node analyzer, so it isn't reported twice.
func (s *scanner) analyzeDeno(dir string) []*candidate {
	file := joinRel(dir, "deno.json")
	raw, ok := s.readSeen(file)
	if !ok {
		file = joinRel(dir, "deno.jsonc")
		if raw, ok = s.readSeen(file); !ok {
			return nil
		}
	}
	var cfg denoConfig
	if err := json.Unmarshal(stripJSONC(raw), &cfg); err != nil {
		s.warnf("could not parse %s: %v", file, err)
		return nil
	}
	if s.has(joinRel(dir, "package.json")) {
		s.extraTech = append(s.extraTech, "deno")
		return nil
	}

	name := cleanName(cfg.Name)
	if name == "" {
		name = s.dirName(dir)
	}
	c := newCandidate(dir, name, "deno", originNative)
	c.addSource(api.Source{File: file})
	c.PackageManager = "deno"

	imports := make([]string, 0, len(cfg.Imports))
	for k, v := range cfg.Imports {
		imports = append(imports, strings.ToLower(k+" "+v))
	}
	joined := strings.Join(imports, "\n")
	for _, fw := range denoFrameworks {
		if strings.Contains(joined, fw.marker) {
			c.Framework = fw.name
			c.httpish = true
			break
		}
	}

	var task, body string
	for _, k := range []string{"dev", "start", "serve"} {
		if b, ok := cfg.Tasks[k]; ok {
			task, body = k, b
			break
		}
	}
	switch {
	case task != "":
		line := lineOf(raw, `"`+task+`"`)
		c.setCommand("deno task "+task, api.ConfidenceHigh, api.Source{File: file, Line: line, Note: "task: " + body})
		if p, _ := findPort([]byte(body), scriptPortRe); p > 0 {
			c.offerPort(p, api.ConfidenceHigh, api.Source{File: file, Line: line, Note: "port flag in \"" + task + "\" task"})
		}
		if strings.Contains(body, "--allow-net") {
			c.httpish = true // a task that opens the network is a server
		}
	default:
		for _, entry := range denoEntries {
			rel := joinRel(dir, entry)
			src, ok := s.readSeen(rel)
			if !ok || !denoServeRe.Match(src) {
				continue
			}
			c.httpish = true
			c.setCommand("deno run --allow-net "+entry, api.ConfidenceMedium,
				api.Source{File: rel, Line: lineOf(src, "Deno.serve("), Note: "Deno.serve entry; add --allow-* flags it needs"})
			if p, line := findPort(src, denoPortRe); p > 0 {
				c.offerPort(p, api.ConfidenceHigh, api.Source{File: rel, Line: line, Note: "port in Deno.serve options"})
			} else {
				c.offerPort(8000, api.ConfidenceMedium, api.Source{File: rel, Line: lineOf(src, "Deno.serve("), Note: "Deno.serve default port"})
			}
			break
		}
	}
	if c.Command == nil {
		s.extraTech = append(s.extraTech, "deno")
		return nil
	}
	if c.Framework == "fresh" {
		c.offerPort(8000, api.ConfidenceMedium, api.Source{File: file, Note: "Fresh default port"})
	}
	for _, lib := range denoLibs {
		if strings.Contains(joined, lib.marker) {
			c.needKind(lib.kind, api.Source{File: file, Line: lineOf(raw, lib.marker), Note: "imports " + lib.marker})
		}
	}
	return []*candidate{c}
}

// stripJSONC removes // and /* */ comments (and trailing commas) so deno.jsonc
// parses as JSON. String contents are left alone.
func stripJSONC(in []byte) []byte {
	out := make([]byte, 0, len(in))
	inStr, esc := false, false
	for i := 0; i < len(in); i++ {
		ch := in[i]
		switch {
		case inStr:
			out = append(out, ch)
			switch {
			case esc:
				esc = false
			case ch == '\\':
				esc = true
			case ch == '"':
				inStr = false
			}
		case ch == '"':
			inStr = true
			out = append(out, ch)
		case ch == '/' && i+1 < len(in) && in[i+1] == '/':
			for i < len(in) && in[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case ch == '/' && i+1 < len(in) && in[i+1] == '*':
			i += 2
			for i+1 < len(in) && !(in[i] == '*' && in[i+1] == '/') {
				i++
			}
			i++
		default:
			out = append(out, ch)
		}
	}
	return trailingCommaRe.ReplaceAll(out, []byte("$1"))
}

var trailingCommaRe = regexp.MustCompile(`,(\s*[}\]])`)
