package discovery

import (
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

var composeFiles = map[string]bool{
	"compose.yaml": true, "compose.yml": true,
	"docker-compose.yaml": true, "docker-compose.yml": true,
}

// imageKinds maps an image's base name to an infrastructure kind.
var imageKinds = map[string]string{
	"postgres": "postgres", "postgresql": "postgres", "postgis": "postgres", "timescaledb": "postgres",
	"mysql": "mysql", "mariadb": "mysql",
	"redis": "redis", "redis-stack": "redis", "redis-stack-server": "redis", "valkey": "redis", "keydb": "redis",
	"mongo": "mongodb", "mongodb": "mongodb",
	"rabbitmq": "rabbitmq", "kafka": "kafka", "cp-kafka": "kafka",
	"elasticsearch": "elasticsearch", "opensearch": "elasticsearch",
	"memcached": "memcached", "minio": "minio", "mailhog": "mailhog", "mailpit": "mailhog",
	"nats": "nats", "clickhouse-server": "clickhouse",
}

var httpImages = map[string]bool{"nginx": true, "caddy": true, "traefik": true, "httpd": true, "mailhog": true, "mailpit": true}

// imageBase returns the repository base name of an image reference.
func imageBase(image string) string {
	ref := image
	if i := strings.IndexByte(ref, '@'); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndexByte(ref, '/'); i >= 0 {
		ref = ref[i+1:]
	}
	if i := strings.IndexByte(ref, ':'); i >= 0 {
		ref = ref[:i]
	}
	return strings.ToLower(ref)
}

func mapGet(n *yaml.Node, key string) (k, v *yaml.Node) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i], n.Content[i+1]
		}
	}
	return nil, nil
}

func (s *scanner) analyzeCompose(dir, name string) []*candidate {
	file := joinRel(dir, name)
	raw, ok := s.readSeen(file)
	if !ok {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		s.warnf("could not parse %s: %v", file, err)
		return nil
	}
	if len(doc.Content) == 0 {
		return nil
	}
	_, services := mapGet(doc.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil
	}

	var out []*candidate
	for i := 0; i+1 < len(services.Content); i += 2 {
		key, svc := services.Content[i], services.Content[i+1]
		if svc.Kind != yaml.MappingNode {
			continue
		}
		c := newCandidate(dir, key.Value, "container", originCompose)
		c.addSource(api.Source{File: file, Line: key.Line})

		if _, img := mapGet(svc, "image"); img != nil && img.Kind == yaml.ScalarNode {
			c.Image = img.Value
			base := imageBase(img.Value)
			if kind, ok := imageKinds[base]; ok {
				c.infra = kind
				c.Technology = kind
			}
			c.httpish = httpImages[base]
		}
		if bk, b := mapGet(svc, "build"); b != nil {
			ctx := ""
			switch b.Kind {
			case yaml.ScalarNode:
				ctx = b.Value
			case yaml.MappingNode:
				if _, cn := mapGet(b, "context"); cn != nil {
					ctx = cn.Value
				}
			}
			if ctx != "" && !strings.Contains(ctx, "://") {
				bd := path.Clean(path.Join(dir, filepath.ToSlash(ctx)))
				if bd == ".." || strings.HasPrefix(bd, "../") {
					s.warnf("%s:%d: build context %q is outside the project root; ignored", file, bk.Line, ctx)
				} else {
					c.buildDir = bd
					c.dir = bd
					c.Dir = dirLabel(bd)
				}
			}
		}

		rel, err := filepath.Rel(filepath.FromSlash(c.dir), filepath.FromSlash(file))
		if err != nil {
			rel = file
		}
		rel = filepath.ToSlash(rel)
		cmd := "docker compose up " + key.Value
		if rel != name || !composeFiles[name] {
			cmd = "docker compose -f " + rel + " up " + key.Value
		}
		c.setCommand(cmd, api.ConfidenceHigh, api.Source{File: file, Line: key.Line, Note: "compose service"})

		if _, ports := mapGet(svc, "ports"); ports != nil && ports.Kind == yaml.SequenceNode {
			for _, p := range ports.Content {
				if host := composeHostPort(p); host > 0 {
					c.offerPort(host, api.ConfidenceHigh, api.Source{File: file, Line: p.Line, Note: "published port"})
					break
				}
			}
		}
		if _, deps := mapGet(svc, "depends_on"); deps != nil {
			var names []*yaml.Node
			switch deps.Kind {
			case yaml.SequenceNode:
				names = deps.Content
			case yaml.MappingNode:
				for j := 0; j < len(deps.Content); j += 2 {
					names = append(names, deps.Content[j])
				}
			}
			for _, n := range names {
				c.needs = append(c.needs, need{name: n.Value, declared: true, source: api.Source{File: file, Line: n.Line, Note: "depends_on"}})
			}
		}
		if _, env := mapGet(svc, "environment"); env != nil {
			c.EnvKeys = append(c.EnvKeys, composeEnvKeys(env)...)
		}
		out = append(out, c)
	}
	return out
}

// composeHostPort returns the published host port of a compose port entry, or 0.
func composeHostPort(n *yaml.Node) int {
	switch n.Kind {
	case yaml.ScalarNode:
		spec := n.Value
		if i := strings.IndexByte(spec, '/'); i >= 0 {
			spec = spec[:i]
		}
		parts := strings.Split(spec, ":")
		if len(parts) < 2 {
			return 0 // container port only; host port is ephemeral
		}
		host := parts[len(parts)-2]
		if i := strings.IndexByte(host, '-'); i >= 0 {
			host = host[:i]
		}
		p, _ := strconv.Atoi(host)
		return p
	case yaml.MappingNode:
		if _, pub := mapGet(n, "published"); pub != nil {
			v := pub.Value
			if i := strings.IndexByte(v, '-'); i >= 0 {
				v = v[:i]
			}
			p, _ := strconv.Atoi(v)
			return p
		}
	}
	return 0
}

// composeEnvKeys returns variable names only; values are never kept.
func composeEnvKeys(n *yaml.Node) []string {
	var keys []string
	switch n.Kind {
	case yaml.SequenceNode:
		for _, e := range n.Content {
			k, _, _ := strings.Cut(e.Value, "=")
			if k = strings.TrimSpace(k); k != "" {
				keys = append(keys, k)
			}
		}
	case yaml.MappingNode:
		for i := 0; i < len(n.Content); i += 2 {
			keys = append(keys, n.Content[i].Value)
		}
	}
	return keys
}

var exposeRe = regexp.MustCompile(`(?mi)^\s*EXPOSE\s+(\d{2,5})`)

// applyDockerfiles attaches EXPOSE ports to candidates in the same directory,
// or creates a low-confidence docker candidate when nothing else is there.
func (s *scanner) applyDockerfiles(cands []*candidate) []*candidate {
	for _, dir := range s.sortedDirs() {
		file := joinRel(dir, "Dockerfile")
		raw, ok := s.readSeen(file)
		if !ok {
			continue
		}
		port, line := findPort(raw, exposeRe)
		src := api.Source{File: file, Line: line, Note: "EXPOSE (container port; assumed published on the same host port)"}
		attached := false
		for _, c := range cands {
			if c.dir != dir {
				continue
			}
			attached = true
			c.addSource(api.Source{File: file})
			c.offerPort(port, api.ConfidenceLow, src)
		}
		if attached {
			continue
		}
		name := s.dirName(dir)
		c := newCandidate(dir, name, "docker", originDocker)
		c.addSource(api.Source{File: file})
		cmd := "docker build -t " + name + " . && docker run --rm " + name
		if port > 0 {
			cmd = "docker build -t " + name + " . && docker run --rm -p " + strconv.Itoa(port) + ":" + strconv.Itoa(port) + " " + name
		}
		c.setCommand(cmd, api.ConfidenceLow, api.Source{File: file, Note: "Dockerfile with no compose service or language manifest"})
		c.offerPort(port, api.ConfidenceLow, src)
		cands = append(cands, c)
	}
	return cands
}

var envExampleFiles = []string{".env.example", ".env.sample", ".env.template", "env.example"}

var envKeyLineRe = regexp.MustCompile(`(?m)^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=`)

var envKindPrefixes = []struct{ prefix, kind string }{
	{"POSTGRES_", "postgres"}, {"PGHOST", "postgres"}, {"PGPORT", "postgres"}, {"PGDATABASE", "postgres"},
	{"MYSQL_", "mysql"}, {"REDIS_", "redis"}, {"MONGO", "mongodb"},
	{"RABBITMQ_", "rabbitmq"}, {"AMQP_URL", "rabbitmq"}, {"KAFKA_", "kafka"},
}

// applyEnvExamples attaches variable names (never values) from .env.example
// style files to candidates in the same directory. Real .env files are never read.
func (s *scanner) applyEnvExamples(cands []*candidate) {
	for _, dir := range s.sortedDirs() {
		for _, name := range envExampleFiles {
			file := joinRel(dir, name)
			raw, ok := s.readSeen(file)
			if !ok {
				continue
			}
			var keys []string
			var lines []int
			for _, m := range envKeyLineRe.FindAllSubmatchIndex(raw, -1) {
				keys = append(keys, string(raw[m[2]:m[3]]))
				lines = append(lines, lineAtOffset(raw, m[2]))
			}
			for _, c := range cands {
				if c.dir != dir {
					continue
				}
				c.addSource(api.Source{File: file})
				c.EnvKeys = append(c.EnvKeys, keys...)
				for i, k := range keys {
					for _, p := range envKindPrefixes {
						if strings.HasPrefix(k, p.prefix) {
							c.needKind(p.kind, api.Source{File: file, Line: lines[i], Note: "variable " + k})
						}
					}
				}
			}
		}
	}
	for _, c := range cands {
		c.EnvKeys = uniqueSorted(c.EnvKeys)
	}
}

func uniqueSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	sort.Strings(in)
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}
