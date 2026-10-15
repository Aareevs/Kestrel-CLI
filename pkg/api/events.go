package api

import "time"

// EventType classifies an event.
type EventType string

const (
	EventProcessStarted       EventType = "process_started"
	EventProcessExited        EventType = "process_exited"
	EventProcessCrashed       EventType = "process_crashed"
	EventLogLine              EventType = "log_line"
	EventStartupTimeout       EventType = "startup_timeout"
	EventProbeReady           EventType = "probe_ready" // first passing probe after start
	EventProbeFailed          EventType = "probe_failed"
	EventProbeRecovered       EventType = "probe_recovered"
	EventPortBindFailed       EventType = "port_bind_failed"
	EventDependencyConnFailed EventType = "dependency_conn_failed"
	EventRestart              EventType = "restart"
	EventTestFailed           EventType = "test_failed"
	EventCollectorOverload    EventType = "collector_overload"
	EventVerificationResult   EventType = "verification_result"
)

// Lifecycle reports whether t describes a process or probe state change.
// Lifecycle events are never dropped under backpressure.
func (t EventType) Lifecycle() bool {
	switch t {
	case EventLogLine, EventTestFailed, EventDependencyConnFailed, EventPortBindFailed:
		return false
	}
	return true
}

// Severity is an event's level.
type Severity string

const (
	SeverityDebug Severity = "debug"
	SeverityInfo  Severity = "info"
	SeverityWarn  Severity = "warn"
	SeverityError Severity = "error"
	SeverityFatal Severity = "fatal"
)

// Rank orders severities: debug=1 … fatal=5, unknown=0.
func (s Severity) Rank() int {
	switch s {
	case SeverityDebug:
		return 1
	case SeverityInfo:
		return 2
	case SeverityWarn:
		return 3
	case SeverityError:
		return 4
	case SeverityFatal:
		return 5
	}
	return 0
}

// EventSource is where an event was captured.
type EventSource string

const (
	SourceStdout     EventSource = "stdout"
	SourceStderr     EventSource = "stderr"
	SourceProbe      EventSource = "probe"
	SourceSupervisor EventSource = "supervisor"
	SourceKestrel    EventSource = "kestrel"
)

// Attrs holds typed optional event attributes.
type Attrs struct {
	Port       int    `json:"port,omitempty"`
	Target     string `json:"target,omitempty"` // host:port or service a connection was aimed at
	ExitCode   *int   `json:"exit_code,omitempty"`
	Signal     string `json:"signal,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	PatternID  string `json:"pattern_id,omitempty"`
	// Repeats is how many identical lines (by fingerprint) this event stands
	// for beyond the first, over DurationMS.
	Repeats int `json:"repeats,omitempty"`
	// Dropped counts lines discarded under backpressure (collector_overload).
	Dropped   int  `json:"dropped,omitempty"`
	Truncated bool `json:"truncated,omitempty"`
	// Requested is true when Kestrel itself asked the process to stop.
	Requested bool `json:"requested,omitempty"`
}

// Event is one observed fact. Seq is the authoritative order within a session.
type Event struct {
	ID          string      `json:"id"`
	SessionID   string      `json:"session_id"`
	Seq         uint64      `json:"seq"`
	Time        time.Time   `json:"time"`
	ServiceID   string      `json:"service"`
	PID         int         `json:"pid,omitempty"`
	Type        EventType   `json:"type"`
	Severity    Severity    `json:"severity"`
	Message     string      `json:"message"`
	Source      EventSource `json:"source"`
	Attrs       Attrs       `json:"attrs"`
	Fingerprint string      `json:"fingerprint,omitempty"`
}

// Liveness is whether a service's process is running.
type Liveness string

const (
	LivenessNotStarted Liveness = "not_started"
	LivenessRunning    Liveness = "running"
	LivenessExited     Liveness = "exited"
	LivenessCrashed    Liveness = "crashed"
	LivenessExternal   Liveness = "external" // not started by Kestrel; observed only
	LivenessUnknown    Liveness = "unknown"
)

// Readiness is whether a service's health probe passes.
type Readiness string

const (
	ReadinessUnknown  Readiness = "unknown"  // no probe, or no result yet
	ReadinessStarting Readiness = "starting" // within startup grace
	ReadinessReady    Readiness = "ready"
	ReadinessNotReady Readiness = "not_ready"
)

// ServiceStatus is one row of `kestrel status`.
type ServiceStatus struct {
	Name       string    `json:"name"`
	Liveness   Liveness  `json:"liveness"`
	Readiness  Readiness `json:"readiness"`
	PID        int       `json:"pid,omitempty"`
	Probe      string    `json:"probe,omitempty"` // e.g. "http http://localhost:8080/health"
	Detail     string    `json:"detail,omitempty"`
	StartedAt  time.Time `json:"started_at,omitzero"`
	ExitCode   *int      `json:"exit_code,omitempty"`
	LastChange time.Time `json:"last_change,omitzero"`
}

// StatusReport is the output of `kestrel status`.
type StatusReport struct {
	SchemaVersion int             `json:"schema_version"`
	SessionID     string          `json:"session_id,omitempty"`
	Active        bool            `json:"active"`
	Note          string          `json:"note,omitempty"`
	Services      []ServiceStatus `json:"services"`
}
