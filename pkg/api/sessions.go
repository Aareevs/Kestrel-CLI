package api

import "time"

// Session statuses.
const (
	SessionActive      = "active"
	SessionClosed      = "closed"
	SessionInterrupted = "interrupted" // Kestrel died without closing it
)

// SessionSummary is one row of `kestrel sessions list`.
type SessionSummary struct {
	ID          string    `json:"id"`
	Mode        string    `json:"mode"` // run | watch
	ProjectRoot string    `json:"project_root"`
	Status      string    `json:"status"`
	StartedAt   time.Time `json:"started_at"`
	EndedAt     time.Time `json:"ended_at,omitzero"`
	Services    []string  `json:"services"`
	Events      int       `json:"events"`
	Errors      int       `json:"errors"` // error + fatal events
	Dropped     int       `json:"dropped"`
	SizeBytes   int64     `json:"size_bytes"`
	// WriteErrors counts events that could not be fully persisted. Non-zero
	// means the stored record is incomplete.
	WriteErrors int `json:"write_errors,omitempty"`
}

// SessionList is the output of `kestrel sessions list`.
type SessionList struct {
	SchemaVersion int              `json:"schema_version"`
	Sessions      []SessionSummary `json:"sessions"`
	AllProjects   bool             `json:"all_projects"`
}

// SessionDetail is the output of `kestrel sessions show`.
type SessionDetail struct {
	SchemaVersion int             `json:"schema_version"`
	Session       SessionSummary  `json:"session"`
	Services      []ServiceStatus `json:"services"`
	CountsByType  map[string]int  `json:"counts_by_type"`
	Notable       []Event         `json:"notable_events"` // warn+, lifecycle, probe changes
	NotableCap    int             `json:"notable_cap"`
}

// ExportBundle is a redacted, self-contained copy of a session.
type ExportBundle struct {
	SchemaVersion int             `json:"schema_version"`
	ExportedAt    time.Time       `json:"exported_at"`
	Redaction     string          `json:"redaction"`
	Session       SessionSummary  `json:"session"`
	Services      []ServiceStatus `json:"services"`
	Events        []Event         `json:"events"`
	// Warnings lists problems reading stored data (e.g. a missing segment).
	Warnings []string `json:"warnings,omitempty"`
}

// RemovedSession describes a session removed by retention or delete.
type RemovedSession struct {
	ID        string `json:"id"`
	Reason    string `json:"reason"` // age | size | requested
	SizeBytes int64  `json:"size_bytes"`
}

// RetentionResult is the output of `kestrel cleanup`.
type RetentionResult struct {
	SchemaVersion int              `json:"schema_version"`
	DryRun        bool             `json:"dry_run"`
	RetentionDays int              `json:"retention_days"`
	MaxTotalMB    int              `json:"max_total_mb"`
	Removed       []RemovedSession `json:"removed"`
	Kept          int              `json:"kept"`
	TotalBefore   int64            `json:"total_bytes_before"`
	TotalAfter    int64            `json:"total_bytes_after"`
	// Skipped lists sessions retention would have removed but could not
	// (e.g. still active).
	Skipped []string `json:"skipped,omitempty"`
}
