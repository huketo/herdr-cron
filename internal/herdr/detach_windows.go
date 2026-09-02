//go:build windows

package herdr

import (
	"os/exec"
	"syscall"
)

// detach gives the spawned server no console and its own process group.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x08000000, // DETACHED_PROCESS
	}
}
