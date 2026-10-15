// Package graph is the service dependency graph used by correlation and
// diagnosis.
//
// Edges come from two places only: depends_on in the config (declared), and
// connection errors that name a port owned by another service (inferred, with
// the events as evidence). Two services failing close together never creates
// an edge.
package graph

import (
	"sort"
	"strconv"
	"strings"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

const maxEdgeEvidence = 3

// Graph is a directed graph: an edge From → To means From depends on To.
type Graph struct {
	services map[string]bool
	external map[string]bool
	edges    []api.GraphEdge
	out      map[string][]int // service → indexes into edges
	owners   map[int]string   // port → owning service ("" if contested)
}

// Build derives the graph from a session's topology and events.
func Build(topo api.Topology, events []api.Event) *Graph {
	g := &Graph{services: map[string]bool{}, external: map[string]bool{}, out: map[string][]int{}, owners: map[int]string{}}
	claims := map[int]map[string]bool{}
	claim := func(port int, svc string) {
		if port <= 0 || svc == "" {
			return
		}
		if claims[port] == nil {
			claims[port] = map[string]bool{}
		}
		claims[port][svc] = true
	}
	for _, s := range topo.Services {
		g.services[s.Name] = true
		if s.External {
			g.external[s.Name] = true
		}
		for _, p := range s.Ports {
			claim(p, s.Name)
		}
	}
	for _, e := range events {
		if e.ServiceID != "" {
			g.services[e.ServiceID] = true
		}
		// A service announcing its own listen address owns that port.
		if e.Attrs.PatternID == "L001-listening" {
			claim(e.Attrs.Port, e.ServiceID)
		}
	}
	for port, svcs := range claims {
		if len(svcs) == 1 {
			for s := range svcs {
				g.owners[port] = s
			}
		} else {
			g.owners[port] = "" // two services claim it: no basis to pick one
		}
	}

	for _, s := range topo.Services {
		for _, d := range s.DependsOn {
			g.add(api.GraphEdge{From: s.Name, To: d, Kind: api.EdgeDeclared})
		}
	}
	for _, e := range events {
		if e.Type != api.EventDependencyConnFailed {
			continue
		}
		port := ConnPort(e)
		owner, ok := g.PortOwner(port)
		if !ok || owner == e.ServiceID {
			continue
		}
		if i, exists := g.edge(e.ServiceID, owner); exists {
			if g.edges[i].Kind == api.EdgeInferred && len(g.edges[i].Evidence) < maxEdgeEvidence {
				g.edges[i].Evidence = append(g.edges[i].Evidence, e.ID)
			}
			continue
		}
		g.add(api.GraphEdge{From: e.ServiceID, To: owner, Kind: api.EdgeInferred, Port: port, Evidence: []string{e.ID}})
	}
	return g
}

// ConnPort is the port a connection-failure event was aimed at, or 0.
func ConnPort(e api.Event) int {
	if e.Attrs.Port > 0 {
		return e.Attrs.Port
	}
	if i := strings.LastIndexByte(e.Attrs.Target, ':'); i >= 0 {
		if p, err := strconv.Atoi(e.Attrs.Target[i+1:]); err == nil {
			return p
		}
	}
	return 0
}

func (g *Graph) add(e api.GraphEdge) {
	g.services[e.From], g.services[e.To] = true, true
	g.edges = append(g.edges, e)
	g.out[e.From] = append(g.out[e.From], len(g.edges)-1)
}

func (g *Graph) edge(from, to string) (int, bool) {
	for _, i := range g.out[from] {
		if g.edges[i].To == to {
			return i, true
		}
	}
	return 0, false
}

// Edges returns all edges, sorted for stable output.
func (g *Graph) Edges() []api.GraphEdge {
	out := append([]api.GraphEdge{}, g.edges...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

// Edge returns the direct edge from → to, if any.
func (g *Graph) Edge(from, to string) (api.GraphEdge, bool) {
	i, ok := g.edge(from, to)
	if !ok {
		return api.GraphEdge{}, false
	}
	return g.edges[i], true
}

// DependsOn reports whether from depends on to, directly or transitively.
func (g *Graph) DependsOn(from, to string) bool {
	if from == to {
		return false
	}
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, i := range g.out[n] {
			t := g.edges[i].To
			if t == to {
				return true
			}
			if !seen[t] {
				seen[t] = true
				stack = append(stack, t)
			}
		}
	}
	return false
}

// Related reports whether a and b are linked by a dependency path either way.
func (g *Graph) Related(a, b string) bool { return g.DependsOn(a, b) || g.DependsOn(b, a) }

// PortOwner returns the service that owns port, if exactly one does.
func (g *Graph) PortOwner(port int) (string, bool) {
	s, ok := g.owners[port]
	return s, ok && s != ""
}

// External reports whether a service is probe-only (not started by Kestrel).
func (g *Graph) External(svc string) bool { return g.external[svc] }

// Services returns every known service, sorted.
func (g *Graph) Services() []string {
	out := make([]string, 0, len(g.services))
	for s := range g.services {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
