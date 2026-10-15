//go:build !windows

package process

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// readPIDs reads PIDs a test script wrote, one per line.
func readPIDs(t *testing.T, path string, want int) []int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(path)
		var pids []int
		for _, f := range strings.Fields(string(b)) {
			if n, err := strconv.Atoi(f); err == nil {
				pids = append(pids, n)
			}
		}
		if len(pids) >= want {
			return pids
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d pids written", len(pids), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// running reports whether pid is a live process, not a zombie: an unreaped
// dead child still answers kill(pid, 0), so that alone proves nothing.
func running(pid int) bool {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	st := strings.TrimSpace(string(out))
	return err == nil && st != "" && !strings.HasPrefix(st, "Z")
}

func assertAllGone(t *testing.T, pids []int) {
	t.Helper()
	for _, pid := range pids {
		if Alive(pid) {
			t.Errorf("pid %d is still running (orphan)", pid)
		}
	}
}

func TestStopKillsWholeGroup(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pids")
	// The shell starts two background grandchildren and records all PIDs.
	script := `sleep 60 & echo $! >> ` + pidfile + `; sleep 60 & echo $! >> ` + pidfile + `; echo $$ >> ` + pidfile + `; wait`
	p, out, errR, err := Start(Spec{Shell: script})
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	defer errR.Close()
	pids := readPIDs(t, pidfile, 3)

	start := time.Now()
	p.Stop(2 * time.Second)
	if time.Since(start) > 2*time.Second {
		t.Errorf("SIGTERM should have been enough; took %v", time.Since(start))
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
	// Ignored signals are inherited, so the grandchild ignores SIGTERM too.
	script := `trap '' TERM; sleep 60 & echo $! >> ` + pidfile + `; echo $$ >> ` + pidfile + `; wait`
	p, out, errR, err := Start(Spec{Shell: script})
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
		t.Errorf("expected SIGKILL after the 300ms grace, took %v", elapsed)
	}
	assertAllGone(t, pids)
	if e := p.Exit(); e.Signal != "SIGKILL" {
		t.Errorf("exit = %+v, want SIGKILL", e)
	}
}

func TestExitClassification(t *testing.T) {
	tests := []struct {
		script string
		code   int
		signal string
	}{
		{"exit 0", 0, ""},
		{"exit 3", 3, ""},
		{"kill -SEGV $$", -1, "SIGSEGV"},
	}
	for _, tt := range tests {
		p, out, errR, err := Start(Spec{Shell: tt.script})
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, out)
		io.Copy(io.Discard, errR)
		e := p.Exit()
		if e.Code != tt.code || e.Signal != tt.signal || e.Requested {
			t.Errorf("%q: exit %+v", tt.script, e)
		}
		p.Stop(0)
	}
}

func TestOutputPipes(t *testing.T) {
	p, out, errR, err := Start(Spec{Argv: []string{"/bin/sh", "-c", "echo to-out; echo to-err >&2"}, Env: []string{"X=1"}})
	if err != nil {
		t.Fatal(err)
	}
	o, _ := io.ReadAll(out)
	e, _ := io.ReadAll(errR)
	if string(o) != "to-out\n" || string(e) != "to-err\n" {
		t.Fatalf("stdout %q stderr %q", o, e)
	}
	if !p.Exit().Success() {
		t.Fatal(p.Exit())
	}
}

// A grandchild that keeps the pipes open must not hide that the main process
// exited; Stop afterwards still cleans the grandchild up.
func TestLingeringGrandchild(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pids")
	p, out, errR, err := Start(Spec{Shell: `sleep 60 & echo $! >> ` + pidfile + `; exit 7`})
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	defer errR.Close()
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("main process exit not detected while a grandchild holds the pipes")
	}
	if p.Exit().Code != 7 {
		t.Fatalf("exit %+v", p.Exit())
	}
	pids := readPIDs(t, pidfile, 1)
	if !p.GroupAlive() {
		t.Fatal("grandchild should still be alive before Stop")
	}
	p.Stop(time.Second)
	assertAllGone(t, pids)
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
