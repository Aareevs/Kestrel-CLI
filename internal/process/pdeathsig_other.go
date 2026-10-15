//go:build !linux && !windows

package process

import "syscall"

// setPdeathsig is a no-op: only Linux has PR_SET_PDEATHSIG.
func setPdeathsig(*syscall.SysProcAttr) {}
