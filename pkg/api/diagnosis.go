package api

import "time"

// ClaimLevel says how strongly something is known. Diagnosis output never
// blends levels: every statement carries one.
type ClaimLevel string

const (
	ClaimObserved   ClaimLevel = "observed"   // an event that happened
	ClaimCorrelated ClaimLevel = "correlated" // linked to other events by graph, port, or time
	ClaimHypothesis ClaimLevel = "hypothesis" // a proposed cause, with evidence
	ClaimVerified   ClaimLevel = "verified"   // confirmed by a verification run (Phase 5)
	ClaimUnresolved ClaimLevel = "unresolved" // evidence is insufficient to pick a cause
)

// ServiceTopology is what a session knew about one service when it started.
type ServiceTopology struct {
	Name      string   `json:"name"`
	DependsOn []string `json:"depends_on,omitempty"`
	Ports     []int    `json:"ports,omitempty"` // from health probes
	External  bool     `json:"external,omitempty"`
}

// Topology is the service layout a session ran with.
type Topology struct {
	Services []ServiceTopology `json:"services"`
}

// Edge kinds in the dependency graph.
const (
	EdgeDeclared = "declared" // depends_on in kestrel.yaml
	EdgeInferred = "inferred" // a connection error named a port another service owns
)

// GraphEdge says From depends on To.
type GraphEdge struct {
	From     string   `json:"from"`
	To       string   `json:"to"`
	Kind     string   `json:"kind"`
	Port     int      `json:"port,omitempty"`
	Evidence []string `json:"evidence,omitempty"` // event IDs, for inferred edges
}

// Correlation strengths.
const (
	CorrelationStrong = "strong" // linked by a dependency edge, a port, or the same service
	CorrelationWeak   = "weak"   // linked only by happening close together in time
)

// Timeline entry roles.
const (
	RoleHealthy      = "last_healthy"
	RoleFirstAnomaly = "first_anomaly"
	RoleKey          = "key"
	RoleSymptom      = "downstream_symptom"
	RoleRecovery     = "recovery"
)

// TimelineEntry is one line of an incident timeline. Repeated events with the
// same fingerprint are collapsed: Count and Until describe the run.
type TimelineEntry struct {
	EventID  string     `json:"event_id"`
	Seq      uint64     `json:"seq"`
	Time     time.Time  `json:"time"`
	Until    time.Time  `json:"until,omitzero"`
	Count    int        `json:"count"`
	Service  string     `json:"service"`
	Type     EventType  `json:"type"`
	Severity Severity   `json:"severity"`
	Message  string     `json:"message"`
	Role     string     `json:"role"`
	Claim    ClaimLevel `json:"claim"`
}

// Incident is a group of related anomalies.
type Incident struct {
	Number   int       `json:"number"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Services []string  `json:"services"`
	EventIDs []string  `json:"event_ids"`
	// CorrelationStrength is how the anomalies are linked to each other. It is
	// separate from any causal confidence, which belongs to hypotheses.
	CorrelationStrength string `json:"correlation_strength"`
	// Initiating is the earliest anomaly of the upstream-most failing service,
	// when the evidence singles one out. InitiatingKnown is false otherwise.
	InitiatingKnown   bool            `json:"initiating_known"`
	InitiatingService string          `json:"initiating_service,omitempty"`
	InitiatingEventID string          `json:"initiating_event_id,omitempty"`
	InitiatingNote    string          `json:"initiating_note,omitempty"`
	Timeline          []TimelineEntry `json:"timeline"`
}

// Evidence is one event cited by a hypothesis.
type Evidence struct {
	EventID string    `json:"event_id"`
	Time    time.Time `json:"time"`
	Service string    `json:"service"`
	Summary string    `json:"summary"`
	Count   int       `json:"count,omitempty"` // >1 when it stands for repeats
}

// ConfidenceDetail is a coarse level plus the factors that produced it.
type ConfidenceDetail struct {
	Level   Confidence `json:"level"`
	Factors []string   `json:"factors"`
}

// NextStep is a suggestion for the user to run. Kestrel never runs it.
type NextStep struct {
	Description string `json:"description"`
	Command     string `json:"command,omitempty"`
}

// Hypothesis is a proposed cause with the evidence for and against it.
type Hypothesis struct {
	Rank          int              `json:"rank"`
	RuleID        string           `json:"rule_id"`
	Title         string           `json:"title"`
	Explanation   string           `json:"explanation"`
	Claim         ClaimLevel       `json:"claim"`
	Confidence    ConfidenceDetail `json:"confidence"`
	Services      []string         `json:"services"`
	Supporting    []Evidence       `json:"supporting"`
	Contradicting []Evidence       `json:"contradicting"`
	NextStep      NextStep         `json:"next_step"`
}

// Symptom is a downstream effect attributed to an upstream failure.
type Symptom struct {
	Service  string     `json:"service"`
	Summary  string     `json:"summary"`
	EventIDs []string   `json:"event_ids"`
	Claim    ClaimLevel `json:"claim"`
}

// IncidentDiagnosis is the diagnosis of one incident.
type IncidentDiagnosis struct {
	Incident   Incident     `json:"incident"`
	Status     ClaimLevel   `json:"status"` // hypothesis or unresolved
	Hypotheses []Hypothesis `json:"hypotheses"`
	Symptoms   []Symptom    `json:"symptoms"`
	// NextStep is the single most informative check for this incident.
	NextStep NextStep `json:"next_step"`
}

// DiagnosisReport is the output of `kestrel diagnose`.
type DiagnosisReport struct {
	SchemaVersion int                 `json:"schema_version"`
	Session       SessionSummary      `json:"session"`
	Graph         []GraphEdge         `json:"graph"`
	Incidents     []IncidentDiagnosis `json:"incidents"`
	Note          string              `json:"note,omitempty"`
}

// TimelineReport is the output of `kestrel timeline`.
type TimelineReport struct {
	SchemaVersion int             `json:"schema_version"`
	Session       SessionSummary  `json:"session"`
	Graph         []GraphEdge     `json:"graph"`
	Incidents     []Incident      `json:"incidents"`
	Service       string          `json:"service,omitempty"`
	Since         time.Time       `json:"since,omitzero"`
	Events        []TimelineEntry `json:"events,omitempty"` // with --service: that service's key events
}
