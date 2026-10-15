// Package correlation groups anomalies into incidents and builds their
// timelines.
//
// Links between anomalies are deterministic and ranked:
//
//   - strong: same service, a dependency path in the graph (either direction),
//     or a connection failure aimed at a port another member owns;
//   - weak:   only that they happened within the correlation window.
//
// Weak links may put anomalies in the same incident (so simultaneous failures
// are looked at together), but they never become dependency edges and never
// count as causal evidence.
package correlation

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/graph"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// DefaultWindow matches the config default.
const DefaultWindow = 10 * time.Second

// IsAnomaly reports whether an event indicates something going wrong.
// Warnings alone are not anomalies: a noisy but healthy service has no
// incident.
func IsAnomaly(e api.Event) bool {
	switch e.Type {
	case api.EventProcessCrashed, api.EventStartupTimeout, api.EventProbeFailed,
		api.EventPortBindFailed, api.EventDependencyConnFailed, api.EventTestFailed:
		return true
	case api.EventLogLine:
		return e.Severity.Rank() >= api.SeverityError.Rank()
	}
	return false
}

// Incident is a correlated group of anomalies plus what diagnosis needs.
type Incident struct {
	api.Incident
	Anomalies []api.Event            // in seq order
	ByService map[string][]api.Event // anomalies per service
	First     map[string]api.Event   // first anomaly per service
	Roots     []string               // members not explained by an earlier-failing dependency
	Weak      bool                   // joined at least once by time alone
	last      time.Time
}

// Members returns the services involved, sorted.
func (in *Incident) Members() []string {
	out := make([]string, 0, len(in.ByService))
	for s := range in.ByService {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func (in *Incident) has(svc string) bool { _, ok := in.ByService[svc]; return ok }

func (in *Incident) add(e api.Event) {
	in.Anomalies = append(in.Anomalies, e)
	if _, ok := in.First[e.ServiceID]; !ok {
		in.First[e.ServiceID] = e
	}
	in.ByService[e.ServiceID] = append(in.ByService[e.ServiceID], e)
	in.last = e.Time
}

// strongLink reports whether e is strongly linked to the incident.
func (in *Incident) strongLink(e api.Event, g *graph.Graph) bool {
	if in.has(e.ServiceID) {
		return true
	}
	for svc := range in.ByService {
		if g.Related(e.ServiceID, svc) {
			return true
		}
	}
	if e.Type == api.EventDependencyConnFailed {
		if owner, ok := g.PortOwner(graph.ConnPort(e)); ok && in.has(owner) {
			return true
		}
	}
	// A member's connection failure aimed at e's service links them too.
	for _, m := range in.Anomalies {
		if m.Type == api.EventDependencyConnFailed {
			if owner, ok := g.PortOwner(graph.ConnPort(m)); ok && owner == e.ServiceID {
				return true
			}
		}
	}
	return false
}

// Correlate groups the anomalies among events (seq order) into incidents.
// An anomaly joins the current incident if it arrives within the window of
// the incident's latest anomaly; the link is recorded as strong or weak.
// After a quiet gap longer than the window, the next anomaly starts a new
// incident.
func Correlate(events []api.Event, g *graph.Graph, window time.Duration) []*Incident {
	if window <= 0 {
		window = DefaultWindow
	}
	var all []*Incident
	var cur *Incident
	for _, e := range events {
		if !IsAnomaly(e) {
			continue
		}
		if cur != nil && e.Time.Sub(cur.last) <= window {
			if !cur.strongLink(e, g) {
				cur.Weak = true // linked only by time
			}
			cur.add(e)
			continue
		}
		cur = &Incident{ByService: map[string][]api.Event{}, First: map[string]api.Event{}}
		cur.add(e)
		all = append(all, cur)
	}
	for i, in := range all {
		in.Number = i + 1
		in.finish(events, g)
	}
	return all
}

// finish computes roots, the initiating event, and the timeline.
func (in *Incident) finish(events []api.Event, g *graph.Graph) {
	in.Start, in.End = in.Anomalies[0].Time, in.Anomalies[len(in.Anomalies)-1].Time
	in.Services = in.Members()
	in.EventIDs = make([]string, len(in.Anomalies))
	for i, e := range in.Anomalies {
		in.EventIDs[i] = e.ID
	}
	in.CorrelationStrength = api.CorrelationStrong
	if in.Weak {
		in.CorrelationStrength = api.CorrelationWeak
	}

	// A member is explained when it depends on another member that failed
	// earlier. The unexplained ones are root candidates.
	for _, s := range in.Services {
		explained := false
		for _, u := range in.Services {
			if u != s && g.DependsOn(s, u) && in.First[u].Seq < in.First[s].Seq {
				explained = true
				break
			}
		}
		if !explained {
			in.Roots = append(in.Roots, s)
		}
	}
	if len(in.Roots) == 1 {
		first := in.First[in.Roots[0]]
		in.InitiatingKnown, in.InitiatingService, in.InitiatingEventID = true, first.ServiceID, first.ID
	} else {
		in.InitiatingNote = fmt.Sprintf("%d services failed with no dependency or port link between them (%s); the initiating failure can't be determined from this evidence",
			len(in.Roots), strings.Join(in.Roots, ", "))
	}
	in.Timeline = in.timeline(events)
}

func (in *Incident) isRoot(svc string) bool {
	for _, r := range in.Roots {
		if r == svc {
			return true
		}
	}
	return false
}

// timeline: last healthy state, first anomaly, key events, downstream
// symptoms, recovery. Consecutive repeats per service collapse into one entry.
func (in *Incident) timeline(events []api.Event) []api.TimelineEntry {
	var out []api.TimelineEntry
	entry := func(e api.Event, role string, claim api.ClaimLevel) api.TimelineEntry {
		n := 1 + e.Attrs.Repeats
		te := api.TimelineEntry{EventID: e.ID, Seq: e.Seq, Time: e.Time, Count: n, Service: e.ServiceID,
			Type: e.Type, Severity: e.Severity, Message: e.Message, Role: role, Claim: claim}
		if e.Attrs.Repeats > 0 {
			te.Until = e.Time
			te.Time = e.Time.Add(-time.Duration(e.Attrs.DurationMS) * time.Millisecond)
		}
		return te
	}

	// Last known healthy state per member, before the incident began.
	for _, s := range in.Services {
		var last *api.Event
		for i := range events {
			e := events[i]
			if e.Seq >= in.Anomalies[0].Seq {
				break
			}
			if e.ServiceID == s && (e.Type == api.EventProbeReady || e.Type == api.EventProbeRecovered || e.Type == api.EventProcessStarted) {
				last = &events[i]
			}
		}
		if last != nil {
			out = append(out, entry(*last, api.RoleHealthy, api.ClaimObserved))
		}
	}

	firstSeq := in.Anomalies[0].Seq
	lastByService := map[string]int{} // index of the service's latest anomaly entry
	lastFP := map[string]string{}     // and that entry's fingerprint
	for _, e := range in.Anomalies {
		role, claim := api.RoleKey, api.ClaimObserved
		switch {
		case e.Seq == firstSeq:
			role = api.RoleFirstAnomaly
		case in.InitiatingKnown && !in.isRoot(e.ServiceID):
			role, claim = api.RoleSymptom, api.ClaimCorrelated
		}
		if i, ok := lastByService[e.ServiceID]; ok && role != api.RoleFirstAnomaly &&
			out[i].Type == e.Type && e.Fingerprint != "" && lastFP[e.ServiceID] == e.Fingerprint {
			// Same failure repeating: extend the previous entry.
			out[i].Count += 1 + e.Attrs.Repeats
			out[i].Until = e.Time
			continue
		}
		out = append(out, entry(e, role, claim))
		lastByService[e.ServiceID], lastFP[e.ServiceID] = len(out)-1, e.Fingerprint
	}

	// Recovery: a member's probe passing again after that member's own first
	// anomaly. (A service that only became ready, having never failed, did
	// not recover from anything.)
	for _, e := range events {
		first, member := in.First[e.ServiceID]
		if member && e.Seq > first.Seq && (e.Type == api.EventProbeRecovered || e.Type == api.EventProbeReady) {
			out = append(out, entry(e, api.RoleRecovery, api.ClaimObserved))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}
