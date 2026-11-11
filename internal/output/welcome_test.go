package output

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func sampleWelcome() *api.Welcome {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	return &api.Welcome{
		Version: "v1.1.0", Root: "/home/dev/some/quite/long/project/path/that/keeps/going/and/going/api-gateway",
		Technologies: []string{"Go", "Docker"}, Services: 3, ConfigFile: "kestrel.yaml", GeneratedAt: now,
		LastSession: &api.SessionSummary{StartedAt: now.Add(-3 * time.Hour), Services: []string{"api", "db"}, Errors: 4, Status: api.SessionClosed},
		Tips: []api.WelcomeTip{
			{Command: "kestrel why", Why: "explain the failure in the last session, with a rather long reason that has to be cut"},
			{Command: "kestrel --help", Why: "every command"},
		},
	}
}

func TestLogoPlainHasNoEscapesAndColorFadesLeftToRight(t *testing.T) {
	plain := Logo(NewStyle(false), 0)
	if len(plain) != 5 {
		t.Fatalf("logo has %d rows", len(plain))
	}
	for _, l := range plain {
		if strings.Contains(l, "\x1b") || utf8.RuneCountInString(l) != utf8.RuneCountInString(plain[0]) {
			t.Fatalf("plain logo row %q", l)
		}
	}
	colored := Logo(NewStyle(true), 0)
	first, last := colored[0], colored[4]
	if !strings.Contains(first, "38;5;51m") || !strings.Contains(last, "38;5;171m") {
		t.Fatalf("gradient should start cyan (51) and end violet (171):\n%q\n%q", first, last)
	}
	if strings.Index(first, "38;5;51m") > strings.Index(first, "38;5;171m") {
		t.Error("gradient runs the wrong way")
	}
	// A terminal too narrow for the block logo gets the one-line wordmark.
	if n := Logo(NewStyle(false), 20); len(n) != 1 || n[0] != "KESTREL" {
		t.Errorf("narrow logo = %q", n)
	}
}

func TestWelcomeContentAndFit(t *testing.T) {
	for _, width := range []int{0, 80, 40, 30} {
		var b bytes.Buffer
		Welcome(&b, sampleWelcome(), TextOptions{Style: NewStyle(false), Width: width})
		out := b.String()
		for _, want := range []string{"Every failure has a trail.", "v1.1.0", "Go, Docker · 3 services found", "✓ kestrel.yaml", "3h ago · api, db · 4 errors", "› kestrel why"} {
			if !strings.Contains(out, want) && width == 0 {
				t.Errorf("width %d: missing %q\n%s", width, want, out)
			}
		}
		if strings.Contains(out, "\x1b") {
			t.Errorf("width %d: escapes without color", width)
		}
		if width > 0 {
			for _, l := range strings.Split(out, "\n") {
				if n := utf8.RuneCountInString(l); n > width {
					t.Errorf("width %d: line is %d columns: %q", width, n, l)
				}
			}
		}
	}
}

func TestWelcomeWithNothingRecorded(t *testing.T) {
	var b bytes.Buffer
	Welcome(&b, &api.Welcome{Version: "dev", Root: "/p", Tips: []api.WelcomeTip{{Command: "kestrel inspect", Why: "x"}}}, TextOptions{Style: NewStyle(false)})
	out := b.String()
	for _, want := range []string{"nothing recognized yet", "no kestrel.yaml", "nothing recorded for this project yet"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n%s", want, out)
		}
	}
}
