// Package diagnosis turns incidents into ranked, evidence-backed hypotheses.
package diagnosis

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Aareevs/Kestrel-CLI/internal/diagnosis/rules"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// maxEvidence bounds evidence lines per hypothesis (after collapsing repeats).
const maxEvidence = 6

// Diagnose runs every rule against one incident.
func Diagnose(c *rules.Context) api.IncidentDiagnosis {
	return DiagnoseWith(c, rules.All())
}

// DiagnoseWith runs the given rules (tests use subsets).
func DiagnoseWith(c *rules.Context, rs []rules.Rule) api.IncidentDiagnosis {
	in := c.Incident
	d := api.IncidentDiagnosis{Incident: in.Incident, Hypotheses: []api.Hypothesis{}, Symptoms: []api.Symptom{}}

	for _, r := range rs {
		for _, h := range r.Evaluate(c) {
			// The invariant: no hypothesis without evidence.
			if len(h.Supporting) == 0 {
				continue
			}
			for _, e := range h.Supporting {
				if e.EventID == "" {
					panic(fmt.Sprintf("diagnosis: rule %s cited evidence without an event ID", r.ID()))
				}
			}
			if h.Claim == "" {
				h.Claim = api.ClaimHypothesis
			}
			if h.Contradicting == nil {
				h.Contradicting = []api.Evidence{}
			}
			if len(h.Supporting) > maxEvidence {
				h.Supporting = h.Supporting[:maxEvidence]
			}
			d.Hypotheses = append(d.Hypotheses, h)
		}
	}

	score := func(h api.Hypothesis) int {
		s := h.Confidence.Level.Rank() * 100
		if in.InitiatingKnown && len(h.Services) > 0 && h.Services[0] == in.InitiatingService {
			s += 30 // about the failure the evidence says came first
		}
		s += 5 * len(h.Services) // explains more of the incident
		if len(h.Contradicting) > 0 {
			s -= 20
		}
		return s
	}
	// Ties: the hypothesis whose evidence starts earlier, then rule ID.
	sort.SliceStable(d.Hypotheses, func(i, j int) bool {
		a, b := d.Hypotheses[i], d.Hypotheses[j]
		if sa, sb := score(a), score(b); sa != sb {
			return sa > sb
		}
		if a.Supporting[0].Time != b.Supporting[0].Time {
			return a.Supporting[0].Time.Before(b.Supporting[0].Time)
		}
		return a.RuleID < b.RuleID
	})
	for i := range d.Hypotheses {
		d.Hypotheses[i].Rank = i + 1
	}

	d.Symptoms = symptoms(in.Timeline)

	// Status: a hypothesis only when one failure can be singled out and some
	// hypothesis is at least medium confidence.
	d.Status = api.ClaimHypothesis
	switch {
	case len(d.Hypotheses) == 0:
		d.Status = api.ClaimUnresolved
		d.NextStep = api.NextStep{Description: "No rule matched this incident. Read the incident timeline.", Command: "kestrel timeline"}
	case !in.InitiatingKnown:
		d.Status = api.ClaimUnresolved
		first := earliestRoot(c)
		d.NextStep = api.NextStep{
			Description: fmt.Sprintf("%d services failed independently, so the evidence can't single out one cause. Check each; start with the earliest, %s.", len(in.Roots), first),
			Command:     "kestrel timeline --service " + first,
		}
	case d.Hypotheses[0].Confidence.Level.Rank() < api.ConfidenceMedium.Rank():
		d.Status = api.ClaimUnresolved
		d.NextStep = d.Hypotheses[0].NextStep
	default:
		d.NextStep = d.Hypotheses[0].NextStep
	}
	return d
}

func earliestRoot(c *rules.Context) string {
	best := ""
	var bestSeq uint64
	for _, r := range c.Incident.Roots {
		if f := c.Incident.First[r]; best == "" || f.Seq < bestSeq {
			best, bestSeq = r, f.Seq
		}
	}
	return best
}

// symptoms summarizes downstream-symptom timeline entries per service,
// totalling each kind of symptom (a run split by other events counts once).
func symptoms(tl []api.TimelineEntry) []api.Symptom {
	type kind struct {
		entry api.TimelineEntry
		count int
	}
	type acc struct {
		kinds []*kind
		byKey map[string]*kind
		ids   []string
	}
	var order []string
	by := map[string]*acc{}
	for _, e := range tl {
		if e.Role != api.RoleSymptom {
			continue
		}
		a := by[e.Service]
		if a == nil {
			a = &acc{byKey: map[string]*kind{}}
			by[e.Service] = a
			order = append(order, e.Service)
		}
		key := string(e.Type)
		if e.Type == api.EventLogLine {
			key += "|" + e.Message
		}
		k := a.byKey[key]
		if k == nil {
			k = &kind{entry: e}
			a.byKey[key] = k
			a.kinds = append(a.kinds, k)
		}
		k.count += e.Count
		a.ids = append(a.ids, e.EventID)
	}
	out := []api.Symptom{}
	for _, s := range order {
		a := by[s]
		var parts []string
		for _, k := range a.kinds {
			parts = append(parts, symptomText(k.entry, k.count))
		}
		out = append(out, api.Symptom{Service: s, Summary: strings.Join(parts, "; "), EventIDs: a.ids, Claim: api.ClaimCorrelated})
	}
	return out
}

func symptomText(e api.TimelineEntry, n int) string {
	count := ""
	if n > 1 {
		count = fmt.Sprintf(" ×%d", n)
	}
	switch e.Type {
	case api.EventDependencyConnFailed:
		return "connection errors" + count
	case api.EventStartupTimeout:
		return "never became ready"
	case api.EventProbeFailed:
		return "readiness probe failing"
	case api.EventProcessCrashed:
		return "crashed"
	case api.EventPortBindFailed:
		return "could not bind its port"
	}
	msg := e.Message
	if r := []rune(msg); len(r) > 60 {
		msg = string(r[:59]) + "…"
	}
	return msg + count
}
