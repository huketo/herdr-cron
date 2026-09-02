//go:build windows

package cli

import (
	"os/exec"
	"syscall"
)

// detachProcess is the Windows half: no console, own process group, so the daemon
// outlives the caller.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x08000000, // DETACHED_PROCESS
	}
}
