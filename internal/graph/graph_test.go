package graph

import (
	"testing"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func connFail(id, svc string, port int) api.Event {
	return api.Event{ID: id, ServiceID: svc, Type: api.EventDependencyConnFailed, Attrs: api.Attrs{Port: port}}
}

func listening(svc string, port int) api.Event {
	return api.Event{ServiceID: svc, Type: api.EventLogLine, Attrs: api.Attrs{PatternID: "L001-listening", Port: port}}
}

func TestDeclaredAndInferredEdges(t *testing.T) {
	topo := api.Topology{Services: []api.ServiceTopology{
		{Name: "db", Ports: []int{5432}},
		{Name: "api", DependsOn: []string{"db"}, Ports: []int{8080}},
		{Name: "web"},
		{Name: "cache", External: true, Ports: []int{6379}},
	}}
	g := Build(topo, []api.Event{
		listening("web", 5173),
		connFail("e1", "web", 8080), // web → api, inferred via api's probe port
		connFail("e2", "web", 8080), // more evidence for the same edge
		connFail("e3", "api", 5432), // already declared: no inferred duplicate
		connFail("e4", "api", 6379), // api → external cache
		connFail("e5", "api", 9999), // nobody owns 9999: no edge
		connFail("e6", "web", 5173), // its own port: no self edge
		{ID: "e7", ServiceID: "web", Type: api.EventDependencyConnFailed, Attrs: api.Attrs{Target: "localhost:5432"}}, // port from target
	})
	want := map[string]string{"api→db": api.EdgeDeclared, "web→api": api.EdgeInferred, "api→cache": api.EdgeInferred, "web→db": api.EdgeInferred}
	got := map[string]string{}
	for _, e := range g.Edges() {
		got[e.From+"→"+e.To] = e.Kind
	}
	if len(got) != len(want) {
		t.Fatalf("edges %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("edge %s = %q, want %q", k, got[k], v)
		}
	}
	e, _ := g.Edge("web", "api")
	if e.Port != 8080 || len(e.Evidence) != 2 || e.Evidence[0] != "e1" {
		t.Fatalf("inferred edge evidence: %+v", e)
	}
	// web → db is direct (inferred); cache ← api ← web makes cache and web related.
	if !g.DependsOn("web", "db") || g.DependsOn("db", "web") || !g.Related("db", "web") || !g.Related("cache", "web") {
		t.Fatal("transitive dependency checks")
	}
	if !g.External("cache") || g.External("api") {
		t.Fatal("external flags")
	}
}

func TestContestedPortHasNoOwner(t *testing.T) {
	g := Build(api.Topology{Services: []api.ServiceTopology{{Name: "a", Ports: []int{3000}}, {Name: "b"}, {Name: "c"}}},
		[]api.Event{listening("b", 3000), connFail("x", "c", 3000)})
	if _, ok := g.PortOwner(3000); ok {
		t.Fatal("a port claimed by two services must have no owner")
	}
	if len(g.Edges()) != 0 {
		t.Fatalf("no edge may be inferred from a contested port: %+v", g.Edges())
	}
}

// Failing at the same moment is not a dependency.
func TestSimultaneousFailuresCreateNoEdge(t *testing.T) {
	crash := func(svc string) api.Event {
		return api.Event{ServiceID: svc, Type: api.EventProcessCrashed, Severity: api.SeverityError}
	}
	g := Build(api.Topology{Services: []api.ServiceTopology{{Name: "a"}, {Name: "b"}}}, []api.Event{crash("a"), crash("b")})
	if len(g.Edges()) != 0 || g.Related("a", "b") {
		t.Fatal("simultaneous failures must not create edges")
	}
}
