package api

import "time"

// Welcome is what the welcome screen shows: a live summary of the project and
// a few next steps that fit it. It is rendered for people at a terminal and is
// not part of the stable JSON contract.
type Welcome struct {
	Version      string          `json:"version"`
	Root         string          `json:"root"`
	Technologies []string        `json:"technologies,omitempty"`
	Services     int             `json:"services"`
	ConfigFile   string          `json:"config_file,omitempty"`
	LastSession  *SessionSummary `json:"last_session,omitempty"`
	Tips         []WelcomeTip    `json:"tips"`
	GeneratedAt  time.Time       `json:"generated_at"`
}

// WelcomeTip is one suggested command and why.
type WelcomeTip struct {
	Command string `json:"command"`
	Why     string `json:"why"`
}
