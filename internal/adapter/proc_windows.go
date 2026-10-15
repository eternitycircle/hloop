//go:build windows

package adapter

import (
	"os/exec"
	"strconv"
	"time"
)

// prepare makes cancellation kill the harness and everything it started.
func prepare(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
	cmd.WaitDelay = 3 * time.Second
}
