//go:build linux || darwin || windows

package service

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// execTimeout bounds one call into the operating system's scheduler CLI.
//
// Every one of these — `systemctl --user enable --now`, `launchctl load`,
// `schtasks /Create` — is a local, short-lived command, and `service install`
// runs a handful of them in sequence while an agent or a human waits. Without a
// deadline a single wedged invocation (a hung D-Bus, a `loginctl` waiting on
// polkit) hangs the CLI with no output and no way to tell what it is blocked
// on. 30s is far above the observed cost of any of them and far below a
// human's patience.
const execTimeout = 30 * time.Second

// run executes an OS-scheduler command and returns its combined output with
// surrounding whitespace trimmed, alongside the process error.
//
// The output is returned even when err is non-nil, and every caller depends on
// that: a backend distinguishes "the entry is not there" from "the command
// failed" by matching on the text (`launchctl` prints "Could not find",
// `schtasks` prints "cannot find"), because neither tool gives it a distinct
// exit code.
//
// One implementation rather than one per platform: the three backends are
// build-tagged, but the shape of this call is not.
func run(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
