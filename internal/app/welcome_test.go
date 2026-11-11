package app

import (
	"strings"
	"testing"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func cmds(tips []api.WelcomeTip) string {
	var c []string
	for _, t := range tips {
		c = append(c, t.Command)
	}
	return strings.Join(c, " | ")
}

func TestWelcomeTipsFitTheProject(t *testing.T) {
	failed := &api.SessionSummary{Errors: 2, Status: api.SessionClosed}
	quiet := &api.SessionSummary{Status: api.SessionClosed}
	tests := []struct {
		name string
		w    api.Welcome
		want string
	}{
		{"empty directory", api.Welcome{}, "kestrel inspect | kestrel run -- <command> | kestrel --help"},
		{"services but no config", api.Welcome{Services: 2}, "kestrel init | kestrel watch | kestrel run -- <command> | kestrel --help"},
		{"configured, never run", api.Welcome{Services: 2, ConfigFile: "kestrel.yaml"}, "kestrel watch | kestrel run -- <command> | kestrel --help"},
		{"last run failed", api.Welcome{ConfigFile: "kestrel.yaml", LastSession: failed}, "kestrel why | kestrel diagnose -i | kestrel verify | kestrel --help"},
		{"last run interrupted", api.Welcome{ConfigFile: "kestrel.yaml", LastSession: &api.SessionSummary{Status: api.SessionInterrupted}}, "kestrel why | kestrel diagnose -i | kestrel verify | kestrel --help"},
		{"last run quiet", api.Welcome{ConfigFile: "kestrel.yaml", LastSession: quiet}, "kestrel watch | kestrel sessions | kestrel --help"},
		{"no config but sessions", api.Welcome{Services: 1, LastSession: failed}, "kestrel init | kestrel why | kestrel diagnose -i | kestrel verify"},
	}
	for _, tt := range tests {
		if got := cmds(welcomeTips(&tt.w)); got != tt.want {
			t.Errorf("%s:\n got  %s\n want %s", tt.name, got, tt.want)
		}
	}
}
