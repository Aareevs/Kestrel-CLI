package process

import "syscall"

// setPdeathsig asks Linux to SIGKILL the group leader if Kestrel itself dies
// abruptly (e.g. kill -9), so a crashed Kestrel does not leave it running.
// Grandchildren are not covered; see docs/security.md.
func setPdeathsig(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }
