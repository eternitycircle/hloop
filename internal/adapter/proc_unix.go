//go:build !windows

package adapter

import (
	"os/exec"
	"syscall"
	"time"
)

// prepare puts the harness in its own process group and makes cancellation
// kill the whole group, so child processes cannot outlive a timeout.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 3 * time.Second
}
