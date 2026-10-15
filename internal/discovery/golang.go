package discovery

import (
	"bytes"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

var goFrameworks = []struct{ module, name string }{
	{"github.com/gin-gonic/gin", "gin"},
	{"github.com/labstack/echo", "echo"},
	{"github.com/gofiber/fiber", "fiber"},
	{"github.com/go-chi/chi", "chi"},
	{"github.com/gorilla/mux", "gorilla/mux"},
}

var goLibs = []struct{ prefix, kind string }{
	{"github.com/jackc/pgx", "postgres"},
	{"github.com/lib/pq", "postgres"},
	{"github.com/go-sql-driver/mysql", "mysql"},
	{"github.com/redis/go-redis", "redis"},
	{"github.com/go-redis/redis", "redis"},
	{"github.com/gomodule/redigo", "redis"},
	{"go.mongodb.org/mongo-driver", "mongodb"},
	{"github.com/rabbitmq/amqp091-go", "rabbitmq"},
	{"github.com/streadway/amqp", "rabbitmq"},
	{"github.com/segmentio/kafka-go", "kafka"},
	{"github.com/ibm/sarama", "kafka"},
}

var (
	goModuleRe  = regexp.MustCompile(`(?m)^module\s+(\S+)`)
	goRequireRe = regexp.MustCompile(`(?m)^\s*(?:require\s+)?([a-zA-Z0-9.\-_]+\.[a-z]+/\S+)\s+v\S+`)
	goListenRe  = regexp.MustCompile(`(?:ListenAndServe(?:TLS)?|\.Run|\.Listen|\.Start|net\.Listen)\(\s*(?:"tcp[46]?",\s*)?"[^"]*:(\d{2,5})"`)
	goAddrRe    = regexp.MustCompile(`Addr:\s*"[^"]*:(\d{2,5})"`)
	goMainRe    = regexp.MustCompile(`(?m)^package main\b`)
	goHealthRe  = regexp.MustCompile(`"(?:(?:GET|HEAD) )?(/(?:healthz?|readyz|livez|ping|status))"`)
)

func (s *scanner) analyzeGo(dir string) []*candidate {
	file := joinRel(dir, "go.mod")
	raw, ok := s.readSeen(file)
	if !ok {
		return nil
	}
	module := ""
	if m := goModuleRe.FindSubmatch(raw); m != nil {
		module = string(m[1])
	}

	// Find main packages: the module dir itself, or cmd/<name>.
	type mainPkg struct{ rel, name string }
	var mains []mainPkg
	if s.isGoMain(dir) {
		mains = append(mains, mainPkg{".", goModuleName(module)})
	}
	cmdDir := joinRel(dir, "cmd")
	var cmdNames []string
	for d := range s.files {
		if path.Dir(d) == cmdDir && s.isGoMain(d) {
			cmdNames = append(cmdNames, path.Base(d))
		}
	}
	sort.Strings(cmdNames)
	for _, n := range cmdNames {
		mains = append(mains, mainPkg{"./cmd/" + n, n})
	}
	if len(mains) == 0 {
		s.extraTech = append(s.extraTech, "go")
		return nil
	}

	var framework string
	for _, fw := range goFrameworks {
		if bytes.Contains(raw, []byte(fw.module)) {
			framework = fw.name
			break
		}
	}

	var out []*candidate
	for _, m := range mains {
		name := cleanName(m.name)
		if name == "" || (len(mains) == 1 && m.rel == ".") {
			name = cleanName(goModuleName(module))
		}
		if name == "" {
			name = s.dirName(dir)
		}
		c := newCandidate(dir, name, "go", originNative)
		c.Framework = framework
		c.addSource(api.Source{File: file})
		pkgDir := dir
		if m.rel != "." {
			pkgDir = joinRel(dir, strings.TrimPrefix(m.rel, "./"))
		}
		mainFile := joinRel(pkgDir, "main.go")
		c.setCommand("go run "+m.rel, api.ConfidenceHigh, api.Source{File: mainFile, Line: 1, Note: "package main"})

		httpish := framework != ""
		for _, f := range s.files[pkgDir] {
			if !strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "_test.go") {
				continue
			}
			rel := joinRel(pkgDir, f)
			content, ok := s.readSeen(rel)
			if !ok {
				continue
			}
			if bytes.Contains(content, []byte(`"net/http"`)) {
				httpish = true
			}
			if c.healthPath == "" {
				if m := goHealthRe.FindSubmatchIndex(content); m != nil {
					c.healthPath = string(content[m[2]:m[3]])
					c.healthPathFrom = api.Source{File: rel, Line: lineAtOffset(content, m[0]), Note: "health route"}
				}
			}
			for _, re := range []*regexp.Regexp{goListenRe, goAddrRe} {
				if p, line := findPort(content, re); p > 0 {
					c.offerPort(p, api.ConfidenceMedium, api.Source{File: rel, Line: line, Note: "listen address in code"})
				}
			}
		}
		c.httpish = httpish

		for _, req := range goRequireRe.FindAllSubmatchIndex(raw, -1) {
			mod := strings.ToLower(string(raw[req[2]:req[3]]))
			for _, lib := range goLibs {
				if strings.HasPrefix(mod, lib.prefix) {
					c.needKind(lib.kind, api.Source{File: file, Line: lineAtOffset(raw, req[2]), Note: "requires " + string(raw[req[2]:req[3]])})
				}
			}
		}
		out = append(out, c)
	}
	return out
}

// isGoMain reports whether dir holds a main.go declaring package main.
func (s *scanner) isGoMain(dir string) bool {
	content, ok := s.readSeen(joinRel(dir, "main.go"))
	return ok && goMainRe.Match(content)
}

// goModuleName returns the last meaningful element of a module path,
// skipping major-version suffixes like /v2.
func goModuleName(module string) string {
	parts := strings.Split(module, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		p := parts[i]
		if len(p) >= 2 && p[0] == 'v' && strings.Trim(p[1:], "0123456789") == "" {
			continue
		}
		return p
	}
	return ""
}
