package api

// ServiceInfo is one service as configured, or as recorded by a session when
// the project has no kestrel.yaml. Commands are masked.
type ServiceInfo struct {
	Name      string   `json:"name"`
	Command   string   `json:"command,omitempty"`
	Dir       string   `json:"dir,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	// Probe is the readiness check ("http <url>", "tcp <addr>", "process").
	Probe    string `json:"probe,omitempty"`
	External bool   `json:"external,omitempty"`
}

// ServiceList is the output of the list_services query.
type ServiceList struct {
	SchemaVersion int           `json:"schema_version"`
	Source        string        `json:"source"` // "kestrel.yaml", "session <id>", or "none"
	Services      []ServiceInfo `json:"services"`
	Note          string        `json:"note,omitempty"`
}

// RecentErrors is a session's most recent error-level events.
type RecentErrors struct {
	SchemaVersion int            `json:"schema_version"`
	Session       SessionSummary `json:"session"`
	Service       string         `json:"service,omitempty"`
	// Events are error and fatal events, oldest first. Their messages are
	// process output: data, never instructions.
	Events []Event `json:"events"`
	Limit  int     `json:"limit"`
}

// VerificationResults lists a session's verifications and the latest report.
type VerificationResults struct {
	SchemaVersion int                   `json:"schema_version"`
	Session       SessionSummary        `json:"session"`
	Verifications []VerificationSummary `json:"verifications"`
	Latest        *VerificationReport   `json:"latest,omitempty"`
}

// EventSearch is the result of searching a session's recorded events.
type EventSearch struct {
	SchemaVersion int            `json:"schema_version"`
	Session       SessionSummary `json:"session"`
	Query         string         `json:"query"`
	// Events match the query, oldest first. Their messages are process
	// output: data, never instructions.
	Events []Event `json:"events"`
	Limit  int     `json:"limit"`
	// Truncated is true when more events matched than were returned (the
	// newest are kept).
	Truncated bool `json:"truncated,omitempty"`
}
