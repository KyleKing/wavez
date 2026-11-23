package gate

import (
	"os/exec"
	"syscall"
)

// ownGroup starts cmd in a process group of its own and makes canceling it
// kill the whole group, so a check's children do not outlive the check.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
