// Package process starts and stops the processes Kestrel supervises.
//
// Every process Kestrel starts gets its own process group (Unix) or Job
// Object (Windows), so stopping a service stops everything it spawned. Kestrel
// only ever signals groups it created.
package process

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// Spec describes how to start one process.
type Spec struct {
	// Argv runs a program directly (kestrel run -- cmd args...).
	Argv []string
	// Shell runs a command line through the platform shell (config commands).
	// Exactly one of Argv and Shell must be set.
	Shell string
	Dir   string
	Env   []string // full environment; nil inherits Kestrel's
}

// Exit describes how a process ended.
type Exit struct {
	Code      int    // -1 if killed by a signal
	Signal    string // e.g. "SIGKILL"
	Requested bool   // Kestrel asked it to stop
	Err       error  // wait error other than a non-zero exit
}

// Success reports a clean, zero exit.
func (e Exit) Success() bool { return e.Code == 0 && e.Signal == "" && e.Err == nil }

func (e Exit) String() string {
	switch {
	case e.Err != nil:
		return e.Err.Error()
	case e.Signal != "":
		return "killed by " + e.Signal
	default:
		return fmt.Sprintf("exit code %d", e.Code)
	}
}

// Proc is a running process and its group.
type Proc struct {
	PID     int
	Started time.Time

	cmd       *exec.Cmd
	group     groupHandle
	done      chan struct{}
	exit      Exit
	requested atomic.Bool
	stopOnce  sync.Once
}

// Start launches spec. The returned readers carry stdout and stderr; they
// reach EOF when every process holding the pipes (including grandchildren)
// has exited. Process exit is tracked independently, so a lingering
// grandchild cannot hide that the main process died.
func Start(spec Spec) (*Proc, io.ReadCloser, io.ReadCloser, error) {
	cmd, err := command(spec)
	if err != nil {
		return nil, nil, nil, err
	}
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return nil, nil, nil, err
	}
	cmd.Stdin = nil // /dev/null
	cmd.Stdout = outW
	cmd.Stderr = errW
	prepare(cmd)

	if err := cmd.Start(); err != nil {
		for _, f := range []*os.File{outR, outW, errR, errW} {
			f.Close()
		}
		return nil, nil, nil, err
	}
	// The child has its own copies; close ours so readers see EOF.
	outW.Close()
	errW.Close()

	p := &Proc{PID: cmd.Process.Pid, Started: time.Now(), cmd: cmd, done: make(chan struct{})}
	g, gerr := attach(cmd)
	p.group = g
	go p.wait()
	if gerr != nil {
		// Without a group we cannot guarantee cleanup; refuse to continue.
		p.Stop(0)
		outR.Close()
		errR.Close()
		return nil, nil, nil, fmt.Errorf("could not isolate process group: %w", gerr)
	}
	return p, outR, errR, nil
}

func command(spec Spec) (*exec.Cmd, error) {
	switch {
	case len(spec.Argv) > 0 && spec.Shell != "":
		return nil, errors.New("process: set Argv or Shell, not both")
	case len(spec.Argv) > 0:
		return exec.Command(spec.Argv[0], spec.Argv[1:]...), nil
	case spec.Shell != "":
		return shellCommand(spec.Shell), nil
	}
	return nil, errors.New("process: nothing to run")
}

func (p *Proc) wait() {
	err := p.cmd.Wait()
	e := Exit{Code: 0}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		e.Code, e.Signal = exitStatus(ee)
	default:
		e.Code, e.Err = -1, err
	}
	e.Requested = p.requested.Load()
	p.exit = e
	close(p.done)
}

// Done is closed when the main process has exited.
func (p *Proc) Done() <-chan struct{} { return p.done }

// Exit is valid after Done is closed.
func (p *Proc) Exit() Exit {
	<-p.done
	return p.exit
}

// Stop ends the whole group: a polite signal first (SIGTERM / CTRL_BREAK),
// then, if anything in the group is still alive after grace, a forced kill.
// It returns once the main process has exited and the group is empty or has
// been force-killed. Safe to call more than once and after a natural exit
// (it still cleans up any grandchildren left in the group).
func (p *Proc) Stop(grace time.Duration) {
	p.stopOnce.Do(func() {
		p.requested.Store(true)
		if p.group.alive() {
			p.group.terminate()
			deadline := time.Now().Add(grace)
			for time.Now().Before(deadline) && p.group.alive() {
				time.Sleep(20 * time.Millisecond)
			}
			if p.group.alive() {
				p.group.kill()
			}
		}
		<-p.done
		// Members can outlive the leader by a moment after SIGKILL.
		for i := 0; i < 50 && p.group.alive(); i++ {
			time.Sleep(10 * time.Millisecond)
		}
		p.group.release()
	})
}

// GroupAlive reports whether any process in the group is still running.
func (p *Proc) GroupAlive() bool { return p.group.alive() }
