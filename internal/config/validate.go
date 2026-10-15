package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	serviceNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
	envKeyRe      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	statusRe      = regexp.MustCompile(`^([1-5][0-9][0-9])(?:-([1-5][0-9][0-9]))?$`)
)

// validator accumulates issues and resolves line numbers from the YAML tree.
type validator struct {
	doc    *yaml.Node
	issues []Issue
}

func (v *validator) add(path []any, field, format string, args ...any) {
	v.issues = append(v.issues, Issue{
		Line:    lineAt(v.doc, path...),
		Field:   field,
		Message: fmt.Sprintf(format, args...),
	})
}

// lineAt returns the line of the node at path (string keys, int indexes),
// falling back to the deepest node that exists.
func lineAt(n *yaml.Node, path ...any) int {
	line := n.Line
	for _, p := range path {
		var next *yaml.Node
		switch k := p.(type) {
		case string:
			if n.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(n.Content); i += 2 {
					if n.Content[i].Value == k {
						line = n.Content[i].Line
						next = n.Content[i+1]
						break
					}
				}
			}
		case int:
			if n.Kind == yaml.SequenceNode && k < len(n.Content) {
				next = n.Content[k]
				line = next.Line
			}
		}
		if next == nil {
			return line
		}
		n = next
	}
	return line
}

func validate(c *Config, doc *yaml.Node, projectRoot string) []Issue {
	v := &validator{doc: doc}

	switch c.Version {
	case 0:
		v.add(nil, "version", "required; add \"version: %d\" at the top of the file", CurrentVersion)
	case CurrentVersion:
	default:
		v.add([]any{"version"}, "version", "unsupported version %d (this Kestrel supports version %d)", c.Version, CurrentVersion)
	}

	if c.Storage.RetentionDays < 0 {
		v.add([]any{"storage", "retention_days"}, "storage.retention_days", "must be at least 1 (default %d)", DefaultRetentionDays)
	}
	if c.Storage.MaxTotalMB < 0 {
		v.add([]any{"storage", "max_total_mb"}, "storage.max_total_mb", "must be at least 1 (default %d)", DefaultMaxTotalMB)
	}
	if c.Correlation.Window < 0 {
		v.add([]any{"correlation", "window"}, "correlation.window", "must be positive (default %s)", DefaultCorrelationWindow)
	}
	for i, p := range c.Redaction.ExtraPatterns {
		if _, err := regexp.Compile(p); err != nil {
			v.add([]any{"redaction", "extra_patterns", i}, fmt.Sprintf("redaction.extra_patterns[%d]", i), "invalid regular expression: %v", err)
		}
	}

	names := map[string]int{}
	for i, s := range c.Services {
		at := func(p ...any) []any { return append([]any{"services", i}, p...) }
		label := serviceLabel(i, s.Name)
		switch {
		case s.Name == "":
			v.add(at(), label+".name", "required")
		case !serviceNameRe.MatchString(s.Name):
			v.add(at("name"), label+".name", "%q is not a valid name (letters, digits, '-', '_', '.'; max 63 chars)", s.Name)
		default:
			if prev, dup := names[s.Name]; dup {
				v.add(at("name"), label+".name", "duplicate service name (first defined as services[%d])", prev)
			} else {
				names[s.Name] = i
			}
		}
		if s.Command == "" && s.Health == nil {
			v.add(at(), label, "needs a \"command\" to run or a \"health\" probe to observe")
		}
		if s.Dir != "" && projectRoot != "" {
			d := s.Dir
			if !filepath.IsAbs(d) {
				d = filepath.Join(projectRoot, d)
			}
			if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
				v.add(at("dir"), label+".dir", "directory %q does not exist", s.Dir)
			}
		}
		keys := make([]string, 0, len(s.Env))
		for k := range s.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !envKeyRe.MatchString(k) {
				v.add(at("env", k), label+".env", "%q is not a valid environment variable name", k)
			}
			if _, err := Refs(s.Env[k]); err != nil {
				v.add(at("env", k), label+".env."+k, "%v", err)
			}
		}
		if s.Health != nil {
			v.health(at("health"), label+".health", s)
		}
	}

	for i, s := range c.Services {
		label := serviceLabel(i, s.Name)
		seen := map[string]bool{}
		for j, d := range s.DependsOn {
			p := []any{"services", i, "depends_on", j}
			switch {
			case d == s.Name:
				v.add(p, label+".depends_on", "a service cannot depend on itself")
			case seen[d]:
				v.add(p, label+".depends_on", "%q is listed twice", d)
			default:
				if _, ok := names[d]; !ok {
					v.add(p, label+".depends_on", "unknown service %q%s", d, suggest(d, names))
				}
			}
			seen[d] = true
		}
	}
	if cycle := findCycle(c.Services); cycle != nil {
		v.add([]any{"services", names[cycle[0]], "depends_on"}, "depends_on", "dependency cycle: %s", strings.Join(cycle, " → "))
	}

	for i, ch := range c.Verify.Checks {
		at := []any{"verify", "checks", i}
		label := fmt.Sprintf("verify.checks[%d]", i)
		switch ch.Type {
		case CheckProbe:
			if ch.Service == "" {
				v.add(at, label+".service", "required for probe checks")
			} else if idx, ok := names[ch.Service]; !ok {
				v.add(append(at, "service"), label+".service", "unknown service %q%s", ch.Service, suggest(ch.Service, names))
			} else if c.Services[idx].Health == nil {
				v.add(append(at, "service"), label+".service", "service %q has no health probe to re-run", ch.Service)
			}
		case CheckCommand:
			if strings.TrimSpace(ch.Run) == "" {
				v.add(at, label+".run", "required for command checks")
			}
			if ch.Timeout < 0 {
				v.add(append(at, "timeout"), label+".timeout", "must be positive")
			}
		case "":
			v.add(at, label+".type", "required (%q or %q)", CheckProbe, CheckCommand)
		default:
			v.add(append(at, "type"), label+".type", "unknown check type %q (use %q or %q)", ch.Type, CheckProbe, CheckCommand)
		}
	}

	sort.SliceStable(v.issues, func(a, b int) bool { return v.issues[a].Line < v.issues[b].Line })
	return v.issues
}

func (v *validator) health(at []any, label string, s Service) {
	h := s.Health
	p := func(k string) []any { return append(append([]any{}, at...), k) }
	switch h.Type {
	case HealthHTTP:
		if h.URL == "" {
			v.add(at, label+".url", "required for http probes")
		} else if u, err := url.Parse(h.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			v.add(p("url"), label+".url", "must be an absolute http:// or https:// URL")
		}
		if h.Address != "" {
			v.add(p("address"), label+".address", "not used by http probes (use url)")
		}
		if h.ExpectStatus != "" {
			if m := statusRe.FindStringSubmatch(h.ExpectStatus); m == nil || (m[2] != "" && m[2] < m[1]) {
				v.add(p("expect_status"), label+".expect_status", "must be a status code like \"200\" or a range like \"200-399\"")
			}
		}
	case HealthTCP:
		if h.Address == "" {
			v.add(at, label+".address", "required for tcp probes (host:port)")
		} else if host, port, err := net.SplitHostPort(h.Address); err != nil || host == "" || !validPort(port) {
			v.add(p("address"), label+".address", "must be host:port, e.g. localhost:5432")
		}
	case HealthProcess:
		if s.Command == "" {
			v.add(p("type"), label+".type", "process probes need the service to have a command")
		}
	case "":
		v.add(at, label+".type", "required (%q, %q or %q)", HealthHTTP, HealthTCP, HealthProcess)
		return
	default:
		v.add(p("type"), label+".type", "unknown probe type %q (use %q, %q or %q)", h.Type, HealthHTTP, HealthTCP, HealthProcess)
		return
	}
	if h.Type != HealthHTTP {
		if h.URL != "" {
			v.add(p("url"), label+".url", "only used by http probes")
		}
		if h.ExpectStatus != "" || h.BodyContains != "" {
			v.add(at, label, "expect_status and body_contains only apply to http probes")
		}
	}
	if h.Interval < 0 || h.Timeout < 0 || h.StartupGrace < 0 {
		v.add(at, label, "durations must be positive")
	}
	if h.Retries < 0 {
		v.add(p("retries"), label+".retries", "must be 0 or more")
	}
	interval, timeout := h.Interval, h.Timeout
	if interval == 0 {
		interval = Duration(DefaultHealthInterval)
	}
	if timeout == 0 {
		timeout = Duration(DefaultHealthTimeout)
	}
	if timeout > interval {
		v.add(p("timeout"), label+".timeout", "timeout (%s) must not exceed interval (%s)", timeout, interval)
	}
}

func validPort(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n > 0 && n <= 65535
}

func serviceLabel(i int, name string) string {
	if name != "" && serviceNameRe.MatchString(name) {
		return "services." + name
	}
	return fmt.Sprintf("services[%d]", i)
}

// suggest returns a "did you mean" hint for a close service name.
func suggest(name string, names map[string]int) string {
	best, bestD := "", 3
	for n := range names {
		if d := editDistance(strings.ToLower(name), strings.ToLower(n)); d < bestD || (d == bestD && n < best) {
			best, bestD = n, d
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf(" (did you mean %q?)", best)
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// findCycle returns one dependency cycle as a closed path (a, b, a), or nil.
// Unknown dependency names are ignored here; they are reported separately.
func findCycle(services []Service) []string {
	deps := map[string][]string{}
	var order []string
	for _, s := range services {
		if _, ok := deps[s.Name]; !ok {
			order = append(order, s.Name)
		}
		deps[s.Name] = append(deps[s.Name], s.DependsOn...)
	}
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var stack []string
	var cycle []string
	var visit func(n string) bool
	visit = func(n string) bool {
		color[n] = grey
		stack = append(stack, n)
		for _, d := range deps[n] {
			if _, known := deps[d]; !known || d == n {
				continue
			}
			switch color[d] {
			case grey:
				for i, s := range stack {
					if s == d {
						cycle = append(append([]string{}, stack[i:]...), d)
						return true
					}
				}
			case white:
				if visit(d) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return false
	}
	for _, n := range order {
		if color[n] == white && visit(n) {
			return cycle
		}
	}
	return nil
}
