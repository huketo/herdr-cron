package daemon

// Client half of the trigger-file protocol (docs/spec/04-storage.md §8). It lives here so
// the CLI and the TUI share one implementation rather than two.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/huketo/herdr-cron/internal/paths"
)

// TriggerGrace is how long a client waits for a daemon to claim its request before
// reporting daemon_unreachable.
const TriggerGrace = 3 * time.Second

// WriteTrigger stages a request in tmp/ and renames it into triggers/, returning the
// final path so the caller can hand it to AwaitTrigger or delete it. Only the rename
// publishes the file: a half-written trigger must never be readable by the daemon
// (docs/spec/04-storage.md §8).
func WriteTrigger(roots paths.Roots, tr Trigger) (string, error) {
	if err := roots.EnsureState(); err != nil {
		return "", err
	}
	b, err := json.Marshal(tr)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(roots.TmpDir(), "trigger.*.json")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	// Staged then renamed: a half-written trigger must never be readable.
	final := filepath.Join(roots.TriggersDir(), tr.ID+".json")
	if err := os.Rename(name, final); err != nil {
		return "", err
	}
	return final, nil
}

// AwaitTrigger waits for the daemon to act on a request written by WriteTrigger. The
// daemon claims a trigger by renaming it to <id>.claimed and answers in <id>.result, so
// waiting is polling at 100 ms — there is no socket to be notified over, which is what
// puts 100-300 ms under `job run --wait`. With wait false it returns as soon as the claim
// is visible; if nothing claims within TriggerGrace it removes the file and reports the
// daemon unreachable (docs/spec/04-storage.md §8).
func AwaitTrigger(roots paths.Roots, tr Trigger, path string, wait bool) (*TriggerResult, error) {
	resultPath := filepath.Join(roots.TriggersDir(), tr.ID+".result")
	claimed := filepath.Join(roots.TriggersDir(), tr.ID+".claimed")

	deadline := time.Now().Add(TriggerGrace)
	claimSeen := false
	for {
		if b, err := os.ReadFile(resultPath); err == nil {
			var res TriggerResult
			if err := json.Unmarshal(b, &res); err == nil {
				_ = os.Remove(resultPath)
				return &res, nil
			}
		}
		if !claimSeen {
			if _, err := os.Stat(claimed); err == nil {
				claimSeen = true
			} else if _, err := os.Stat(path); err != nil {
				claimSeen = true // claimed and already finished
			}
		}
		if !claimSeen && time.Now().After(deadline) {
			_ = os.Remove(path)
			return nil, errors.New("no daemon claimed the request")
		}
		if claimSeen && !wait {
			return nil, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// NewTriggerID mints the stem shared by all three files of one exchange — <id>.json,
// <id>.claimed, <id>.result. Nanosecond clock plus PID: two clients racing on one machine
// cannot collide, and no coordination with the daemon is needed to pick a name.
func NewTriggerID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
}

// ReadHeartbeat returns daemon.json, or nil when it is absent or unreadable.
func ReadHeartbeat(roots paths.Roots) *Heartbeat {
	b, err := os.ReadFile(roots.DaemonFile())
	if err != nil {
		return nil
	}
	var hb Heartbeat
	if err := json.Unmarshal(b, &hb); err != nil {
		return nil
	}
	return &hb
}

// LockHeld reports whether another process holds daemon.lock. It is the authoritative
// half of the liveness test: a kill -9 leaves a heartbeat that stays fresh for a minute,
// while the kernel releases the lock immediately (docs/spec/04-storage.md §7).
func LockHeld(roots paths.Roots) bool {
	lock := flock.New(filepath.Join(roots.State, "daemon.lock"))
	got, err := lock.TryLock()
	if err != nil {
		return false
	}
	if got {
		_ = lock.Unlock()
		return false
	}
	return true
}
