//go:build windows

package process

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// running on Windows reports whether pid is still alive. Unlike Unix,
// Windows has no zombie state where dead children answer kill(pid, 0)
// until reaped by wait().
func running(pid int) bool {
	return Alive(pid)
}

func TestStopKillsWholeJob(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pids")
	// Launch fakesvc with children to verify Job Object containment
	p, out, errR, err := Start(Spec{Argv: []string{fakesvc, "--children", "2", "--pidfile", pidfile}})
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	defer errR.Close()
	pids := readPIDs(t, pidfile, 3)

	start := time.Now()
	p.Stop(2 * time.Second)
	if time.Since(start) > 2*time.Second {
		t.Errorf("CTRL_BREAK should have been enough; took %v", time.Since(start))
	}
	if p.GroupAlive() {
		t.Fatal("group still alive after Stop")
	}
	assertAllGone(t, pids)
	if e := p.Exit(); !e.Requested {
		t.Errorf("exit should be marked requested: %+v", e)
	}
}

func TestStopEscalatesToKill(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pids")
	p, out, errR, err := Start(Spec{Argv: []string{fakesvc, "--ignore-term", "--children", "1", "--pidfile", pidfile}})
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	defer errR.Close()
	pids := readPIDs(t, pidfile, 2)

	start := time.Now()
	p.Stop(300 * time.Millisecond)
	elapsed := time.Since(start)
	if elapsed < 300*time.Millisecond || elapsed > 3*time.Second {
		t.Errorf("expected terminate after the 300ms grace, took %v", elapsed)
	}
	assertAllGone(t, pids)
}

func TestExitClassification(t *testing.T) {
	tests := []struct {
		script string
		code   int
	}{
		{"exit 0", 0},
		{"exit 3", 3},
	}
	for _, tt := range tests {
		p, out, errR, err := Start(Spec{Shell: tt.script})
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, out)
		io.Copy(io.Discard, errR)
		e := p.Exit()
		if e.Code != tt.code || e.Requested {
			t.Errorf("%q: exit %+v", tt.script, e)
		}
		p.Stop(0)
	}
}

func TestOutputPipes(t *testing.T) {
	p, out, errR, err := Start(Spec{Shell: "echo to-out& echo to-err 1>&2", Env: []string{"X=1"}})
	if err != nil {
		t.Fatal(err)
	}
	o, _ := io.ReadAll(out)
	e, _ := io.ReadAll(errR)
	if string(o) != "to-out\r\n" && string(o) != "to-out\n" {
		t.Fatalf("stdout %q", o)
	}
	if string(e) != "to-err\r\n" && string(e) != "to-err\n" {
		t.Fatalf("stderr %q", e)
	}
	if !p.Exit().Success() {
		t.Fatal(p.Exit())
	}
}

func TestStartErrors(t *testing.T) {
	if _, _, _, err := Start(Spec{}); err == nil {
		t.Error("empty spec should fail")
	}
	if _, _, _, err := Start(Spec{Argv: []string{"x"}, Shell: "y"}); err == nil {
		t.Error("both Argv and Shell should fail")
	}
	if _, _, _, err := Start(Spec{Argv: []string{"/definitely/not/a/program"}}); err == nil {
		t.Error("missing program should fail")
	}
}

func TestAlive(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Error("own pid should be alive")
	}
	if Alive(0) || Alive(-1) {
		t.Error("non-positive pids are never alive")
	}
}
