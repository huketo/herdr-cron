// Package service registers herdr-cron with the operating system's own scheduler.
//
// Two drivers (docs/spec/02-architecture.md §2, §4):
//
//   - DriverDaemon installs one unit that runs `herdr-cron daemon`.
//   - DriverOSScheduler installs one entry per job, each exec'ing `herdr-cron run-once`.
//     Only this driver gets the OS's own catch-up (systemd `Persistent=true`).
//
// Every generated artefact is marker-fenced with a hash of its content, so an entry
// herdr-cron wrote can be recognised, compared, and removed without touching anything a
// human put there — the reversible-registration idea from `@dortort/scheduler`.
package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/huketo/herdr-cron/internal/model"
	"github.com/huketo/herdr-cron/internal/paths"
)

// Driver selects what gets registered.
type Driver string

// The values `service install --driver` accepts (docs/spec/05-cli.md §3.4). The
// architecture names a third driver, `foreground`, which is deliberately absent here: it
// registers nothing with the OS and so never reaches a backend
// (docs/spec/02-architecture.md §2.2).
const (
	DriverDaemon      Driver = "daemon"
	DriverOSScheduler Driver = "os-scheduler"
)

// ErrUnsupported means this platform has no backend in this build.
var ErrUnsupported = errors.New("no service backend for this platform")

// ErrUntranslatable means a schedule cannot be expressed in the OS scheduler's grammar,
// which aborts that job rather than registering something that fires at the wrong time.
var ErrUntranslatable = errors.New("schedule cannot be translated for the OS scheduler")

// Entry is one registered artefact.
type Entry struct {
	JobID  string `json:"jobId,omitempty"`
	Name   string `json:"name"`
	Path   string `json:"path,omitempty"`
	State  string `json:"state"` // ok | missing | stale | orphan | error
	Detail string `json:"detail,omitempty"`
}

// Plan is what an install would do, or what a status found.
type Plan struct {
	Driver   Driver   `json:"driver"`
	Backend  string   `json:"backend"`
	Entries  []Entry  `json:"entries"`
	Warnings []string `json:"warnings,omitempty"`
}

// Backend is one operating system's registration mechanism.
type Backend interface {
	// Name is the backend's identifier, e.g. "systemd-user".
	Name() string
	// Install writes and enables the artefacts, returning what it did.
	Install(req Request) ([]Entry, []string, error)
	// Uninstall removes every artefact herdr-cron owns.
	Uninstall(req Request) ([]Entry, error)
	// Status reports what is registered and whether it matches the request.
	Status(req Request) ([]Entry, error)
}

// Request is the input to every backend call.
type Request struct {
	Driver Driver
	Roots  paths.Roots
	Binary string
	Jobs   []*model.Resolved
	// NextRun answers "when does this job fire next", jitter included, so a backend can
	// self-check its own schedule translation against herdr-cron's own prediction.
	NextRun func(*model.Resolved) (string, error)
}

// marker fences an artefact so it can be recognised later.
func marker(jobID, body string) (head, tail string) {
	sum := sha256.Sum256([]byte(body))
	return fmt.Sprintf("# herdr-cron:%s:begin\n# herdr-cron:%s:sha256=%s\n",
			jobID, jobID, hex.EncodeToString(sum[:])),
		fmt.Sprintf("# herdr-cron:%s:end\n", jobID)
}

// fenced wraps a body in its markers.
func fenced(jobID, body string) string {
	head, tail := marker(jobID, body)
	return head + body + tail
}

// ownedBy reports whether a file on disk carries herdr-cron's fence for this job.
func ownedBy(path, jobID string) (owned bool, body string, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, "", err
	}
	text := string(b)
	begin := fmt.Sprintf("# herdr-cron:%s:begin", jobID)
	end := fmt.Sprintf("# herdr-cron:%s:end", jobID)
	if !strings.Contains(text, begin) || !strings.Contains(text, end) {
		return false, text, nil
	}
	return true, text, nil
}

// entryState compares what is on disk with what would be generated now.
func entryState(path, jobID, want string) Entry {
	e := Entry{JobID: jobID, Name: filepath.Base(path), Path: path}
	owned, body, err := ownedBy(path, jobID)
	switch {
	case os.IsNotExist(err):
		e.State = "missing"
		return e
	case err != nil:
		e.State = "error"
		e.Detail = err.Error()
		return e
	case !owned:
		// Something else owns this path. Never touched, never repaired.
		e.State = "orphan"
		e.Detail = "the file exists but carries no herdr-cron marker"
		return e
	case body != want:
		e.State = "stale"
		e.Detail = "the registered artefact differs from what this version would generate"
		return e
	}
	e.State = "ok"
	return e
}

// writeFenced writes a generated artefact, refusing to clobber a file it does not own.
func writeFenced(path, jobID, body string) error {
	if owned, _, err := ownedBy(path, jobID); err == nil && !owned {
		return fmt.Errorf("%s exists and is not herdr-cron's; refusing to overwrite it", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

// removeFenced deletes an artefact only when herdr-cron owns it. "It is already gone" is a
// benign race that converges.
func removeFenced(path, jobID string) (Entry, error) {
	e := Entry{JobID: jobID, Name: filepath.Base(path), Path: path}
	owned, _, err := ownedBy(path, jobID)
	switch {
	case os.IsNotExist(err):
		e.State = "missing"
		return e, nil
	case err != nil:
		e.State = "error"
		e.Detail = err.Error()
		return e, err
	case !owned:
		e.State = "orphan"
		e.Detail = "left in place: no herdr-cron marker"
		return e, nil
	}
	if err := os.Remove(path); err != nil {
		e.State = "error"
		e.Detail = err.Error()
		return e, err
	}
	e.State = "missing"
	return e, nil
}
