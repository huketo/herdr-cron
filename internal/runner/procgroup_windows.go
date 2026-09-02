//go:build windows

package runner

import (
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// waitDelay bounds how long Wait blocks on inherited pipe writers after the leader dies.
const waitDelay = 5 * time.Second

// configureProcessGroup is the Windows half of the split in docs/spec/03-job-model.md §3.1.
// taskkill /T walks the child tree, which is what kills a grandchild holding the output
// pipe open; Process.Kill alone would not.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = waitDelay
}
