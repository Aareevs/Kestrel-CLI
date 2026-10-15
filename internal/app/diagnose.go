package app

import (
	"context"
	"fmt"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/correlation"
	"github.com/Aareevs/Kestrel-CLI/internal/diagnosis"
	"github.com/Aareevs/Kestrel-CLI/internal/diagnosis/rules"
	"github.com/Aareevs/Kestrel-CLI/internal/graph"
	"github.com/Aareevs/Kestrel-CLI/internal/storage"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// tailLines is how much output before a failure rules may look at.
const tailLines = 30

// analysis is a session loaded and correlated.
type analysis struct {
	store     *storage.Store
	summary   api.SessionSummary
	events    []api.Event
	graph     *graph.Graph
	incidents []*correlation.Incident
}

// window is correlation.window from the project's config, or the default.
func (a *App) window() time.Duration {
	if cfg, _, err := a.LoadConfig(); err == nil && cfg != nil && cfg.Correlation.Window > 0 {
		return cfg.Correlation.Window.D()
	}
	return correlation.DefaultWindow
}

// analyze loads a session and correlates it. The caller closes the store.
func (a *App) analyze(ctx context.Context, ref string, all bool) (*analysis, error) {
	st, err := a.openStoreRecovered(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s", storageHint(err))
	}
	id, err := a.resolve(ctx, st, ref, all)
	if err != nil {
		st.Close()
		return nil, err
	}
	sum, _, err := st.GetSession(ctx, id)
	if err != nil {
		st.Close()
		return nil, err
	}
	topo, err := st.Topology(ctx, id)
	if err != nil {
		st.Close()
		return nil, err
	}
	evs, err := st.DiagnosticEvents(ctx, id)
	if err != nil {
		st.Close()
		return nil, err
	}
	g := graph.Build(topo, evs)
	return &analysis{store: st, summary: sum, events: evs, graph: g, incidents: correlation.Correlate(evs, g, a.window())}, nil
}

// tails fetches, per member of an incident, the output leading up to its
// first crash or startup failure (or its first anomaly otherwise).
func (an *analysis) tails(ctx context.Context, in *correlation.Incident) (map[string][]api.Event, error) {
	out := map[string][]api.Event{}
	for _, svc := range in.Services {
		at := in.First[svc]
		for _, e := range in.ByService[svc] {
			if e.Type == api.EventProcessCrashed || e.Type == api.EventStartupTimeout {
				at = e
				break
			}
		}
		lines, err := an.store.TailBefore(ctx, an.summary.ID, svc, at.Seq, tailLines)
		if err != nil {
			return nil, err
		}
		out[svc] = lines
	}
	return out, nil
}

func liveNote(sum api.SessionSummary) string {
	if sum.Status == api.SessionActive {
		return "this session is still running; the analysis covers events recorded so far"
	}
	return ""
}

// Diagnose correlates a recorded session into incidents and proposes ranked,
// evidence-backed hypotheses for each.
func (a *App) Diagnose(ctx context.Context, ref string, all bool) (*api.DiagnosisReport, error) {
	an, err := a.analyze(ctx, ref, all)
	if err != nil {
		return nil, err
	}
	defer an.store.Close()
	rep := &api.DiagnosisReport{SchemaVersion: api.SchemaVersion, Session: an.summary, Graph: an.graph.Edges(),
		Incidents: []api.IncidentDiagnosis{}, Note: liveNote(an.summary)}
	if rep.Graph == nil {
		rep.Graph = []api.GraphEdge{}
	}
	for _, in := range an.incidents {
		tail, err := an.tails(ctx, in)
		if err != nil {
			return nil, err
		}
		rep.Incidents = append(rep.Incidents, diagnosis.Diagnose(&rules.Context{Incident: in, Graph: an.graph, Events: an.events, Tail: tail}))
	}
	return rep, nil
}

// TimelineOptions narrow `kestrel timeline`.
type TimelineOptions struct {
	Service string
	// Since keeps only what happened in the last Since of the session.
	Since time.Duration
	// Limit bounds the per-service event list (default 200).
	Limit int
}

// Timeline returns the incident timelines of a session, or with Service set,
// that service's own recent events (all output, not just anomalies).
func (a *App) Timeline(ctx context.Context, ref string, all bool, o TimelineOptions) (*api.TimelineReport, error) {
	an, err := a.analyze(ctx, ref, all)
	if err != nil {
		return nil, err
	}
	defer an.store.Close()
	rep := &api.TimelineReport{SchemaVersion: api.SchemaVersion, Session: an.summary, Graph: an.graph.Edges(),
		Incidents: []api.Incident{}, Service: o.Service}
	if rep.Graph == nil {
		rep.Graph = []api.GraphEdge{}
	}

	var cutoff time.Time
	if o.Since > 0 {
		end := an.summary.EndedAt
		if end.IsZero() {
			end = a.Now()
		}
		cutoff = end.Add(-o.Since)
		rep.Since = cutoff
	}

	if o.Service != "" {
		known := false
		for _, s := range an.graph.Services() {
			if s == o.Service {
				known = true
			}
		}
		if !known {
			return nil, &UsageError{Err: fmt.Errorf("no service %q in session %s (services: %v)", o.Service, an.summary.ID, an.graph.Services())}
		}
		limit := o.Limit
		if limit <= 0 {
			limit = 200
		}
		evs, err := an.store.Events(ctx, an.summary.ID, storage.EventQuery{Service: o.Service, Limit: limit, Newest: true})
		if err != nil {
			return nil, err
		}
		rep.Events = serviceEntries(evs, cutoff)
	}
	for _, in := range an.incidents {
		if !cutoff.IsZero() && in.End.Before(cutoff) {
			continue
		}
		if o.Service != "" && !in.Has(o.Service) {
			continue
		}
		rep.Incidents = append(rep.Incidents, in.Incident)
	}
	return rep, nil
}

// serviceEntries turns a service's events into timeline entries, collapsing
// consecutive repeats.
func serviceEntries(evs []api.Event, cutoff time.Time) []api.TimelineEntry {
	out := []api.TimelineEntry{}
	lastFP := ""
	for _, e := range evs {
		if !cutoff.IsZero() && e.Time.Before(cutoff) {
			continue
		}
		n := e.Lines()
		if len(out) > 0 && e.Fingerprint != "" && e.Fingerprint == lastFP && out[len(out)-1].Type == e.Type {
			out[len(out)-1].Count += n
			out[len(out)-1].Until = e.Time
			continue
		}
		role := api.RoleOutput
		if correlation.IsAnomaly(e) {
			role = api.RoleKey
		}
		out = append(out, api.TimelineEntry{EventID: e.ID, Seq: e.Seq, Time: e.Time, Count: n, Service: e.ServiceID,
			Type: e.Type, Severity: e.Severity, Message: e.Message, Role: role, Claim: api.ClaimObserved})
		lastFP = e.Fingerprint
	}
	return out
}
