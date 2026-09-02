//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

// detachProcess puts the spawned daemon in its own session so it survives the caller,
// which is what a Herdr [[startup]] hook needs: the hook must exit
// (docs/spec/07-herdr-integration.md §8).
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
