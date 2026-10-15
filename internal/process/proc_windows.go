//go:build windows

package process

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32                      = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW          = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject   = kernel32.NewProc("SetInformationJobObject")
	procQueryInformationJobObject = kernel32.NewProc("QueryInformationJobObject")
	procAssignProcessToJobObject  = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject        = kernel32.NewProc("TerminateJobObject")
	procGenerateConsoleCtrlEvent  = kernel32.NewProc("GenerateConsoleCtrlEvent")
)

const (
	jobObjectBasicAccountingInformation = 1
	jobObjectExtendedLimitInformation   = 9
	jobObjectLimitKillOnJobClose        = 0x2000
	processSetQuota                     = 0x0100
	processTerminate                    = 0x0001
	processQueryLimitedInformation      = 0x1000
	ctrlBreakEvent                      = 1
	stillActive                         = 259
)

type jobBasicLimit struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount, WriteOperationCount, OtherOperationCount uint64
	ReadTransferCount, WriteTransferCount, OtherTransferCount    uint64
}

type jobExtendedLimit struct {
	Basic                 jobBasicLimit
	IO                    ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type jobBasicAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

func normalizeCommandLine(line string) string {
	line = strings.TrimSpace(line)
	// cmd.exe parses '/' as a switch delimiter, so leading './' causes '.'
	// to be treated as a command with switch '/...'. Replace './' with '.\'.
	if strings.HasPrefix(line, `"./`) {
		closing := strings.Index(line[1:], `"`)
		if closing != -1 {
			target := line[1 : 1+closing]
			norm := ".\\" + strings.ReplaceAll(target[2:], "/", "\\")
			return `"` + norm + `"` + line[1+closing+1:]
		}
	}
	if strings.HasPrefix(line, "./") {
		firstEnd := strings.IndexAny(line, " \t")
		if firstEnd == -1 {
			return ".\\" + strings.ReplaceAll(line[2:], "/", "\\")
		}
		return ".\\" + strings.ReplaceAll(line[2:firstEnd], "/", "\\") + line[firstEnd:]
	}
	return line
}

func shellCommand(line string) *exec.Cmd {
	norm := normalizeCommandLine(line)
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /S /C "` + norm + `"`}
	return cmd
}

// prepare starts the child in a new console process group so it can receive
// CTRL_BREAK without Kestrel receiving it too.
func prepare(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// groupHandle is a Job Object that contains the child and everything it
// spawns afterwards. KILL_ON_JOB_CLOSE means the job dies with Kestrel even
// if Kestrel is killed abruptly. Processes the child spawned before it was
// assigned to the job (a narrow window after start) are not covered.
type groupHandle struct {
	job syscall.Handle
	pid int
}

func attach(cmd *exec.Cmd) (groupHandle, error) {
	g := groupHandle{pid: cmd.Process.Pid}
	h, _, err := procCreateJobObjectW.Call(0, 0)
	if h == 0 {
		return g, fmt.Errorf("CreateJobObject: %w", err)
	}
	g.job = syscall.Handle(h)
	var info jobExtendedLimit
	info.Basic.LimitFlags = jobObjectLimitKillOnJobClose
	if r, _, err := procSetInformationJobObject.Call(uintptr(g.job), jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)); r == 0 {
		return g, fmt.Errorf("SetInformationJobObject: %w", err)
	}
	ph, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(g.pid))
	if err != nil {
		return g, fmt.Errorf("OpenProcess: %w", err)
	}
	defer syscall.CloseHandle(ph)
	if r, _, err := procAssignProcessToJobObject.Call(uintptr(g.job), uintptr(ph)); r == 0 {
		return g, fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	return g, nil
}

func (g groupHandle) alive() bool {
	if g.job == 0 {
		return Alive(g.pid)
	}
	var acct jobBasicAccounting
	r, _, _ := procQueryInformationJobObject.Call(uintptr(g.job), jobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&acct)), unsafe.Sizeof(acct), 0)
	if r == 0 {
		return Alive(g.pid)
	}
	return acct.ActiveProcesses > 0
}

func (g groupHandle) terminate() {
	_, _, _ = procGenerateConsoleCtrlEvent.Call(ctrlBreakEvent, uintptr(g.pid))
}

func (g groupHandle) kill() {
	if g.job != 0 {
		_, _, _ = procTerminateJobObject.Call(uintptr(g.job), 1)
	}
}

func (g groupHandle) release() {
	if g.job != 0 {
		syscall.CloseHandle(g.job)
	}
}

func exitStatus(ee *exec.ExitError) (int, string) { return ee.ExitCode(), "" }

// Alive reports whether a process with pid is still running.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
