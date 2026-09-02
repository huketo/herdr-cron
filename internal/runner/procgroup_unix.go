//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
	"time"
)

// waitDelay bounds how long Wait blocks on inherited pipe writers after the leader dies.
const waitDelay = 5 * time.Second

// configureProcessGroup puts the child in its own process group and makes cancellation
// kill the whole group (docs/spec/03-job-model.md §3.1).
//
// Killing only the direct child is not enough: `sh -c "sleep 20; echo done"` cannot exec
// in place, so the shell stays and `sleep` becomes a grandchild that both survives the
// kill and holds the output pipe open, which leaves the run recorded as `running`.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// A negative pid signals the whole process group.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = waitDelay
}
