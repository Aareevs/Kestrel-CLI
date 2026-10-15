package discovery

import (
	"bytes"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// origin records which kind of file produced a candidate.
type origin int

const (
	originNative  origin = iota // a language manifest (package.json, go.mod, ...)
	originCompose               // a docker-compose service
	originDocker                // a lone Dockerfile
)

// candidate is a service candidate plus the private facts the post-pass needs.
type candidate struct {
	api.ServiceCandidate
	dir      string // slash-relative dir, "." for root
	origin   origin
	buildDir string // compose: build context dir, "" if image-only
	infra    string // well-known infrastructure kind (postgres, redis, ...)
	httpish  bool   // speaks HTTP (web framework or HTTP server code)
	// healthPath is a health endpoint path found in code, e.g. "/healthz".
	healthPath     string
	healthPathFrom api.Source
	needs          []need // dependency hints resolved in the post-pass
}

// need is a dependency hint: either an infrastructure kind or a localhost port.
type need struct {
	kind     string // infrastructure kind, e.g. "postgres"
	port     int    // or a localhost port another service should own
	name     string // or a declared service name (compose depends_on)
	declared bool
	source   api.Source
}

func newCandidate(dir, name, tech string, o origin) *candidate {
	return &candidate{
		ServiceCandidate: api.ServiceCandidate{
			Name:       name,
			Dir:        dirLabel(dir),
			Technology: tech,
		},
		dir:    dir,
		origin: o,
	}
}

func (c *candidate) addSource(src api.Source) {
	for _, s := range c.Sources {
		if s == src {
			return
		}
	}
	c.Sources = append(c.Sources, src)
}

func (c *candidate) setCommand(value string, conf api.Confidence, src api.Source) {
	c.Command = &api.CommandGuess{Value: value, Confidence: conf, Source: src}
}

// offerPort sets the port if none is known or the new guess is more confident.
func (c *candidate) offerPort(port int, conf api.Confidence, src api.Source) {
	if port <= 0 || port > 65535 {
		return
	}
	if c.Port == nil || conf.Rank() > c.Port.Confidence.Rank() {
		c.Port = &api.PortGuess{Value: port, Confidence: conf, Source: src}
	}
}

func (c *candidate) needKind(kind string, src api.Source) {
	for _, n := range c.needs {
		if n.kind == kind {
			return
		}
	}
	c.needs = append(c.needs, need{kind: kind, source: src})
}

func (c *candidate) needPort(port int, src api.Source) {
	for _, n := range c.needs {
		if n.port == port {
			return
		}
	}
	c.needs = append(c.needs, need{port: port, source: src})
}

// lineOf returns the 1-based line of the first occurrence of needle, or 0.
func lineOf(content []byte, needle string) int {
	i := bytes.Index(content, []byte(needle))
	if i < 0 {
		return 0
	}
	return bytes.Count(content[:i], []byte("\n")) + 1
}

// lineAtOffset returns the 1-based line containing byte offset off.
func lineAtOffset(content []byte, off int) int {
	return bytes.Count(content[:off], []byte("\n")) + 1
}

// findPort applies re (whose first group is a port) to content and returns the
// port and its line.
func findPort(content []byte, re *regexp.Regexp) (port, line int) {
	m := re.FindSubmatchIndex(content)
	if m == nil {
		return 0, 0
	}
	p, err := strconv.Atoi(string(content[m[2]:m[3]]))
	if err != nil || p <= 0 || p > 65535 {
		return 0, 0
	}
	return p, lineAtOffset(content, m[2])
}

var nameCleanRe = regexp.MustCompile(`[^a-z0-9_.-]+`)

// cleanName turns an arbitrary package/module name into a valid service name.
func cleanName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	s = nameCleanRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-._")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-._")
	}
	return s
}

// dirName is the fallback service name for a directory.
func (s *scanner) dirName(dir string) string {
	if dir == "." {
		if n := cleanName(path.Base(strings.ReplaceAll(s.root, `\`, "/"))); n != "" {
			return n
		}
		return "app"
	}
	return cleanName(path.Base(dir))
}

// libMap maps client libraries (lower-cased) to the infrastructure they talk to.
type libMap map[string]string

func (m libMap) lookup(lib string) (string, bool) {
	k, ok := m[strings.ToLower(lib)]
	return k, ok
}

// localhostURLRe finds http(s)://localhost:PORT references, used to infer
// dependencies from dev-server proxies.
var localhostURLRe = regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1|0\.0\.0\.0):(\d{2,5})`)

func (c *candidate) needLocalhostURLs(content []byte, file string) {
	for _, m := range localhostURLRe.FindAllSubmatchIndex(content, -1) {
		p, err := strconv.Atoi(string(content[m[2]:m[3]]))
		if err != nil {
			continue
		}
		if c.Port != nil && c.Port.Value == p {
			continue
		}
		c.needPort(p, api.Source{File: file, Line: lineAtOffset(content, m[0]), Note: "proxies to localhost:" + strconv.Itoa(p)})
	}
}
