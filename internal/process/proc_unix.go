//go:build !windows

package process

import (
	"errors"
	"os/exec"
	"syscall"
)

func shellCommand(line string) *exec.Cmd { return exec.Command("/bin/sh", "-c", line) }

// prepare puts the child in a new process group whose ID is its PID.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	setPdeathsig(cmd.SysProcAttr)
}

// groupHandle is the process group ID, which equals the leader's PID. POSIX
// does not reuse a PID while a process group with that ID exists, so
// signalling -pgid only ever reaches processes Kestrel started (or their
// descendants that stayed in the group).
type groupHandle struct{ pgid int }

func attach(cmd *exec.Cmd) (groupHandle, error) {
	return groupHandle{pgid: cmd.Process.Pid}, nil
}

func (g groupHandle) alive() bool {
	err := syscall.Kill(-g.pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (g groupHandle) terminate() { _ = syscall.Kill(-g.pgid, syscall.SIGTERM) }
func (g groupHandle) kill()      { _ = syscall.Kill(-g.pgid, syscall.SIGKILL) }
func (g groupHandle) release()   {}

func exitStatus(ee *exec.ExitError) (code int, signal string) {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return -1, signalName(ws.Signal())
	}
	return ee.ExitCode(), ""
}

func signalName(s syscall.Signal) string {
	switch s {
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGSEGV:
		return "SIGSEGV"
	case syscall.SIGABRT:
		return "SIGABRT"
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGBUS:
		return "SIGBUS"
	}
	return s.String()
}

// Alive reports whether a process with pid exists. Signal 0 performs only the
// existence and permission check; nothing is delivered.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
