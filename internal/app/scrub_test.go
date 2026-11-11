package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func TestScrubbedKeepsRedactingWhenTheConfigIsUnreadable(t *testing.T) {
	a := mcpApp(t)
	os.WriteFile(filepath.Join(a.ProjectRoot, "kestrel.yaml"), []byte("version: 1\nservices: [this is: not valid\n"), 0o644)
	if _, err := a.redactor(); err == nil {
		t.Fatal("premise: the redactor should fail on this config")
	}
	rep := &api.RecentErrors{Events: []api.Event{{Message: "connecting redis://:myredispass@cache:6379/0 with password=hunter2"}}}
	out, err := scrubbed(a, rep, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m := out.Events[0].Message; strings.Contains(m, "myredispass") || strings.Contains(m, "hunter2") {
		t.Fatalf("built-in redaction must apply even when kestrel.yaml can't be read: %q", m)
	}
	// An error or a nil report passes through untouched.
	if r, err := scrubbed[api.RecentErrors](a, nil, os.ErrNotExist); r != nil || err != os.ErrNotExist {
		t.Errorf("error pass-through: %v %v", r, err)
	}
}
