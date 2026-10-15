package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/privacy"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func TestRedactValue(t *testing.T) {
	in := &api.RecentErrors{
		Session: api.SessionSummary{ID: "01M4ZN0HSHDN9RQ18QA2QMV1DJ", Services: []string{"api"}},
		Events: []api.Event{{ID: "01M4ZN0PP8D5ZABFAQZXGG1P5N", Time: time.Unix(0, 0).UTC(), Seq: 7,
			Message: `login failed: password=hunter22 "quoted" \ back`, Attrs: api.Attrs{Target: "postgres://app:s3cret@db:5432/x"}}},
		Limit: 5,
	}
	out, err := redactValue(privacy.Default, in)
	if err != nil {
		t.Fatal(err)
	}
	e := out.Events[0]
	if strings.Contains(e.Message, "hunter22") || strings.Contains(e.Attrs.Target, "s3cret") {
		t.Fatalf("not redacted: %q %q", e.Message, e.Attrs.Target)
	}
	if !strings.Contains(e.Message, `"quoted" \ back`) {
		t.Fatalf("quotes and backslashes must survive intact: %q", e.Message)
	}
	// Identifiers and non-string fields pass through unchanged.
	if out.Session.ID != in.Session.ID || e.ID != in.Events[0].ID || e.Seq != 7 || out.Limit != 5 || !e.Time.Equal(in.Events[0].Time) {
		t.Fatalf("changed: %+v", out)
	}
	if in.Events[0].Message == e.Message {
		t.Fatal("input must not be modified in place")
	}
}
