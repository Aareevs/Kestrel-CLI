package app

import (
	"testing"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/correlation"
	"github.com/Aareevs/Kestrel-CLI/internal/graph"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func ev(seq int, svc string, typ api.EventType, sev api.Severity, fp string) api.Event {
	return api.Event{ID: "E" + string(rune('A'+seq)), Seq: uint64(seq), Time: t0.Add(time.Duration(seq) * time.Second),
		ServiceID: svc, Type: typ, Severity: sev, Message: string(typ), Fingerprint: fp}
}

func incidents(evs []api.Event) []*correlation.Incident {
	return correlation.Correlate(evs, graph.Build(api.Topology{}, evs), 5*time.Second)
}

func TestPickIncident(t *testing.T) {
	evs := []api.Event{
		ev(1, "db", api.EventProcessCrashed, api.SeverityError, ""),
		ev(30, "api", api.EventProbeFailed, api.SeverityError, ""), // far apart: a second incident
	}
	ins := incidents(evs)
	if len(ins) != 2 {
		t.Fatalf("%d incidents", len(ins))
	}
	if in, n, err := pickIncident(ins, 0); err != nil || n != 2 || !in.Has("api") {
		t.Fatalf("latest: %v %d %v", in, n, err)
	}
	if in, n, err := pickIncident(ins, 1); err != nil || n != 1 || !in.Has("db") {
		t.Fatalf("#1: %v %d %v", in, n, err)
	}
	if _, _, err := pickIncident(ins, 3); err == nil {
		t.Fatal("#3 should not exist")
	}
	if in, n, err := pickIncident(nil, 0); in != nil || n != 0 || err != nil {
		t.Fatal("no incidents")
	}
}

func TestRecoveryAndAfterEvidence(t *testing.T) {
	evs := []api.Event{
		ev(1, "api", api.EventProbeFailed, api.SeverityError, ""),
		ev(2, "api", api.EventProbeRecovered, api.SeverityInfo, ""),
	}
	ins := incidents(evs)
	rec := recoveryEvidence(ins[0])
	if len(rec) != 1 || rec[0].EventID != evs[1].ID {
		t.Fatalf("recovery: %+v", rec)
	}

	// During the window: api errors (two of them identical, plus a repeat
	// summary standing for 4 lines), an info line, and another service's error.
	repeat := ev(6, "api", api.EventLogLine, api.SeverityError, "fp1")
	repeat.Attrs.Repeats = 4
	window := []api.Event{
		ev(3, "api", api.EventLogLine, api.SeverityError, "fp1"),
		ev(4, "api", api.EventLogLine, api.SeverityInfo, "fp2"),
		ev(5, "worker", api.EventLogLine, api.SeverityError, "fp3"),
		repeat,
		ev(7, "api", api.EventProbeFailed, api.SeverityError, "fp4"),
	}
	after := afterEvidence(window, ins[0])
	if len(after) != 2 || after[0].Count != 5 || after[1].EventID != window[4].ID {
		t.Fatalf("after (incident services only, repeats counted): %+v", after)
	}
	// With no incident, any service's anomaly counts.
	if got := afterEvidence(window, nil); len(got) != 3 {
		t.Fatalf("after without incident: %+v", got)
	}
}
