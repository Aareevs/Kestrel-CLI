//go:build windows

package integration

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"github.com/Aareevs/Kestrel-CLI/internal/process"
)

var (
	modpsapi                 = syscall.NewLazyDLL("psapi.dll")
	procGetProcessMemoryInfo = modpsapi.NewProc("GetProcessMemoryInfo")
)

type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

func setProcGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

func killProcGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func sendCtrlC(cmd *exec.Cmd) {
	if cmd.Process != nil {
		// On Windows, os.Interrupt delivers a console Ctrl+C event
		_ = cmd.Process.Signal(os.Interrupt)
	}
}

func rssKB(pid int) (int, error) {
	const processQueryInformation = 0x0400
	const processVMRead = 0x0010
	h, err := syscall.OpenProcess(processQueryInformation|processVMRead, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	defer syscall.CloseHandle(h)
	var counters processMemoryCounters
	counters.CB = uint32(unsafe.Sizeof(counters))
	r, _, err := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&counters)), uintptr(counters.CB))
	if r == 0 {
		return 0, err
	}
	return int(counters.WorkingSetSize / 1024), nil
}

func running(pid int) bool {
	return process.Alive(pid)
}

func alive(pid int) bool {
	return process.Alive(pid)
}
