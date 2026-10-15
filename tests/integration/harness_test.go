package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

var bins struct {
	once    sync.Once
	dir     string
	kestrel string
	fakesvc string
	err     error
}

func buildBins(t *testing.T) (kestrel, fakesvc string) {
	t.Helper()
	bins.once.Do(func() {
		bins.dir, bins.err = os.MkdirTemp("", "kestrel-it")
		if bins.err != nil {
			return
		}
		exe := ""
		if runtime.GOOS == "windows" {
			exe = ".exe"
		}
		bins.kestrel = filepath.Join(bins.dir, "kestrel"+exe)
		bins.fakesvc = filepath.Join(bins.dir, "fakesvc"+exe)
		for out, pkg := range map[string]string{bins.kestrel: "../../cmd/kestrel", bins.fakesvc: "../fakesvc"} {
			if b, err := exec.Command("go", "build", "-o", out, pkg).CombinedOutput(); err != nil {
				bins.err = fmt.Errorf("go build %s: %v\n%s", pkg, err, b)
				return
			}
		}
	})
	if bins.err != nil {
		t.Fatal(bins.err)
	}
	return bins.kestrel, bins.fakesvc
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// kestrelProc is a kestrel subprocess in its own process group, so a
// terminal-style Ctrl-C can be sent to the group without touching the test.
type kestrelProc struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stderr *bytes.Buffer
}

func startKestrel(t *testing.T, home string, args ...string) *kestrelProc {
	t.Helper()
	k, _ := buildBins(t)
	cmd := exec.Command(k, args...)
	cmd.Env = append(os.Environ(), "KESTREL_HOME="+home, "NO_COLOR=1")
	setProcGroup(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		killProcGroup(cmd)
	})
	return &kestrelProc{cmd: cmd, stdout: out, stderr: &errb}
}

func (k *kestrelProc) ctrlC() {
	sendCtrlC(k.cmd)
}

func (k *kestrelProc) wait(t *testing.T, timeout time.Duration) int {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- k.cmd.Wait() }()
	select {
	case err := <-done:
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return 0
	case <-time.After(timeout):
		t.Fatalf("kestrel did not exit within %v; stderr:\n%s", timeout, k.stderr)
		return -1
	}
}

// eventStream reads NDJSON events and lets tests wait for conditions.
type eventStream struct {
	mu   sync.Mutex
	evs  []api.Event
	done chan struct{}
}

func readEvents(r io.Reader) *eventStream {
	s := &eventStream{done: make(chan struct{})}
	go func() {
		defer close(s.done)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var ev api.Event
			if json.Unmarshal(sc.Bytes(), &ev) == nil {
				s.mu.Lock()
				s.evs = append(s.evs, ev)
				s.mu.Unlock()
			}
		}
	}()
	return s
}

func (s *eventStream) count(f func(api.Event) bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.evs {
		if f(e) {
			n++
		}
	}
	return n
}

func (s *eventStream) waitFor(t *testing.T, what string, timeout time.Duration, f func(api.Event) bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for s.count(f) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; events:\n%s", what, s.dump())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *eventStream) dump() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	for _, e := range s.evs {
		fmt.Fprintf(&b, "%d %s %s %s\n", e.Seq, e.ServiceID, e.Type, e.Message)
	}
	return b.String()
}

func pidsFrom(t *testing.T, path string, want int) []int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, _ := os.ReadFile(path)
		var pids []int
		for _, f := range strings.Fields(string(b)) {
			if n, err := strconv.Atoi(f); err == nil {
				pids = append(pids, n)
			}
		}
		if len(pids) >= want || time.Now().After(deadline) {
			if len(pids) < want {
				t.Fatalf("only %d of %d PIDs recorded", len(pids), want)
			}
			return pids
		}
		time.Sleep(20 * time.Millisecond)
	}
}
