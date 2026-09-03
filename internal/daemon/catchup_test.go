package daemon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-co-op/gocron/v2"

	"github.com/huketo/herdr-cron/internal/model"
	"github.com/huketo/herdr-cron/internal/paths"
)

func everyJob(sec int64) *model.Resolved {
	return &model.Resolved{
		ID: "tick",
		Schedule: model.ResolvedSchedule{
			Type: "every", EverySec: sec, Timezone: "UTC", Catchup: model.CatchupLatest,
		},
	}
}

// The catch-up pass enumerates strictly the occurrences inside (from, to); gocron itself
// discards every missed tick, so this is the only thing standing between a slept laptop
// and a silently skipped job (docs/spec/03-job-model.md §4.1).
func TestOccurrencesAreBounded(t *testing.T) {
	d := &Daemon{}
	from := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	to := from.Add(60 * time.Second)

	got := d.occurrences(everyJob(10), from, to)
	if len(got) != 5 {
		t.Fatalf("got %d occurrences, want 5 in (10:00:00, 10:01:00) at 10s", len(got))
	}
	if !got[0].Equal(from.Add(10 * time.Second)) {
		t.Errorf("first occurrence is %s, want the one strictly after `from`", got[0])
	}
	for _, o := range got {
		if !o.After(from) || !o.Before(to) {
			t.Errorf("%s is outside the half-open window (%s, %s)", o, from, to)
		}
	}
}

// A one-time job has nothing to replay: firing a past `at:` on every restart would be a
// surprise, not a catch-up.
func TestOneTimeJobsAreNeverReplayed(t *testing.T) {
	d := &Daemon{}
	j := &model.Resolved{ID: "once", Schedule: model.ResolvedSchedule{
		Type: "at", At: "2026-01-01T00:00:00Z", Timezone: "UTC",
	}}
	if got := d.occurrences(j, time.Time{}, time.Now()); len(got) != 0 {
		t.Fatalf("got %d occurrences for a one-time job, want 0", len(got))
	}
}

// The enumeration must terminate even when the window is absurdly wide, or a daemon start
// after a long outage would hang instead of scheduling.
func TestOccurrencesTerminateOnAWideWindow(t *testing.T) {
	d := &Daemon{}
	from := time.Now().Add(-365 * 24 * time.Hour)
	done := make(chan int, 1)
	go func() { done <- len(d.occurrences(everyJob(1), from, time.Now())) }()
	select {
	case n := <-done:
		if n > catchupCap*2+1 {
			t.Fatalf("enumeration returned %d occurrences; it must stop near the cap", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("occurrences did not terminate")
	}
}

// The identifier must be stable across restarts, or every reload would look like a new
// job to gocron.
func TestStableIDIsDeterministic(t *testing.T) {
	a, b := stableID("nightly-deps"), stableID("nightly-deps")
	if a != b {
		t.Fatalf("stableID is not deterministic: %s vs %s", a, b)
	}
	if a == stableID("nightly-dep") {
		t.Fatal("two different job ids produced the same identifier")
	}
}

func newOneShotDaemon(t *testing.T, at time.Time, catchup model.Catchup, window string) (*Daemon, paths.Roots) {
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
  - id: once
    schedule:
      at: %q
      catchup: %s
      catchup_window: %s
    kind: shell
    shell:
      command: "true"
`, at.Format(time.RFC3339), catchup, window)
	if err := os.WriteFile(roots.JobsFile(), []byte(body), 0o644); err != nil {
		t.Fatalf("write jobs.yaml: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := New(roots, log, "test", "test")
	d.reload()
	d.mu.Lock()
	loaded, configErr := d.loaded, d.configErr
	d.mu.Unlock()
	if configErr != nil {
		t.Fatalf("reload rejected jobs.yaml: %s", *configErr)
	}
	if loaded == nil {
		t.Fatal("reload did not load jobs.yaml")
	}
	return d, roots
}

func oneShotRuns(t *testing.T, d *Daemon) []*model.Run {
	t.Helper()
	runs, err := d.store.Runs("once")
	if err != nil {
		t.Fatalf("read runs: %v", err)
	}
	return runs
}

func requireSkippedOneShot(t *testing.T, d *Daemon, reason string) {
	t.Helper()
	runs := oneShotRuns(t, d)
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want exactly one skipped run", len(runs))
	}
	if runs[0].Status != model.StatusSkipped {
		t.Fatalf("status = %q, want %q", runs[0].Status, model.StatusSkipped)
	}
	if runs[0].Reason == nil || *runs[0].Reason != reason {
		t.Fatalf("reason = %v, want %q", runs[0].Reason, reason)
	}
}

// A one-time Occurrence missed during a short daemon outage still belongs to the job.
func TestReconcileOneShotRunsWithinCatchupWindow(t *testing.T) {
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	d, _ := newOneShotDaemon(t, at, model.CatchupLatest, "1h")

	d.reconcileOneShots(context.Background())

	runs := oneShotRuns(t, d)
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want one", len(runs))
	}
	run := runs[0]
	if run.Status != model.StatusSuccess {
		t.Fatalf("status = %q, want %q", run.Status, model.StatusSuccess)
	}
	if run.Trigger != model.TriggerCatchup {
		t.Errorf("trigger = %q, want %q", run.Trigger, model.TriggerCatchup)
	}
	if run.ScheduledAt == nil || !run.ScheduledAt.Equal(at) {
		t.Errorf("scheduledAt = %v, want %s", run.ScheduledAt, at.Format(time.RFC3339))
	}
}

// Once the catch-up window closes, history must explain the missed Occurrence instead of
// either executing it late or letting it disappear.
func TestReconcileOneShotRecordsMissedWindow(t *testing.T) {
	at := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	d, _ := newOneShotDaemon(t, at, model.CatchupLatest, "1h")

	d.reconcileOneShots(context.Background())

	requireSkippedOneShot(t, d, model.ReasonMissedWindow)
}

// catchup: off declines execution, but the refusal still consumes and records the Occurrence.
func TestReconcileOneShotRecordsCatchupOff(t *testing.T) {
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	d, _ := newOneShotDaemon(t, at, model.CatchupOff, "1h")

	d.reconcileOneShots(context.Background())

	requireSkippedOneShot(t, d, model.ReasonCatchupOff)
}

// A second reconciliation must see the watermark written by the first one; otherwise every
// reload would append another account of the same Occurrence.
func TestReconcileOneShotIsIdempotent(t *testing.T) {
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	d, roots := newOneShotDaemon(t, at, model.CatchupLatest, "1h")

	d.reconcileOneShots(context.Background())
	before, err := os.ReadFile(roots.RunsFile("once"))
	if err != nil {
		t.Fatalf("read first run history: %v", err)
	}
	d.reconcileOneShots(context.Background())
	after, err := os.ReadFile(roots.RunsFile("once"))
	if err != nil {
		t.Fatalf("read second run history: %v", err)
	}

	if !bytes.Equal(after, before) {
		t.Fatal("the second reconciliation appended another record")
	}
	if runs := oneShotRuns(t, d); len(runs) != 1 {
		t.Fatalf("got %d runs after two reconciliations, want one", len(runs))
	}
}

// Reconciliation must not claim a future Occurrence before gocron gets the chance to fire it.
func TestReconcileOneShotLeavesFutureOccurrenceUntouched(t *testing.T) {
	at := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	d, roots := newOneShotDaemon(t, at, model.CatchupLatest, "1h")

	d.reconcileOneShots(context.Background())

	if runs := oneShotRuns(t, d); len(runs) != 0 {
		t.Fatalf("got %d runs for a future Occurrence, want none", len(runs))
	}
	if _, err := os.Stat(roots.StateFile()); !os.IsNotExist(err) {
		t.Fatalf("state.json was written for a future Occurrence: %v", err)
	}
}

// gocron rejects a past OneTimeJob, so rebuild must leave its disposition to reconciliation.
func TestRebuildExcludesPastOneShot(t *testing.T) {
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	d, _ := newOneShotDaemon(t, at, model.CatchupLatest, "1h")
	sched, err := gocron.NewScheduler(gocron.WithLocation(time.Local))
	if err != nil {
		t.Fatalf("create scheduler: %v", err)
	}
	t.Cleanup(func() { _ = sched.Shutdown() })
	d.sched = sched

	d.rebuild()

	if jobs := sched.Jobs(); len(jobs) != 0 {
		t.Fatalf("gocron has %d jobs, want no past one-time job", len(jobs))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.ids) != 0 {
		t.Fatalf("daemon retained %d scheduler ids, want none", len(d.ids))
	}
}
