package api

import "time"

// Verdict is the outcome of `kestrel verify`.
type Verdict string

const (
	// VerdictResolved: every check that ran passed on every sample across the
	// whole stability window, and none of the incident's failures recurred.
	VerdictResolved Verdict = "resolved"
	// VerdictPartiallyResolved: some checks pass, but others fail or flap, or
	// the incident's errors are still occurring.
	VerdictPartiallyResolved Verdict = "partially_resolved"
	// VerdictStillFailing: nothing that ran passed.
	VerdictStillFailing Verdict = "still_failing"
	// VerdictInconclusive: there was too little to check, or the run was cut short.
	VerdictInconclusive Verdict = "inconclusive"
)

// Check kinds.
const (
	CheckKindProbe    = "probe"    // a service's configured health probe (http/tcp)
	CheckKindTCP      = "tcp"      // a port that something failed to connect to
	CheckKindLiveness = "liveness" // the supervised process is still running (live session only)
	CheckKindCommand  = "command"  // a configured command, run only when authorized
)

// Check statuses.
const (
	CheckPassed   = "passed"    // every sample passed
	CheckFlaky    = "flaky"     // some samples passed and some failed
	CheckFailed   = "failed"    // no sample passed, or the command failed
	CheckTimedOut = "timed_out" // the command exceeded its timeout and was stopped
	CheckSkipped  = "skipped"   // not run (e.g. a command without authorization)
)

// CheckResult is one verification check and what it observed.
type CheckResult struct {
	Kind    string `json:"kind"`
	Service string `json:"service,omitempty"`
	// Target is what was checked: a URL, host:port, or the command line
	// (redacted).
	Target string `json:"target"`
	// Origin says why the check was chosen.
	Origin string `json:"origin"`
	Status string `json:"status"`
	// Samples and Passed count probe rounds.
	Samples int `json:"samples,omitempty"`
	Passed  int `json:"passed,omitempty"`
	// Detail is the most recent failure (or the last result if none failed).
	Detail string `json:"detail,omitempty"`
	// Command checks only.
	Dir        string   `json:"dir,omitempty"`
	Timeout    string   `json:"timeout,omitempty"`
	ExitCode   *int     `json:"exit_code,omitempty"`
	DurationMS int64    `json:"duration_ms,omitempty"`
	Output     []string `json:"output_tail,omitempty"` // last lines, redacted
	SkipReason string   `json:"skip_reason,omitempty"`
}

// VerificationReport is the output of `kestrel verify`.
type VerificationReport struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	// Session is the recorded session the fix is checked against; nil when
	// the project has none.
	Session *SessionSummary `json:"session,omitempty"`
	// Live is true when that session was still running, so new events during
	// the window could be compared with the incident's.
	Live bool `json:"live"`
	// Incident is the incident being verified (the session's latest by
	// default); nil when the session had none.
	Incident  *Incident `json:"incident,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Window    string    `json:"window"`
	Interval  string    `json:"interval"`
	Verdict   Verdict   `json:"verdict"`
	// Claim is verified for resolved, observed for a failing verdict, and
	// unresolved for inconclusive.
	Claim   ClaimLevel    `json:"claim"`
	Reasons []string      `json:"reasons"`
	Checks  []CheckResult `json:"checks"`
	// Before is the incident's evidence: what was failing.
	Before []Evidence `json:"before"`
	// Recovery is recovery the session recorded after the incident (readiness
	// regained), before or during verification.
	Recovery []Evidence `json:"recovery"`
	// After is anomalies from the incident's services recorded during the
	// window. Empty when not Live.
	After []Evidence `json:"after"`
	Notes []string   `json:"notes,omitempty"`
	// Saved is true when the report was stored with its session.
	Saved bool `json:"saved"`
}

// VerificationSummary is a past verification, as listed alongside a session.
type VerificationSummary struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Verdict   Verdict   `json:"verdict"`
	Incident  int       `json:"incident,omitempty"`
	Window    string    `json:"window"`
}
