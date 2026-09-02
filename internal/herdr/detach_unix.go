//go:build !windows

package herdr

import (
	"os/exec"
	"syscall"
)

// detach gives the spawned server its own session, so it neither dies with the runner nor
// inherits its controlling terminal.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
