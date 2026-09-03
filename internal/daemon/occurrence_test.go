package daemon

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/huketo/herdr-cron/internal/paths"
)

// newDaemonWithJob writes one job and loads it, without starting a scheduler.
func newDaemonWithJob(t *testing.T, id, scheduleBody, command string) (*Daemon, paths.Roots) {
	t.Helper()

	root := t.TempDir()
	roots := paths.Roots{
		Config: filepath.Join(root, "config"),
		State:  filepath.Join(root, "state"),
	}
	if err := os.MkdirAll(roots.Config, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := roots.EnsureState(); err != nil {
		t.Fatalf("ensure state: %v", err)
	}
	body := fmt.Sprintf(`version: 1
jobs:
  - id: %s
    schedule:
%s      jitter: off
    kind: shell
    shell:
      command: %q
`, id, scheduleBody, command)
	if err := os.WriteFile(roots.JobsFile(), []byte(body), 0o644); err != nil {
		t.Fatalf("write jobs.yaml: %v", err)
	}

	d := New(roots, slog.New(slog.NewTextHandler(io.Discard, nil)), "test", "test")
	d.reload()
	d.mu.Lock()
	configErr := d.configErr
	d.mu.Unlock()
	if configErr != nil {
		t.Fatalf("reload rejected jobs.yaml: %s", *configErr)
	}
	return d, roots
}

// A scheduled run must be recorded against the Occurrence it belongs to, not the clock that
// happened to trip. gocron fires a few seconds early on a stepping clock, and a watermark
// written before its own Occurrence leaves that Occurrence looking missed: the next
// reconciliation pass then replays a job that already ran, which for kind: agent is a paid
// invocation nobody asked for (issue #12).
func TestFireRecordsTheOccurrenceNotTheClock(t *testing.T) {
	// A five-second grid keeps the test instant: every wall clock lies within FireSkew of an
	// Occurrence, which is the situation a real scheduler fire is always in.
	d, _ := newDaemonWithJob(t, "tick", "      cron: \"*/5 * * * * *\"\n", "echo fired")

	d.fire("tick")

	runs, err := d.store.Runs("tick")
	if err != nil {
		t.Fatalf("read runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want one", len(runs))
	}
	at := runs[0].ScheduledAt
	if at == nil {
		t.Fatal("the run records no scheduledAt")
	}
	if at.Second()%5 != 0 || at.Nanosecond() != 0 {
		t.Errorf("scheduledAt = %s, want a five-second boundary of `*/5 * * * * *`", at.Format(time.RFC3339Nano))
	}
	if at.After(time.Now()) {
		t.Errorf("scheduledAt = %s is in the future; a run belongs to an occurrence that came due", at)
	}
	if d := time.Since(*at); d > time.Minute {
		t.Errorf("scheduledAt = %s is %s old; it is not the occurrence just fired", at, d)
	}

	state, err := d.store.LoadState()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	js := state.Jobs["tick"]
	if js == nil || js.LastScheduledAt == nil {
		t.Fatal("the run left no catch-up watermark")
	}
	if !js.LastScheduledAt.Equal(*at) {
		t.Errorf("watermark = %s, want the occurrence %s", js.LastScheduledAt, at)
	}
	// The watermark now sits on the Occurrence, so nothing between it and now is missing.
	if got := d.occurrences(d.job("tick"), *js.LastScheduledAt, time.Now()); len(got) != 0 {
		t.Errorf("catch-up would replay %d occurrence(s) the scheduler just ran: %v", len(got), got)
	}
}
