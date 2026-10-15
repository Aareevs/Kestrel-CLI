package discovery

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/kestrel-cli/kestrel/pkg/api"
)

// ---- Python ---------------------------------------------------------------

var pythonLibs = []struct{ lib, kind string }{
	{"psycopg2", "postgres"}, {"psycopg2-binary", "postgres"}, {"psycopg", "postgres"}, {"asyncpg", "postgres"},
	{"mysqlclient", "mysql"}, {"pymysql", "mysql"}, {"mysql-connector-python", "mysql"},
	{"redis", "redis"}, {"pymongo", "mongodb"}, {"motor", "mongodb"},
	{"pika", "rabbitmq"}, {"kafka-python", "kafka"}, {"confluent-kafka", "kafka"},
}

var (
	pyProjectNameRe = regexp.MustCompile(`(?m)^\s*name\s*=\s*["']([^"']+)["']`)
	fastAPIVarRe    = regexp.MustCompile(`(?m)^(\w+)\s*=\s*FastAPI\(`)
	flaskVarRe      = regexp.MustCompile(`(?m)^(\w+)\s*=\s*Flask\(`)
	pyRunPortRe     = regexp.MustCompile(`\.run\([^)]*port\s*=\s*(\d{2,5})`)
)

// pyDepRe matches a package name as a whole token in a dependency file.
func pyDepIndex(content []byte, lib string) int {
	re := regexp.MustCompile(`(?mi)(?:^|["'\s,\[])` + regexp.QuoteMeta(lib) + `(?:$|[\s"'<>=~!\[;,\]])`)
	loc := re.FindIndex(content)
	if loc == nil {
		return -1
	}
	return loc[0]
}

func (s *scanner) analyzePython(dir string) []*candidate {
	depFiles := []string{"pyproject.toml", "requirements.txt", "Pipfile", "setup.py"}
	contents := map[string][]byte{}
	for _, f := range depFiles {
		rel := joinRel(dir, f)
		if b, ok := s.readSeen(rel); ok {
			contents[rel] = b
		}
	}
	hasManage := s.has(joinRel(dir, "manage.py"))
	if len(contents) == 0 && !hasManage {
		return nil
	}

	name := ""
	if b, ok := contents[joinRel(dir, "pyproject.toml")]; ok {
		if m := pyProjectNameRe.FindSubmatch(b); m != nil {
			name = cleanName(string(m[1]))
		}
	}
	if name == "" {
		name = s.dirName(dir)
	}
	c := newCandidate(dir, name, "python", originNative)
	for _, f := range depFiles {
		if _, ok := contents[joinRel(dir, f)]; ok {
			c.addSource(api.Source{File: joinRel(dir, f)})
		}
	}

	findDep := func(lib string) (api.Source, bool) {
		for _, f := range depFiles {
			rel := joinRel(dir, f)
			b, ok := contents[rel]
			if !ok {
				continue
			}
			if i := pyDepIndex(b, lib); i >= 0 {
				return api.Source{File: rel, Line: lineAtOffset(b, i) + boolInt(b[i] == '\n')}, true
			}
		}
		return api.Source{}, false
	}

	prefix := ""
	switch {
	case s.has(joinRel(dir, "uv.lock")):
		prefix = "uv run "
	case s.has(joinRel(dir, "poetry.lock")) || bytes.Contains(contents[joinRel(dir, "pyproject.toml")], []byte("[tool.poetry]")):
		prefix = "poetry run "
	case s.has(joinRel(dir, "Pipfile")):
		prefix = "pipenv run "
	}

	_, hasFastAPI := findDep("fastapi")
	_, hasFlask := findDep("flask")
	switch {
	case hasManage:
		c.Framework = "django"
		c.httpish = true
		c.setCommand(prefix+"python manage.py runserver", api.ConfidenceHigh, api.Source{File: joinRel(dir, "manage.py"), Note: "Django management script"})
		c.offerPort(8000, api.ConfidenceMedium, api.Source{File: joinRel(dir, "manage.py"), Note: "Django runserver default port"})
	case hasFastAPI:
		c.Framework = "fastapi"
		c.httpish = true
		if entry, v, line := s.pyEntry(dir, fastAPIVarRe); entry != "" {
			mod := strings.TrimSuffix(strings.ReplaceAll(entry, "/", "."), ".py")
			c.setCommand(prefix+"uvicorn "+mod+":"+v+" --reload", api.ConfidenceMedium, api.Source{File: joinRel(dir, entry), Line: line, Note: "FastAPI app object"})
			c.offerPort(8000, api.ConfidenceMedium, api.Source{File: joinRel(dir, entry), Note: "uvicorn default port"})
		}
	case hasFlask:
		c.Framework = "flask"
		c.httpish = true
		if entry, _, line := s.pyEntry(dir, flaskVarRe); entry != "" {
			src := api.Source{File: joinRel(dir, entry), Line: line, Note: "Flask app object"}
			mod := strings.TrimSuffix(strings.ReplaceAll(entry, "/", "."), ".py")
			c.setCommand(prefix+"flask --app "+mod+" run", api.ConfidenceMedium, src)
			if b, ok := s.readSeen(joinRel(dir, entry)); ok {
				if p, l := findPort(b, pyRunPortRe); p > 0 {
					c.offerPort(p, api.ConfidenceMedium, api.Source{File: joinRel(dir, entry), Line: l, Note: "app.run port"})
				}
			}
			c.offerPort(5000, api.ConfidenceMedium, api.Source{File: joinRel(dir, entry), Note: "flask run default port"})
		}
	default:
		for _, entry := range []string{"main.py", "app.py"} {
			if s.has(joinRel(dir, entry)) {
				c.setCommand(prefix+"python "+entry, api.ConfidenceLow, api.Source{File: joinRel(dir, entry), Note: "conventional entry file"})
				break
			}
		}
	}
	if c.Command == nil {
		s.extraTech = append(s.extraTech, "python")
		return nil
	}
	for _, lib := range pythonLibs {
		if src, ok := findDep(lib.lib); ok {
			src.Note = "uses " + lib.lib
			c.needKind(lib.kind, src)
		}
	}
	return []*candidate{c}
}

// pyEntry finds the first conventional entry file whose content matches re
// and returns it with the app variable name and line.
func (s *scanner) pyEntry(dir string, re *regexp.Regexp) (entry, variable string, line int) {
	for _, f := range []string{"main.py", "app.py", "app/main.py", "src/main.py", "wsgi.py", "asgi.py"} {
		b, ok := s.readSeen(joinRel(dir, f))
		if !ok {
			continue
		}
		if m := re.FindSubmatchIndex(b); m != nil {
			return f, string(b[m[2]:m[3]]), lineAtOffset(b, m[0])
		}
	}
	return "", "", 0
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- JVM (Maven, Gradle) ----------------------------------------------------

var (
	pomParentRe     = regexp.MustCompile(`(?s)<parent>.*?</parent>`)
	pomArtifactRe   = regexp.MustCompile(`<artifactId>\s*([^<\s]+)\s*</artifactId>`)
	gradleRootRe    = regexp.MustCompile(`rootProject\.name\s*=\s*["']([^"']+)["']`)
	springPortRe    = regexp.MustCompile(`(?m)^\s*server\.port\s*[=:]\s*(\d{2,5})`)
	springYAMLPortR = regexp.MustCompile(`(?m)^server:\s*\n(?:[ \t]+.*\n)*?[ \t]+port:\s*(\d{2,5})`)
)

var jvmLibs = []struct{ needle, kind string }{
	{"org.postgresql", "postgres"}, {"mysql-connector", "mysql"},
	{"spring-boot-starter-data-redis", "redis"}, {"jedis", "redis"}, {"lettuce", "redis"},
	{"spring-boot-starter-data-mongodb", "mongodb"}, {"spring-boot-starter-amqp", "rabbitmq"},
	{"spring-kafka", "kafka"},
}

func (s *scanner) analyzeJVM(dir string) []*candidate {
	var file, tool string
	for _, f := range []string{"pom.xml", "build.gradle.kts", "build.gradle"} {
		if s.has(joinRel(dir, f)) {
			file, tool = joinRel(dir, f), f
			break
		}
	}
	if file == "" {
		return nil
	}
	raw, ok := s.readSeen(file)
	if !ok {
		return nil
	}
	name := ""
	if tool == "pom.xml" {
		if m := pomArtifactRe.FindSubmatch(pomParentRe.ReplaceAll(raw, nil)); m != nil {
			name = cleanName(string(m[1]))
		}
	} else {
		for _, sf := range []string{"settings.gradle.kts", "settings.gradle"} {
			if b, ok := s.readSeen(joinRel(dir, sf)); ok {
				if m := gradleRootRe.FindSubmatch(b); m != nil {
					name = cleanName(string(m[1]))
				}
			}
		}
	}
	if name == "" {
		name = s.dirName(dir)
	}
	c := newCandidate(dir, name, "java", originNative)
	c.addSource(api.Source{File: file})

	spring := bytes.Contains(raw, []byte("spring-boot")) || bytes.Contains(raw, []byte("org.springframework.boot"))
	if spring {
		c.Framework = "spring-boot"
		c.httpish = true
		line := lineOf(raw, "spring-boot")
		if line == 0 {
			line = lineOf(raw, "org.springframework.boot")
		}
		src := api.Source{File: file, Line: line, Note: "Spring Boot build plugin"}
		if tool == "pom.xml" {
			c.setCommand(wrapper(s, dir, "mvnw", "mvn")+" spring-boot:run", api.ConfidenceHigh, src)
		} else {
			c.setCommand(wrapper(s, dir, "gradlew", "gradle")+" bootRun", api.ConfidenceHigh, src)
		}
		for _, rf := range []string{"src/main/resources/application.properties", "src/main/resources/application.yml", "src/main/resources/application.yaml"} {
			rel := joinRel(dir, rf)
			if b, ok := s.readOptional(rel); ok {
				re := springPortRe
				if !strings.HasSuffix(rf, ".properties") {
					re = springYAMLPortR
				}
				if p, l := findPort(b, re); p > 0 {
					c.offerPort(p, api.ConfidenceHigh, api.Source{File: rel, Line: l, Note: "server.port"})
				}
			}
		}
		c.offerPort(8080, api.ConfidenceMedium, api.Source{File: file, Note: "Spring Boot default port"})
	} else if tool != "pom.xml" && regexp.MustCompile(`\bapplication\b`).Match(raw) {
		c.setCommand(wrapper(s, dir, "gradlew", "gradle")+" run", api.ConfidenceMedium, api.Source{File: file, Line: lineOf(raw, "application"), Note: "Gradle application plugin"})
	}
	if c.Command == nil {
		s.extraTech = append(s.extraTech, "java")
		return nil
	}
	for _, lib := range jvmLibs {
		if line := lineOf(raw, lib.needle); line > 0 {
			c.needKind(lib.kind, api.Source{File: file, Line: line, Note: "uses " + lib.needle})
		}
	}
	return []*candidate{c}
}

// wrapper prefers a checked-in build wrapper script over a global tool.
func wrapper(s *scanner, dir, script, tool string) string {
	if s.has(joinRel(dir, script)) {
		return "./" + script
	}
	return tool
}

// ---- PHP --------------------------------------------------------------------

func (s *scanner) analyzePHP(dir string) []*candidate {
	file := joinRel(dir, "composer.json")
	raw, ok := s.readSeen(file)
	if !ok {
		return nil
	}
	c := newCandidate(dir, s.dirName(dir), "php", originNative)
	c.addSource(api.Source{File: file})
	switch {
	case bytes.Contains(raw, []byte(`"laravel/framework"`)) && s.has(joinRel(dir, "artisan")):
		c.Framework = "laravel"
		c.httpish = true
		c.setCommand("php artisan serve", api.ConfidenceHigh, api.Source{File: joinRel(dir, "artisan"), Note: "Laravel artisan"})
		c.offerPort(8000, api.ConfidenceMedium, api.Source{File: file, Line: lineOf(raw, "laravel/framework"), Note: "artisan serve default port"})
	case s.has(joinRel(dir, "public/index.php")):
		c.httpish = true
		c.setCommand("php -S localhost:8000 -t public", api.ConfidenceLow, api.Source{File: joinRel(dir, "public/index.php"), Note: "front controller; built-in server"})
		c.offerPort(8000, api.ConfidenceLow, api.Source{File: joinRel(dir, "public/index.php"), Note: "port chosen in suggested command"})
	}
	if c.Command == nil {
		s.extraTech = append(s.extraTech, "php")
		return nil
	}
	if line := lineOf(raw, "predis/predis"); line > 0 {
		c.needKind("redis", api.Source{File: file, Line: line, Note: "uses predis/predis"})
	}
	return []*candidate{c}
}

// ---- Ruby -------------------------------------------------------------------

var gemRe = func(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\s*gem\s+["']` + regexp.QuoteMeta(name) + `["']`)
}

func (s *scanner) analyzeRuby(dir string) []*candidate {
	file := joinRel(dir, "Gemfile")
	raw, ok := s.readSeen(file)
	if !ok {
		return nil
	}
	c := newCandidate(dir, s.dirName(dir), "ruby", originNative)
	c.addSource(api.Source{File: file})
	if loc := gemRe("rails").FindIndex(raw); loc != nil {
		c.Framework = "rails"
		c.httpish = true
		cmd := "bundle exec rails server"
		if s.has(joinRel(dir, "bin/rails")) {
			cmd = "bin/rails server"
		}
		c.setCommand(cmd, api.ConfidenceHigh, api.Source{File: file, Line: lineAtOffset(raw, loc[0]), Note: "rails gem"})
		c.offerPort(3000, api.ConfidenceMedium, api.Source{File: file, Line: lineAtOffset(raw, loc[0]), Note: "rails server default port"})
	} else if loc := gemRe("sinatra").FindIndex(raw); loc != nil && s.has(joinRel(dir, "app.rb")) {
		c.Framework = "sinatra"
		c.httpish = true
		c.setCommand("bundle exec ruby app.rb", api.ConfidenceMedium, api.Source{File: joinRel(dir, "app.rb"), Note: "conventional Sinatra entry"})
		c.offerPort(4567, api.ConfidenceMedium, api.Source{File: file, Line: lineAtOffset(raw, loc[0]), Note: "Sinatra default port"})
	}
	if c.Command == nil {
		s.extraTech = append(s.extraTech, "ruby")
		return nil
	}
	for _, g := range []struct{ gem, kind string }{{"pg", "postgres"}, {"mysql2", "mysql"}, {"redis", "redis"}, {"mongoid", "mongodb"}} {
		if loc := gemRe(g.gem).FindIndex(raw); loc != nil {
			c.needKind(g.kind, api.Source{File: file, Line: lineAtOffset(raw, loc[0]), Note: "uses " + g.gem})
		}
	}
	return []*candidate{c}
}

// ---- Rust -------------------------------------------------------------------

var (
	cargoPackageNameRe = regexp.MustCompile(`(?ms)^\[package\][^\[]*?^\s*name\s*=\s*"([^"]+)"`)
	rustBindRe         = regexp.MustCompile(`"(?:0\.0\.0\.0|127\.0\.0\.1|localhost|\[::\])?:(\d{2,5})"|\(\s*"[^"]*",\s*(\d{2,5})\s*\)`)
)

var rustFrameworks = []string{"actix-web", "axum", "rocket", "warp", "poem"}

func (s *scanner) analyzeRust(dir string) []*candidate {
	file := joinRel(dir, "Cargo.toml")
	raw, ok := s.readSeen(file)
	if !ok {
		return nil
	}
	mainRel := joinRel(dir, "src/main.rs")
	if !s.has(mainRel) {
		s.extraTech = append(s.extraTech, "rust")
		return nil
	}
	name := ""
	if m := cargoPackageNameRe.FindSubmatch(raw); m != nil {
		name = cleanName(string(m[1]))
	}
	if name == "" {
		name = s.dirName(dir)
	}
	c := newCandidate(dir, name, "rust", originNative)
	c.addSource(api.Source{File: file})
	c.setCommand("cargo run", api.ConfidenceHigh, api.Source{File: mainRel, Note: "binary crate"})
	for _, fw := range rustFrameworks {
		if regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(fw) + `\s*=`).Match(raw) {
			c.Framework = fw
			c.httpish = true
			break
		}
	}
	if b, ok := s.readSeen(mainRel); ok {
		if m := rustBindRe.FindSubmatchIndex(b); m != nil {
			g := 2
			if m[2] < 0 {
				g = 4
			}
			if p, _ := findPort(b[m[g]:m[g+1]], regexp.MustCompile(`^(\d+)$`)); p > 0 {
				c.offerPort(p, api.ConfidenceMedium, api.Source{File: mainRel, Line: lineAtOffset(b, m[0]), Note: "bind address in code"})
			}
		}
	}
	if c.Framework == "rocket" {
		c.offerPort(8000, api.ConfidenceLow, api.Source{File: file, Note: "Rocket default port"})
	}
	for _, lib := range []struct{ crate, kind string }{{"tokio-postgres", "postgres"}, {"postgres", "postgres"}, {"redis", "redis"}, {"mongodb", "mongodb"}, {"lapin", "rabbitmq"}, {"rdkafka", "kafka"}} {
		if loc := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(lib.crate) + `\s*=`).FindIndex(raw); loc != nil {
			c.needKind(lib.kind, api.Source{File: file, Line: lineAtOffset(raw, loc[0]), Note: "uses " + lib.crate})
		}
	}
	if loc := regexp.MustCompile(`(?m)^\s*sqlx\s*=.*"postgres"`).FindIndex(raw); loc != nil {
		c.needKind("postgres", api.Source{File: file, Line: lineAtOffset(raw, loc[0]), Note: "uses sqlx with postgres"})
	}
	return []*candidate{c}
}
