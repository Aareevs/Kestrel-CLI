package output

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func longDiagnosis() *api.DiagnosisReport {
	ev := func(id, svc, text string, n int) api.Evidence {
		return api.Evidence{EventID: id, Time: t0, Service: svc, Summary: text, Count: n}
	}
	return &api.DiagnosisReport{
		Session: api.SessionSummary{ID: "01JTESTSESSION", Mode: "watch", Status: "closed", StartedAt: t0, EndedAt: t0.Add(time.Minute), Services: []string{"database", "api"}},
		Incidents: []api.IncidentDiagnosis{{
			Incident: api.Incident{Number: 1, Start: t0, Services: []string{"database", "api"}, CorrelationStrength: "strong", InitiatingKnown: true, InitiatingService: "database", InitiatingEventID: "01JEVENT00000000000000DBF1"},
			Status:   api.ClaimHypothesis,
			Hypotheses: []api.Hypothesis{{
				Rank: 1, RuleID: "K002-dependency-down", Claim: api.ClaimHypothesis, Title: "database failed first; api failed because of it",
				Confidence: api.ConfidenceDetail{Level: api.ConfidenceHigh, Factors: []string{
					"database failed first (03:04:05)", "api declares depends_on: [database]", "frontend depends on api, which failed after database, so it is a downstream symptom and not a second cause"}},
				Supporting: []api.Evidence{
					ev("01JEVENT00000000000000AAA1", "database", "database: FATAL:  database files are incompatible with server (initialization of the data directory failed because of an unexpected version)", 0),
					ev("01JEVENT00000000000000AAA2", "api", "api: connection refused to 127.0.0.1:15432", 22),
				},
			}},
			NextStep: api.NextStep{Description: "Check why database failed; the others are likely to recover once it does, so read its startup output before touching api or frontend.", Command: "kestrel timeline --service database"},
		}},
	}
}

func visible(s string) int { return utf8.RuneCountInString(ansiSeq.ReplaceAllString(s, "")) }

func TestDiagnosisFitsTheTerminal(t *testing.T) {
	for _, width := range []int{60, 80, 120} {
		var b bytes.Buffer
		Diagnosis(&b, longDiagnosis(), TextOptions{Style: NewStyle(false), Width: width})
		for _, line := range strings.Split(b.String(), "\n") {
			if visible(line) > width {
				t.Errorf("width %d: line is %d columns: %q", width, visible(line), line)
			}
		}
		out := b.String()
		// Shortening never costs the evidence: every evidence line keeps its event ID.
		for _, id := range []string{"evt …00AAA1", "evt …00AAA2"} {
			if !strings.Contains(out, id) {
				t.Errorf("width %d: lost %q:\n%s", width, id, out)
			}
		}
		// Long explanations wrap under a hanging indent instead of running off the edge.
		if !strings.Contains(out, "\n        ") {
			t.Errorf("width %d: the 'because' line should wrap with a hanging indent:\n%s", width, out)
		}
	}
}

// Without a known width (pipes, files, tests) nothing is shortened or wrapped.
func TestDiagnosisIsUnchangedWithoutAWidth(t *testing.T) {
	var b bytes.Buffer
	Diagnosis(&b, longDiagnosis(), TextOptions{Style: NewStyle(false)})
	out := b.String()
	for _, whole := range []string{
		"initialization of the data directory failed because of an unexpected version)",
		"high because: database failed first (03:04:05); api declares depends_on: [database]; frontend depends on api, which failed after database, so it is a downstream symptom and not a second cause",
		"    Check why database failed; the others are likely to recover once it does, so read its startup output before touching api or frontend.",
	} {
		if !strings.Contains(out, whole) {
			t.Errorf("missing the whole line %q in:\n%s", whole, out)
		}
	}
}

func TestTimelineRowsFitTheTerminal(t *testing.T) {
	rep := &api.TimelineReport{
		Session: api.SessionSummary{ID: "01JTESTSESSION", Mode: "watch", Status: "closed", StartedAt: t0, EndedAt: t0.Add(time.Minute), Services: []string{"api"}},
		Incidents: []api.Incident{{
			Number: 1, Start: t0, Services: []string{"api"}, CorrelationStrength: "strong", InitiatingKnown: true, InitiatingService: "api",
			Timeline: []api.TimelineEntry{
				{EventID: "01JEVENT00000000000000TL01", Time: t0, Count: 1, Service: "api", Type: api.EventProcessStarted, Severity: api.SeverityInfo, Message: "started (pid 1): " + strings.Repeat("arg ", 60), Role: api.RoleHealthy},
				{EventID: "01JEVENT00000000000000TL02", Time: t0, Until: t0.Add(5 * time.Second), Count: 9, Service: "api", Type: api.EventDependencyConnFailed, Severity: api.SeverityError, Message: strings.Repeat("dial tcp 127.0.0.1:5432: connect: connection refused ", 4), Role: api.RoleFirstAnomaly},
			},
		}},
	}
	for _, width := range []int{70, 100} {
		var b bytes.Buffer
		Timeline(&b, rep, TextOptions{Style: NewStyle(false), Width: width})
		for _, line := range strings.Split(b.String(), "\n") {
			if visible(line) > width {
				t.Errorf("width %d: %d columns: %q", width, visible(line), line)
			}
		}
		if !strings.Contains(b.String(), "×9") {
			t.Errorf("width %d: the repeat count must survive:\n%s", width, b.String())
		}
	}
	// A wide terminal gets more of the message than the 100-column cap of redirected output.
	var wide, plain bytes.Buffer
	Timeline(&wide, rep, TextOptions{Style: NewStyle(false), Width: 300})
	Timeline(&plain, rep, TextOptions{Style: NewStyle(false)})
	if !(len(wide.String()) > len(plain.String())) {
		t.Error("a 300-column terminal should show more than the 100-column cap")
	}
}

func TestVerificationWrapsReasonsAndNotes(t *testing.T) {
	rep := &api.VerificationReport{
		Verdict: api.VerdictPartiallyResolved, Claim: api.ClaimObserved, Window: "30s", Interval: "2s", StartedAt: t0, EndedAt: t0,
		Checks:  []api.CheckResult{},
		Reasons: []string{"failing: api (probe http://127.0.0.1:18080/health), worker (liveness process), and cache (tcp 127.0.0.1:6379)"},
		Notes:   []string{"an error repeating unchanged is recorded as one summary every 10s; this 5s window may not see it recur (the default 30s window does)"},
	}
	var b bytes.Buffer
	Verification(&b, rep, TextOptions{Style: NewStyle(true), Width: 60})
	for _, line := range strings.Split(b.String(), "\n") {
		if visible(line) > 60 {
			t.Errorf("%d columns: %q", visible(line), line)
		}
	}
	if !strings.Contains(b.String(), "\n    ") {
		t.Errorf("continuation lines should hang under the bullet:\n%s", b.String())
	}
	// No checks: no misleading window header.
	if strings.Contains(b.String(), "across a 30s window") || !strings.Contains(b.String(), "none could be chosen") {
		t.Errorf("empty checks section:\n%s", b.String())
	}
}
