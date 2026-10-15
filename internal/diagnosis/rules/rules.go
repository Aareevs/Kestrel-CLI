package rules

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Aareevs/Kestrel-CLI/internal/graph"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// ---------------------------------------------------------------------------
// K001: a service could not bind its port.

type PortConflict struct{}

func (PortConflict) ID() string { return "K001-port-conflict" }

func (r PortConflict) Evaluate(c *Context) []api.Hypothesis {
	var out []api.Hypothesis
	in := c.Incident
	for _, svc := range in.Services {
		bind, ok := firstOf(in, svc, api.EventPortBindFailed)
		if !ok {
			continue
		}
		port := bind.Attrs.Port
		perm := bind.Attrs.PatternID == "P002-bind-permission"
		h := api.Hypothesis{RuleID: r.ID(), Claim: api.ClaimHypothesis, Services: []string{svc}}
		h.Supporting = []api.Evidence{evidence(bind, describe(bind))}
		level := api.ConfidenceMedium
		factors := []string{"the process itself reported the bind failure (pattern " + bind.Attrs.PatternID + ")"}
		for _, e := range in.ByService[svc] {
			if e.Type == api.EventProcessCrashed && e.Seq > bind.Seq {
				h.Supporting = append(h.Supporting, evidence(e, describe(e)))
				factors = append(factors, fmt.Sprintf("%s %s right after", svc, exitDetail(e)))
				level = api.ConfidenceHigh
				break
			}
		}
		for _, e := range c.eventsOf(svc, api.EventProbeReady, api.EventProbeRecovered) {
			if e.Seq > bind.Seq {
				h.Contradicting = append(h.Contradicting, evidence(e, svc+" became ready afterwards, so it did get a port"))
				level = lower(level)
				factors = append(factors, "but it became ready afterwards")
				break
			}
		}
		h.Confidence = api.ConfidenceDetail{Level: level, Factors: factors}
		portText := "its port"
		if port > 0 {
			portText = fmt.Sprintf("port %d", port)
		}
		if perm {
			h.Title = fmt.Sprintf("%s is not allowed to bind %s", svc, portText)
			h.Explanation = fmt.Sprintf("%s tried to listen on %s and the operating system refused permission. Ports below 1024 usually need elevated privileges.", svc, portText)
			h.NextStep = api.NextStep{Description: fmt.Sprintf("Configure %s to use a port above 1024.", svc)}
		} else {
			h.Title = fmt.Sprintf("%s could not start: %s is already in use", svc, portText)
			h.Explanation = fmt.Sprintf("%s tried to listen on %s, and the operating system refused because another process already holds it. "+
				"That process was not started by Kestrel, so Kestrel leaves it alone.", svc, portText)
			h.NextStep = api.NextStep{Description: fmt.Sprintf("See which process holds %s, then stop it yourself or move %s to a free port. Kestrel never signals processes it didn't start.", portText, svc)}
			if port > 0 {
				h.NextStep.Command = listenerCommand(c.goos(), port)
			}
		}
		out = append(out, h)
	}
	return out
}

// ---------------------------------------------------------------------------
// K002: an upstream service failed and its dependents failed after it.

type DependencyDown struct{}

func (DependencyDown) ID() string { return "K002-dependency-down" }

// hardFailure is the first event showing svc itself went down or never came up.
func hardFailure(c *Context, svc string) (api.Event, bool) {
	return firstOf(c.Incident, svc, api.EventProcessCrashed, api.EventStartupTimeout, api.EventProbeFailed, api.EventPortBindFailed)
}

func (r DependencyDown) Evaluate(c *Context) []api.Hypothesis {
	var out []api.Hypothesis
	in := c.Incident
	for _, d := range in.Services {
		fail, ok := hardFailure(c, d)
		if !ok {
			continue
		}
		type dep struct {
			svc      string
			via      string // "" for a direct dependent, else the dependent it goes through
			declared bool
			portHits []api.Event // connection failures aimed at d's port
			after    []api.Event // svc's anomalies after d failed
			before   []api.Event // svc's anomalies before d failed (contradicting)
		}
		split := func(x *dep) {
			for _, e := range in.ByService[x.svc] {
				if e.Seq > fail.Seq {
					x.after = append(x.after, e)
				} else {
					x.before = append(x.before, e)
				}
			}
		}
		// Direct dependents: a declared edge or connection errors aimed at d's port.
		var deps []dep
		included := map[string]bool{d: true}
		for _, s := range in.Services {
			if s == d {
				continue
			}
			x := dep{svc: s}
			if e, ok := c.Graph.Edge(s, d); ok && e.Kind == api.EdgeDeclared {
				x.declared = true
			}
			for _, e := range in.ByService[s] {
				if e.Type == api.EventDependencyConnFailed {
					if owner, ok := c.Graph.PortOwner(graph.ConnPort(e)); ok && owner == d {
						x.portHits = append(x.portHits, e)
					}
				}
			}
			if !x.declared && len(x.portHits) == 0 {
				continue // no direct link: timing alone proves nothing
			}
			split(&x)
			if len(x.after) == 0 {
				continue
			}
			deps = append(deps, x)
			included[s] = true
		}
		// Then the chain: services that depend directly on an included
		// dependent and failed after it (frontend via api via database).
		for grew := true; grew; {
			grew = false
			for _, s := range in.Services {
				if included[s] {
					continue
				}
				for _, x := range deps {
					e, ok := c.Graph.Edge(s, x.svc)
					if !ok || in.First[s].Seq < in.First[x.svc].Seq {
						continue
					}
					y := dep{svc: s, via: x.svc, declared: e.Kind == api.EdgeDeclared}
					split(&y)
					if len(y.after) > 0 {
						deps = append(deps, y)
						included[s], grew = true, true
						break
					}
				}
			}
		}
		if len(deps) == 0 {
			continue
		}

		h := api.Hypothesis{RuleID: r.ID(), Claim: api.ClaimHypothesis, Services: []string{d}}
		// The upstream failure, with the line that preceded it when there is one.
		if line, ok := cause(c.Tail[d]); ok && line.Seq < fail.Seq {
			h.Supporting = append(h.Supporting, evidence(line, describe(line)))
		}
		h.Supporting = append(h.Supporting, evidence(fail, describe(fail)))
		level := api.ConfidenceHigh
		factors := []string{fmt.Sprintf("%s failed first (%s)", d, clock(fail.Time))}
		var names []string
		for _, x := range deps {
			names = append(names, x.svc)
			h.Services = append(h.Services, x.svc)
			switch {
			case x.via != "":
				factors = append(factors, fmt.Sprintf("%s depends on %s, which failed after %s", x.svc, x.via, d))
			case x.declared:
				factors = append(factors, fmt.Sprintf("%s declares depends_on: [%s]", x.svc, d))
			default:
				factors = append(factors, fmt.Sprintf("%s's connection errors target port %d, which %s owns", x.svc, graph.ConnPort(x.portHits[0]), d))
			}
			if len(x.portHits) > 0 {
				h.Supporting = append(h.Supporting, collapsed(x.portHits, describe)...)
			}
			// The dependent's later failures. For a direct dependent the
			// connection errors are already cited above; for one reached through
			// the chain, its errors (aimed at the intermediate) are the evidence.
			var rest []api.Event
			for _, e := range x.after {
				if e.Type != api.EventDependencyConnFailed || x.via != "" {
					rest = append(rest, e)
				}
			}
			h.Supporting = append(h.Supporting, collapsed(rest, describe)...)
			if len(x.before) > 0 {
				h.Contradicting = append(h.Contradicting, collapsed(x.before, func(e api.Event) string {
					return describe(e) + " (before " + d + " failed)"
				})...)
				factors = append(factors, x.svc+" was already failing before "+d+" did")
				level = lower(level)
			}
		}
		// If d itself depends on a member that failed before it, d is not where
		// the trouble started.
		for _, u := range in.Services {
			if uf, ok := in.First[u]; ok && u != d && c.Graph.DependsOn(d, u) && uf.Seq < fail.Seq {
				h.Contradicting = append(h.Contradicting, evidence(uf, describe(uf)+" (a dependency of "+d+", failed earlier)"))
				factors = append(factors, fmt.Sprintf("but %s depends on %s, which failed earlier: %s's failure may itself be a symptom", d, u, d))
				level = api.ConfidenceLow
				break
			}
		}
		h.Confidence = api.ConfidenceDetail{Level: level, Factors: factors}
		h.Title = fmt.Sprintf("%s failed first; %s failed because of it", d, strings.Join(names, ", "))
		h.Explanation = fmt.Sprintf("%s %s at %s. After that, %s failed. The link is a declared dependency or connection errors aimed at %s's port (see factors), not just timing.",
			d, failureVerb(fail), clock(fail.Time), strings.Join(names, " and "), d)
		h.NextStep = timelineStep(d, fmt.Sprintf("Check why %s failed; the others are likely to recover once it does.", d))
		out = append(out, h)
	}
	return out
}

func failureVerb(e api.Event) string {
	switch e.Type {
	case api.EventProcessCrashed:
		return exitDetail(e)
	case api.EventStartupTimeout:
		return "never became ready"
	case api.EventProbeFailed:
		return "stopped passing its readiness probe"
	case api.EventPortBindFailed:
		return "could not bind its port"
	}
	return "failed"
}

// ---------------------------------------------------------------------------
// K003: a service that had passed its readiness probe later crashed.

type CrashAfterStart struct{}

func (CrashAfterStart) ID() string { return "K003-crash-after-start" }

func (r CrashAfterStart) Evaluate(c *Context) []api.Hypothesis {
	var out []api.Hypothesis
	for _, svc := range c.Incident.Services {
		crash, ok := firstOf(c.Incident, svc, api.EventProcessCrashed)
		if !ok {
			continue
		}
		ready, wasReady := c.readyBefore(svc, crash.Seq)
		if !wasReady {
			continue // never healthy: K004's territory
		}
		h := api.Hypothesis{RuleID: r.ID(), Claim: api.ClaimHypothesis, Services: []string{svc}}
		h.Supporting = []api.Evidence{evidence(ready, describe(ready))}
		factors := []string{"it had passed its readiness probe, so this is a runtime failure, not a startup problem"}
		level := api.ConfidenceLow
		facts := fmt.Sprintf("Observed: %s was ready at %s and %s at %s.", svc, clock(ready.Time), exitDetail(crash), clock(crash.Time))
		line, found := cause(c.Tail[svc])
		if found {
			h.Supporting = append(h.Supporting, evidence(line, describe(line)))
		}
		h.Supporting = append(h.Supporting, evidence(crash, describe(crash)))
		switch {
		case found && line.Severity == api.SeverityFatal:
			level = api.ConfidenceHigh
			factors = append(factors, "a fatal error was logged just before the exit"+patternNote(line))
		case found:
			level = api.ConfidenceMedium
			factors = append(factors, "an error was logged just before the exit"+patternNote(line))
		default:
			factors = append(factors, "nothing in its output explains the exit")
		}
		h.Confidence = api.ConfidenceDetail{Level: level, Factors: factors}
		if found {
			h.Title = fmt.Sprintf("%s crashed after a healthy start: %s", svc, shorten(line.Message, 70))
			h.Explanation = fmt.Sprintf("%s Hypothesis: the crash was caused by what it logged just before exiting: %q.", facts, shorten(line.Message, 160))
		} else {
			h.Title = fmt.Sprintf("%s crashed after a healthy start (cause not visible in its output)", svc)
			h.Explanation = facts + " Its output before the exit shows no error, so the cause can't be read from here (possible: killed externally, out of memory, or a silent exit)."
		}
		h.NextStep = timelineStep(svc, fmt.Sprintf("Read what %s printed just before it exited.", svc))
		out = append(out, h)
	}
	return out
}

func patternNote(e api.Event) string {
	if d := patternDescription(e.Attrs.PatternID); d != "" {
		return " (" + d + ")"
	}
	return ""
}

// ---------------------------------------------------------------------------
// K004: a service failed before it ever became ready.

type StartupFailure struct{}

func (StartupFailure) ID() string { return "K004-startup-failure" }

func (r StartupFailure) Evaluate(c *Context) []api.Hypothesis {
	var out []api.Hypothesis
	in := c.Incident
	for _, svc := range in.Services {
		fail, ok := firstOf(in, svc, api.EventProcessCrashed, api.EventStartupTimeout)
		if !ok {
			continue
		}
		if _, wasReady := c.readyBefore(svc, fail.Seq); wasReady {
			continue // was healthy first: K003's territory
		}
		if _, bind := firstOf(in, svc, api.EventPortBindFailed); bind {
			continue // K001 explains it more precisely
		}
		h := api.Hypothesis{RuleID: r.ID(), Claim: api.ClaimHypothesis, Services: []string{svc}}
		line, found := cause(c.Tail[svc])
		if found {
			h.Supporting = append(h.Supporting, evidence(line, describe(line)))
		}
		h.Supporting = append(h.Supporting, evidence(fail, describe(fail)))

		crashed := fail.Type == api.EventProcessCrashed
		level := api.ConfidenceLow
		var factors []string
		switch {
		case crashed && found:
			level = api.ConfidenceHigh
			factors = append(factors, fmt.Sprintf("%s %s before ever becoming ready", svc, exitDetail(fail)), "it logged an error just before exiting"+patternNote(line))
		case crashed:
			level = api.ConfidenceMedium
			factors = append(factors, fmt.Sprintf("%s %s before ever becoming ready", svc, exitDetail(fail)), "its output shows no error before the exit")
		case found:
			level = api.ConfidenceMedium
			factors = append(factors, "its readiness probe never passed", "it logged errors while starting"+patternNote(line))
		default:
			factors = append(factors, "its readiness probe never passed", "no error output explains why")
		}
		// If svc was itself failing to reach another member that failed first,
		// its startup failure may be a symptom rather than a cause.
		for _, u := range in.Services {
			uf, ok := hardFailure(c, u)
			if u == svc || !ok || uf.Seq > fail.Seq {
				continue
			}
			var hits []api.Event
			for _, e := range in.ByService[svc] {
				if owner, ok := c.Graph.PortOwner(graph.ConnPort(e)); e.Type == api.EventDependencyConnFailed && ok && owner == u {
					hits = append(hits, e)
				}
			}
			if len(hits) > 0 || c.Graph.DependsOn(svc, u) {
				if len(hits) > 0 {
					h.Contradicting = append(h.Contradicting, collapsed(hits, func(e api.Event) string {
						return describe(e) + " (" + u + " had already failed)"
					})...)
				} else {
					h.Contradicting = append(h.Contradicting, evidence(uf, describe(uf)+" (a dependency, failed first)"))
				}
				factors = append(factors, fmt.Sprintf("but %s depends on %s, which failed first: this may be a downstream symptom", svc, u))
				level = api.ConfidenceLow
				break
			}
		}
		h.Confidence = api.ConfidenceDetail{Level: level, Factors: factors}
		switch {
		case found:
			h.Title = fmt.Sprintf("%s failed during startup: %s", svc, shorten(line.Message, 70))
		case crashed:
			h.Title = fmt.Sprintf("%s %s during startup", svc, exitDetail(fail))
		default:
			h.Title = fmt.Sprintf("%s never became ready", svc)
		}
		facts := fmt.Sprintf("Observed: %s %s at %s without ever passing its readiness probe.", svc, failureVerb(fail), clock(fail.Time))
		if found {
			h.Explanation = fmt.Sprintf("%s Hypothesis: it failed because of what it logged while starting: %q.", facts, shorten(line.Message, 160))
		} else {
			h.Explanation = facts + " Its output doesn't say why."
		}
		h.NextStep = timelineStep(svc, fmt.Sprintf("Read %s's startup output for the reason it stopped.", svc))
		out = append(out, h)
	}
	return out
}

// ---------------------------------------------------------------------------
// K005: connections to something no supervised service provides.

type DependencyNotRunning struct{}

func (DependencyNotRunning) ID() string { return "K005-dependency-not-running" }

func (r DependencyNotRunning) Evaluate(c *Context) []api.Hypothesis {
	var out []api.Hypothesis
	in := c.Incident
	for _, svc := range in.Services {
		byPort := map[int][]api.Event{}
		var ports []int
		for _, e := range in.ByService[svc] {
			if e.Type != api.EventDependencyConnFailed {
				continue
			}
			p := graph.ConnPort(e)
			if _, seen := byPort[p]; !seen {
				ports = append(ports, p)
			}
			byPort[p] = append(byPort[p], e)
		}
		sort.Ints(ports)
		for _, p := range ports {
			if p == 0 {
				continue // target unknown: nothing to say about what isn't running
			}
			hits := byPort[p]
			owner, owned := c.Graph.PortOwner(p)
			if owned && !c.Graph.External(owner) {
				continue // a supervised service owns it: K002 covers that link
			}
			target := targetOf(hits[0])
			h := api.Hypothesis{RuleID: r.ID(), Claim: api.ClaimHypothesis, Services: []string{svc}}
			h.Supporting = collapsed(hits, describe)
			n := 0
			for _, e := range hits {
				n += e.Lines()
			}
			level := api.ConfidenceMedium
			factors := []string{fmt.Sprintf("%s failed to connect to %s %d time(s)", svc, target, n)}
			if owned { // a probe-only service Kestrel watches but didn't start
				h.Services = append(h.Services, owner)
				for _, e := range c.eventsOf(owner, api.EventProbeFailed, api.EventStartupTimeout) {
					h.Supporting = append(h.Supporting, evidence(e, describe(e)))
					level = api.ConfidenceHigh
					factors = append(factors, owner+"'s own probe fails too")
					break
				}
				h.Title = fmt.Sprintf("%s can't reach %s, which Kestrel doesn't start", svc, owner)
				h.Explanation = fmt.Sprintf("%s's connections to %s (%s) fail. %s is observed but not started by Kestrel, so it has to be running already.", svc, owner, target, owner)
				h.NextStep = api.NextStep{Description: fmt.Sprintf("Start %s (or check it's listening where %s expects).", owner, svc)}
			} else {
				factors = append(factors, fmt.Sprintf("no service in this session listens on port %d", p))
				h.Title = fmt.Sprintf("%s's dependency at %s isn't running", svc, target)
				h.Explanation = fmt.Sprintf("%s keeps trying to reach %s and nothing accepts the connection. No service Kestrel knows about provides that port, so either the dependency isn't started or %s is pointed at the wrong host/port.", svc, target, svc)
				h.NextStep = api.NextStep{Description: fmt.Sprintf("Start whatever should listen on port %d, or check %s's connection settings (e.g. DATABASE_URL).", p, svc)}
			}
			if p > 0 {
				h.NextStep.Command = listenerCommand(c.goos(), p)
			}
			h.Confidence = api.ConfidenceDetail{Level: level, Factors: factors}
			out = append(out, h)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// K006: a module or package the service needs is missing.

type MissingModule struct{}

func (MissingModule) ID() string { return "K006-missing-module" }

func (r MissingModule) Evaluate(c *Context) []api.Hypothesis {
	var out []api.Hypothesis
	for _, svc := range c.Incident.Services {
		var hits []api.Event
		for _, e := range c.Incident.ByService[svc] {
			if e.Attrs.PatternID == "P013-module-not-found" {
				hits = append(hits, e)
			}
		}
		if len(hits) == 0 {
			continue
		}
		h := api.Hypothesis{RuleID: r.ID(), Claim: api.ClaimHypothesis, Services: []string{svc}}
		h.Supporting = collapsed(hits, describe)
		level := api.ConfidenceMedium
		factors := []string{"the runtime reported a missing module or package (pattern P013)"}
		if crash, ok := firstOf(c.Incident, svc, api.EventProcessCrashed); ok && crash.Seq > hits[0].Seq {
			h.Supporting = append(h.Supporting, evidence(crash, describe(crash)))
			level = api.ConfidenceHigh
			factors = append(factors, fmt.Sprintf("%s %s right after", svc, exitDetail(crash)))
		}
		h.Confidence = api.ConfidenceDetail{Level: level, Factors: factors}
		h.Title = fmt.Sprintf("%s is missing a dependency: %s", svc, shorten(hits[0].Message, 70))
		h.Explanation = fmt.Sprintf("%s tried to load code that isn't installed or isn't on its path: %q.", svc, shorten(hits[0].Message, 160))
		h.NextStep = api.NextStep{Description: fmt.Sprintf("Install %s's dependencies (e.g. npm install, pip install -r requirements.txt, go mod download) and check the import path.", svc)}
		out = append(out, h)
	}
	return out
}
