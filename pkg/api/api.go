// Package api defines the stable JSON types emitted by Kestrel's read commands
// (`--format json`) and, later, by its MCP server. Fields are only ever added;
// renaming or removing a field requires bumping SchemaVersion.
package api

// SchemaVersion is the version of the JSON output schema.
const SchemaVersion = 1

// Confidence is a coarse, explainable confidence level. Kestrel never reports
// fake percentages.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// Rank orders confidences: high=3, medium=2, low=1, unknown=0.
func (c Confidence) Rank() int {
	switch c {
	case ConfidenceHigh:
		return 3
	case ConfidenceMedium:
		return 2
	case ConfidenceLow:
		return 1
	}
	return 0
}

// Source points at the file (and line, when known) that a finding came from.
// File is relative to the project root, slash-separated.
type Source struct {
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
	Note string `json:"note,omitempty"`
}

// CommandGuess is a probable start command. It is a suggestion only; discovery
// never executes anything.
type CommandGuess struct {
	Value      string     `json:"value"`
	Confidence Confidence `json:"confidence"`
	Source     Source     `json:"source"`
}

// PortGuess is a probable listening port on localhost.
type PortGuess struct {
	Value      int        `json:"value"`
	Confidence Confidence `json:"confidence"`
	Source     Source     `json:"source"`
}

// HealthGuess is a suggested health probe.
type HealthGuess struct {
	Type       string     `json:"type"` // http | tcp
	Target     string     `json:"target"`
	Confidence Confidence `json:"confidence"`
	Reason     string     `json:"reason"`
}

// DependencyKind distinguishes dependencies a file declares from ones Kestrel inferred.
type DependencyKind string

const (
	DependencyDeclared DependencyKind = "declared"
	DependencyInferred DependencyKind = "inferred"
)

// Dependency is a service this candidate probably needs.
type Dependency struct {
	Name string         `json:"name"`
	Kind DependencyKind `json:"kind"`
	// Resolved is true when Name refers to another detected candidate.
	Resolved bool   `json:"resolved"`
	Source   Source `json:"source"`
}

// ServiceCandidate is one runnable thing discovery believes exists in the project.
type ServiceCandidate struct {
	Name           string         `json:"name"`
	Dir            string         `json:"dir"`
	Technology     string         `json:"technology"`
	Framework      string         `json:"framework,omitempty"`
	PackageManager string         `json:"package_manager,omitempty"`
	Image          string         `json:"image,omitempty"`
	Command        *CommandGuess  `json:"command,omitempty"`
	Alternatives   []CommandGuess `json:"alternative_commands,omitempty"`
	Port           *PortGuess     `json:"port,omitempty"`
	Health         *HealthGuess   `json:"health,omitempty"`
	DependsOn      []Dependency   `json:"depends_on,omitempty"`
	EnvKeys        []string       `json:"env_keys,omitempty"`
	Sources        []Source       `json:"sources"`
	Confidence     Confidence     `json:"confidence"`
}

// ScanStats describes how much of the project discovery looked at and whether
// it stopped early.
type ScanStats struct {
	FilesSeen       int    `json:"files_seen"`
	DirsSeen        int    `json:"dirs_seen"`
	MaxDepth        int    `json:"max_depth"`
	MaxFiles        int    `json:"max_files"`
	Truncated       bool   `json:"truncated"`
	TruncatedReason string `json:"truncated_reason,omitempty"`
}

// InspectResult is the output of `kestrel inspect`.
type InspectResult struct {
	SchemaVersion int                `json:"schema_version"`
	Root          string             `json:"root"`
	Technologies  []string           `json:"technologies"`
	Candidates    []ServiceCandidate `json:"candidates"`
	ConfigFile    string             `json:"config_file,omitempty"`
	Scan          ScanStats          `json:"scan"`
	Warnings      []string           `json:"warnings,omitempty"`
}

// ConfigIssue is one validation problem in a config file.
type ConfigIssue struct {
	Line    int    `json:"line,omitempty"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// ConfigValidation is the output of `kestrel config validate`.
type ConfigValidation struct {
	SchemaVersion int           `json:"schema_version"`
	Path          string        `json:"path"`
	Valid         bool          `json:"valid"`
	Issues        []ConfigIssue `json:"issues,omitempty"`
}

// ConfigPath is the output of `kestrel config path`.
type ConfigPath struct {
	SchemaVersion int    `json:"schema_version"`
	ProjectRoot   string `json:"project_root"`
	Path          string `json:"path,omitempty"`
	Exists        bool   `json:"exists"`
}

// CheckStatus is the outcome of one doctor check.
type CheckStatus string

const (
	CheckOK   CheckStatus = "ok"
	CheckWarn CheckStatus = "warn"
	CheckFail CheckStatus = "fail"
	CheckSkip CheckStatus = "skip"
)

// DoctorCheck is one self-check performed by `kestrel doctor`.
type DoctorCheck struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Message string      `json:"message"`
	Hint    string      `json:"hint,omitempty"`
}

// DoctorReport is the output of `kestrel doctor`.
type DoctorReport struct {
	SchemaVersion int           `json:"schema_version"`
	OK            bool          `json:"ok"`
	Checks        []DoctorCheck `json:"checks"`
}

// VersionInfo is the output of `kestrel version`.
type VersionInfo struct {
	SchemaVersion int    `json:"schema_version"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	Date          string `json:"date"`
	GoVersion     string `json:"go_version"`
	Platform      string `json:"platform"`
}
