// Package daemon implements the long-lived driver of docs/spec/02-architecture.md §3.
//
// It owns a gocron scheduler, a reconciliation pass for missed runs, a jobs.yaml watcher,
// the trigger-file channel, and the heartbeat. It is one of three drivers over the same
// run-once primitive; the store, not this process, holds the truth.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-co-op/gocron/v2"
	"github.com/gofrs/flock"
	"github.com/google/uuid"

	"github.com/huketo/herdr-cron/internal/config"
	"github.com/huketo/herdr-cron/internal/model"
	"github.com/huketo/herdr-cron/internal/paths"
	"github.com/huketo/herdr-cron/internal/runner"
	"github.com/huketo/herdr-cron/internal/schedule"
	"github.com/huketo/herdr-cron/internal/store"
)

const (
	heartbeatInterval = 15 * time.Second
	pollInterval      = 5 * time.Second
	debounce          = 200 * time.Millisecond
	clockTick         = 30 * time.Second
	// A wall-clock jump larger than this between two ticks means the machine slept
	// (docs/spec/03-job-model.md §4.2).
	sleepThreshold = 90 * time.Second
	shutdownGrace  = 30 * time.Second
	catchupCap     = 100
)

// Heartbeat is the contents of daemon.json (docs/spec/04-storage.md §7).
type Heartbeat struct {
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"startedAt"`
	HeartbeatAt time.Time `json:"heartbeatAt"`
	Version     string    `json:"version"`
	Driver      string    `json:"driver"`
	ConfigPath  string    `json:"configPath"`
	JobCount    int       `json:"jobCount"`
	ConfigError *string   `json:"configError"`
}

// ErrAlreadyRunning is returned when another daemon holds the lock.
var ErrAlreadyRunning = errors.New("another daemon holds the lock")

// Daemon is a single scheduler process.
type Daemon struct {
	roots   paths.Roots
	store   *store.Store
	log     *slog.Logger
	version string
	driver  string
	started time.Time

	mu        sync.Mutex
	oneShotMu sync.Mutex
	ctx       context.Context
	loaded    *config.Loaded
	configErr *string
	ids       map[string]uuid.UUID               // job id -> gocron identifier
	cancels   map[string]context.CancelCauseFunc // job id -> in-flight run

	sched gocron.Scheduler
}

// New builds a daemon bound to one pair of roots. It performs no I/O and takes
// no lock: Run does both, so a caller can construct a daemon and then decide
// whether it is the one that gets to hold daemon.lock.
//
// version and driver are recorded verbatim in the heartbeat, which is what
// `status` reports back (docs/spec/04-storage.md §7).
func New(roots paths.Roots, log *slog.Logger, version, driver string) *Daemon {
	return &Daemon{
		roots: roots, store: store.New(roots), log: log,
		version: version, driver: driver,
		ids: map[string]uuid.UUID{}, cancels: map[string]context.CancelCauseFunc{},
	}
}

// Run holds the lock and serves the schedule until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	if err := d.roots.EnsureState(); err != nil {
		return err
	}
	lock := flock.New(filepath.Join(d.roots.State, "daemon.lock"))
	got, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !got {
		return ErrAlreadyRunning
	}
	defer func() {
		_ = lock.Unlock()
		_ = os.Remove(d.roots.DaemonFile())
	}()

	d.started = time.Now()
	d.mu.Lock()
	d.ctx = ctx
	d.mu.Unlock()

	// A "running" record with no terminal partner and no live process is a crash, and
	// saying so is more honest than leaving it running forever
	// (docs/spec/04-storage.md §5).
	d.closeOrphanedRuns()

	d.reload()

	sched, err := gocron.NewScheduler(
		gocron.WithLocation(time.Local),
		// Without this listener a single panicking task kills the process
		// (docs/research/2026-09-02-gocron-scheduling-engine.md §6).
		gocron.WithGlobalJobOptions(gocron.WithEventListeners(
			gocron.AfterJobRunsWithPanic(func(id uuid.UUID, name string, recoverData any) {
				d.log.Error("job panicked", "job", name, "panic", fmt.Sprint(recoverData))
			}),
		)),
	)
	if err != nil {
		return err
	}
	d.sched = sched

	d.rebuild()
	d.catchUp(ctx)
	d.reconcileOneShots(ctx)
	sched.Start()

	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); d.heartbeatLoop(ctx) }()
	go func() { defer wg.Done(); d.watchLoop(ctx) }()
	go func() { defer wg.Done(); d.triggerLoop(ctx) }()
	go func() { defer wg.Done(); d.clockLoop(ctx) }()

	d.log.Info("herdr-cron daemon started", "pid", os.Getpid(), "config", d.roots.JobsFile())
	<-ctx.Done()
	d.log.Info("shutting down")

	// StopJobs stops the schedule; in-flight runs are cancelled explicitly, because
	// gocron cannot reach into a task's own context for us.
	stopped := make(chan error, 1)
	go func() { stopped <- sched.StopJobs() }()
	select {
	case <-stopped:
	case <-time.After(shutdownGrace):
		d.log.Warn("timed out waiting for jobs to stop")
	}
	d.mu.Lock()
	for _, cancel := range d.cancels {
		cancel(runner.ErrShutdown)
	}
	d.mu.Unlock()
	_ = sched.Shutdown()
	wg.Wait()
	return nil
}

// ---------------------------------------------------------------- config load

func (d *Daemon) reload() {
	loaded, errs := config.Load(d.roots.JobsFile())
	if len(errs) > 0 {
		msgs := make([]string, 0, len(errs))
		for _, e := range errs {
			msgs = append(msgs, e.String())
		}
		joined := strings.Join(msgs, "; ")
		d.mu.Lock()
		d.configErr = &joined
		d.mu.Unlock()
		// A broken file is rejected wholesale; the previous schedule keeps running.
		d.log.Error("jobs.yaml is invalid; keeping the previous schedule", "error", joined)
		return
	}
	d.mu.Lock()
	d.loaded = loaded
	d.configErr = nil
	d.mu.Unlock()
	for _, w := range loaded.Warnings {
		d.log.Warn("validation warning", "job", w.JobID, "code", w.Code, "message", w.Message)
	}
}

// enabledJobs resolves the effective enabled state for every job.
func (d *Daemon) enabledJobs() []*model.Resolved {
	d.mu.Lock()
	loaded := d.loaded
	d.mu.Unlock()
	if loaded == nil {
		return nil
	}
	ov, err := d.store.LoadOverrides()
	if err != nil {
		d.log.Warn("cannot read overrides.json", "error", err)
		ov = &store.Overrides{Overrides: map[string]*store.Override{}}
	}
	out := []*model.Resolved{}
	for _, j := range loaded.Jobs {
		enabled, _ := store.EffectiveEnabled(j.Enabled, ov.Overrides[j.ID])
		if enabled {
			out = append(out, j)
		}
	}
	return out
}

func (d *Daemon) job(id string) *model.Resolved {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.loaded == nil {
		return nil
	}
	j, _ := d.loaded.Job(id)
	return j
}

// -------------------------------------------------------------- scheduling

// rebuild makes the gocron scheduler match the current file, preserving identifiers so a
// reload does not restart every timer for jobs that did not change.
func (d *Daemon) rebuild() {
	if d.sched == nil {
		return
	}
	want := map[string]*model.Resolved{}
	now := time.Now()
	for _, j := range d.enabledJobs() {
		if at, ok := j.Schedule.Instant(); ok && !at.After(now) {
			// Reconciliation, not gocron, decides whether a past one-time
			// Occurrence runs or is recorded as skipped.
			continue
		}
		want[j.ID] = j
	}

	d.mu.Lock()
	current := make(map[string]uuid.UUID, len(d.ids))
	for k, v := range d.ids {
		current[k] = v
	}
	d.mu.Unlock()

	for id, gid := range current {
		if _, keep := want[id]; !keep {
			// A job that left jobs.yaml must stop firing. RemoveJob only fails
			// when the identifier is already gone, which is harmless here but
			// means the local map and the scheduler have diverged — log it
			// rather than discard it, because the next reload would otherwise
			// keep trying to remove the same phantom.
			if err := d.sched.RemoveJob(gid); err != nil {
				d.log.Warn("cannot remove job from the scheduler", "job", id, "error", err)
			}
			d.mu.Lock()
			delete(d.ids, id)
			d.mu.Unlock()
		}
	}

	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		j := want[id]
		def, err := definition(j)
		if err != nil {
			d.log.Error("cannot schedule job", "job", id, "error", err)
			continue
		}
		gid := stableID(id)
		opts := []gocron.JobOption{gocron.WithName(id), gocron.WithIdentifier(gid)}
		switch j.Concurrency {
		case model.ConcSkip:
			opts = append(opts, gocron.WithSingletonMode(gocron.LimitModeReschedule))
		case model.ConcQueue:
			opts = append(opts, gocron.WithSingletonMode(gocron.LimitModeWait))
		}

		jobID := id
		task := gocron.NewTask(func() { d.fire(jobID) })

		if _, seen := current[id]; seen {
			if _, err := d.sched.Update(gid, def, task, opts...); err != nil {
				d.log.Error("cannot update job", "job", id, "error", err)
			}
			continue
		}
		if _, err := d.sched.NewJob(def, task, opts...); err != nil {
			d.log.Error("cannot add job", "job", id, "error", err)
			continue
		}
		d.mu.Lock()
		d.ids[id] = gid
		d.mu.Unlock()
	}
}

// stableID derives gocron's identifier from the job id, so history and identity survive
// a restart (docs/research/2026-09-02-gocron-scheduling-engine.md "Implications").
func stableID(jobID string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("herdr-cron:"+jobID))
}

func definition(j *model.Resolved) (gocron.JobDefinition, error) {
	switch j.Schedule.Type {
	case "cron":
		// withSeconds is always true; the parser still accepts 5-field expressions.
		return gocron.CronJob(j.Schedule.Expression, true), nil
	case "every":
		return gocron.DurationJob(time.Duration(j.Schedule.EverySec) * time.Second), nil
	case "at":
		t, ok := j.Schedule.Instant()
		if !ok {
			return nil, fmt.Errorf("invalid one-time schedule %q", j.Schedule.At)
		}
		return gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(t)), nil
	default:
		return nil, fmt.Errorf("unknown schedule type %q", j.Schedule.Type)
	}
}

// fire is the scheduler's entry point. gocron fires at the base occurrence; the per-job
// jitter is applied here, because a cron expression cannot carry a sub-minute offset
// (docs/spec/03-job-model.md §2.1).
func (d *Daemon) fire(jobID string) {
	j := d.job(jobID)
	if j == nil {
		return
	}
	ctx := d.baseContext()
	// The Occurrence, not the clock that happened to trip. Recording the clock made a fire
	// that arrived early look like a miss, so the next reconciliation pass replayed a job
	// that had already run (issue #12).
	scheduled := time.Now().Truncate(time.Second)
	if sch, err := schedule.FromResolved(j.Schedule); err == nil {
		if occ, ok := sch.Occurrence(scheduled); ok {
			scheduled = occ
		}
	}
	if j.Schedule.JitterSec > 0 {
		t := time.NewTimer(time.Duration(j.Schedule.JitterSec) * time.Second)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return // shutting down; the occurrence is simply not run
		}
	}
	d.execute(ctx, j, runner.Options{
		Trigger:     model.TriggerScheduler,
		ScheduledAt: scheduled,
	})
}

func (d *Daemon) baseContext() context.Context {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ctx == nil {
		return context.Background()
	}
	return d.ctx
}

// execute runs one job, tracking its cancel func so `job cancel` and shutdown can reach it.
func (d *Daemon) execute(parent context.Context, j *model.Resolved, opt runner.Options) *model.Run {
	ctx, cancel := context.WithCancelCause(parent)
	d.mu.Lock()
	if prev, ok := d.cancels[j.ID]; ok && j.Concurrency == model.ConcCancelPrevious {
		prev(runner.ErrSuperseded)
	}
	d.cancels[j.ID] = cancel
	d.mu.Unlock()

	defer func() {
		cancel(runner.ErrShutdown)
		d.mu.Lock()
		delete(d.cancels, j.ID)
		d.mu.Unlock()
	}()

	run, err := runner.RunOnce(ctx, d.store, j, opt)
	if err != nil {
		d.log.Error("run failed to record", "job", j.ID, "error", err)
		return nil
	}
	d.log.Info("run finished", "job", j.ID, "run", run.RunID, "status", string(run.Status))
	d.autoDisable(j, run)
	d.notify(j, run)
	return run
}

// autoDisable is the circuit breaker: an unattended job that keeps failing stops itself
// (docs/spec/03-job-model.md §4.5).
func (d *Daemon) autoDisable(j *model.Resolved, run *model.Run) {
	if j.Limits.MaxConsecutiveFailures <= 0 || run.Status == model.StatusSkipped {
		return
	}
	state, err := d.store.LoadState()
	if err != nil {
		return
	}
	js := state.Jobs[j.ID]
	if js == nil || js.ConsecutiveFailures < j.Limits.MaxConsecutiveFailures {
		return
	}
	if err := d.store.SetEnabled(j.ID, false, j.Enabled, "auto_failures"); err != nil {
		d.log.Error("cannot auto-disable", "job", j.ID, "error", err)
		return
	}
	d.log.Warn("auto-disabled after consecutive failures",
		"job", j.ID, "failures", js.ConsecutiveFailures)
	d.rebuild()
	d.notifyEvent(j, "auto_disabled",
		fmt.Sprintf("%s disabled after %d consecutive failures", j.Name, js.ConsecutiveFailures))
}

// ------------------------------------------------------------------ catch-up

// catchUp applies each job's missed-run policy. gocron discards every missed tick by
// design, so this is herdr-cron's own code (docs/spec/03-job-model.md §4.1).
func (d *Daemon) catchUp(ctx context.Context) {
	state, err := d.store.LoadState()
	if err != nil {
		d.log.Error("cannot read state.json", "error", err)
		return
	}
	now := time.Now()

	for _, j := range d.enabledJobs() {
		if j.Schedule.OneShot() {
			continue
		}
		if j.Schedule.Catchup == model.CatchupOff {
			continue
		}
		js := state.Jobs[j.ID]
		if js == nil || js.LastScheduledAt == nil {
			continue // never ran; there is nothing to catch up on
		}
		window := time.Duration(j.Schedule.CatchupWindowSec) * time.Second
		from := *js.LastScheduledAt
		if cut := now.Add(-window); from.Before(cut) {
			from = cut
		}
		missed := d.occurrences(j, from, now)
		if len(missed) == 0 {
			continue
		}
		if j.Schedule.Catchup == model.CatchupLatest {
			missed = missed[len(missed)-1:]
		} else if len(missed) > catchupCap {
			d.log.Warn("catch-up capped", "job", j.ID, "missed", len(missed), "cap", catchupCap)
			missed = missed[len(missed)-catchupCap:]
		}
		for _, at := range missed {
			if ctx.Err() != nil {
				return
			}
			d.log.Info("catch-up run", "job", j.ID, "scheduledAt", at.Format(time.RFC3339))
			// Catch-up runs are serialised, never parallel.
			d.execute(ctx, j, runner.Options{Trigger: model.TriggerCatchup, ScheduledAt: at})
		}
	}
}

// reconcileOneShots settles one-time Occurrences that are already in the past, and is the only
// place that can: gocron refuses to register a OneTimeJob whose instant has gone, and the
// recurring replay path has no pattern to enumerate for a schedule with exactly one
// Occurrence. Without it a one-shot missed by an hour of downtime left an error line on every
// reload and no Run record at all — the one gap this product exists to explain
// (docs/spec/03-job-model.md §4.1, ADR-0006).
//
// It is serialised because the start pass, the sleep detector and the reload watcher all call
// it: two concurrent passes would either run the same Occurrence twice or lose each other's
// watermark.
func (d *Daemon) reconcileOneShots(ctx context.Context) {
	d.oneShotMu.Lock()
	defer d.oneShotMu.Unlock()

	for _, j := range d.enabledJobs() {
		if ctx.Err() != nil {
			return
		}
		if !j.Schedule.OneShot() {
			continue
		}
		at, ok := j.Schedule.Instant()
		if !ok {
			continue
		}
		now := time.Now()
		if at.After(now) {
			continue
		}

		// Read for each job because executing or recording the previous one replaced
		// state.json, and this decision must observe the newest watermarks.
		state, err := d.store.LoadState()
		if err != nil {
			d.log.Error("cannot read state.json", "error", err)
			return
		}
		if store.OneShotCompleted(j, state.Jobs[j.ID]) {
			continue
		}

		reason := ""
		switch {
		case j.Schedule.Catchup == model.CatchupOff:
			reason = model.ReasonCatchupOff
		case now.Sub(at) > time.Duration(j.Schedule.CatchupWindowSec)*time.Second:
			reason = model.ReasonMissedWindow
		}
		if reason != "" {
			if _, err := runner.RecordSkip(d.store, j, at, model.TriggerCatchup, reason); err != nil {
				d.log.Error("cannot record skipped one-time job",
					"job", j.ID, "scheduledAt", at.Format(time.RFC3339),
					"reason", reason, "error", err)
				continue
			}
			d.log.Warn("one-time job skipped",
				"job", j.ID, "scheduledAt", at.Format(time.RFC3339), "reason", reason)
			continue
		}

		// latest and all are identical when the schedule has only one Occurrence.
		d.execute(ctx, j, runner.Options{
			Trigger:     model.TriggerCatchup,
			ScheduledAt: at,
		})
	}
}

func (d *Daemon) occurrences(j *model.Resolved, from, to time.Time) []time.Time {
	if j.Schedule.OneShot() {
		return nil // a one-time job has nothing to replay
	}
	sch, err := schedule.FromResolved(j.Schedule)
	if err != nil {
		return nil
	}
	var out []time.Time
	cur := from
	for len(out) <= catchupCap*2 {
		next, ok := sch.Next(cur)
		if !ok || !next.Before(to) {
			break
		}
		out = append(out, next)
		cur = next
	}
	return out
}

// ------------------------------------------------------------------- loops

func (d *Daemon) heartbeatLoop(ctx context.Context) {
	d.writeHeartbeat()
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.writeHeartbeat()
		}
	}
}

func (d *Daemon) writeHeartbeat() {
	d.mu.Lock()
	count := 0
	if d.loaded != nil {
		count = len(d.loaded.Jobs)
	}
	cerr := d.configErr
	d.mu.Unlock()

	hb := Heartbeat{
		PID: os.Getpid(), StartedAt: d.started, HeartbeatAt: time.Now(),
		Version: d.version, Driver: d.driver, ConfigPath: d.roots.JobsFile(),
		JobCount: count, ConfigError: cerr,
	}
	b, err := json.MarshalIndent(hb, "", "  ")
	if err != nil {
		return
	}
	tmp := filepath.Join(d.roots.TmpDir(), "daemon.json.tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, d.roots.DaemonFile())
}

// watchLoop is the fast path; the poll below it is the guarantee, because fsnotify is not
// reliable on every filesystem (docs/spec/04-storage.md §3.1).
func (d *Daemon) watchLoop(ctx context.Context) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		d.log.Warn("no file watcher; falling back to polling", "error", err)
	} else {
		defer func() { _ = w.Close() }()
		// Watch the directory, not the file: an atomic rename replaces the inode and a
		// file watch silently stops working after the first write.
		_ = w.Add(filepath.Dir(d.roots.JobsFile()))
		_ = w.Add(d.roots.State)
	}

	var timer *time.Timer
	fire := make(chan struct{}, 1)
	poll := time.NewTicker(pollInterval)
	defer poll.Stop()

	var lastMod time.Time
	var lastSize int64

	var events chan fsnotify.Event
	if w != nil {
		events = w.Events
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			base := filepath.Base(ev.Name)
			if base != filepath.Base(d.roots.JobsFile()) && base != "overrides.json" {
				continue
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(debounce, func() {
				select {
				case fire <- struct{}{}:
				default:
				}
			})
		case <-poll.C:
			st, err := os.Stat(d.roots.JobsFile())
			if err != nil {
				continue
			}
			if st.ModTime().Equal(lastMod) && st.Size() == lastSize {
				continue
			}
			lastMod, lastSize = st.ModTime(), st.Size()
			select {
			case fire <- struct{}{}:
			default:
			}
		case <-fire:
			d.log.Info("reloading jobs.yaml")
			d.reload()
			d.rebuild()
			// A one-time agent run can take long enough that doing this inline would
			// stop jobs.yaml changes from being observed until the run finishes.
			go d.reconcileOneShots(ctx)
		}
	}
}

// clockLoop detects sleep: Go's monotonic clock does not advance across suspend, so a
// wall-clock jump is the only signal (docs/spec/03-job-model.md §4.2).
func (d *Daemon) clockLoop(ctx context.Context) {
	t := time.NewTicker(clockTick)
	defer t.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if now.Sub(last) > sleepThreshold {
				d.log.Info("wall clock jumped; reconciling",
					"gap", now.Sub(last).Round(time.Second).String())
				d.catchUp(ctx)
				d.reconcileOneShots(ctx)
			}
			last = now
		}
	}
}

// ------------------------------------------------------------- trigger files

// Trigger is one request from a client (docs/spec/04-storage.md §8).
type Trigger struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"createdAt"`
	Action      string    `json:"action"`
	JobID       string    `json:"jobId"`
	RequestedBy string    `json:"requestedBy"`
	Wait        bool      `json:"wait"`
}

// TriggerResult is written next to a claimed trigger so a waiting client can find the run.
type TriggerResult struct {
	ID     string `json:"id"`
	RunID  string `json:"runId,omitempty"`
	JobID  string `json:"jobId,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func (d *Daemon) triggerLoop(ctx context.Context) {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.drainTriggers(ctx)
		}
	}
}

func (d *Daemon) drainTriggers(ctx context.Context) {
	entries, err := os.ReadDir(d.roots.TriggersDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(d.roots.TriggersDir(), e.Name())
		claimed := strings.TrimSuffix(path, ".json") + ".claimed"
		// The rename is the claim; it makes double-processing impossible without a lock.
		if err := os.Rename(path, claimed); err != nil {
			continue
		}
		b, err := os.ReadFile(claimed)
		if err != nil {
			_ = os.Remove(claimed)
			continue
		}
		var tr Trigger
		if err := json.Unmarshal(b, &tr); err != nil {
			_ = os.Remove(claimed)
			continue
		}
		go d.handleTrigger(ctx, tr, claimed)
	}
}

func (d *Daemon) handleTrigger(ctx context.Context, tr Trigger, claimed string) {
	defer func() { _ = os.Remove(claimed) }()
	res := TriggerResult{ID: tr.ID, JobID: tr.JobID, Status: "ok"}

	switch tr.Action {
	case "reload":
		d.reload()
		d.rebuild()
	case "cancel":
		d.mu.Lock()
		cancel, ok := d.cancels[tr.JobID]
		d.mu.Unlock()
		if !ok {
			res.Status = "error"
			res.Error = "no run in flight"
			break
		}
		cancel(runner.ErrCancelledByUser)
	case "run":
		j := d.job(tr.JobID)
		if j == nil {
			res.Status = "error"
			res.Error = "job_not_found"
			break
		}
		run := d.execute(ctx, j, runner.Options{Trigger: model.TriggerManual})
		if run == nil {
			res.Status = "error"
			res.Error = "the run could not be recorded"
			break
		}
		res.RunID = run.RunID
		res.Status = string(run.Status)
	default:
		res.Status = "error"
		res.Error = "unknown action " + tr.Action
	}

	b, err := json.Marshal(res)
	if err != nil {
		return
	}
	_ = os.WriteFile(strings.TrimSuffix(claimed, ".claimed")+".result", b, 0o644)
}

// ------------------------------------------------------------- housekeeping

func (d *Daemon) closeOrphanedRuns() {
	entries, err := os.ReadDir(filepath.Join(d.roots.State, "runs"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		jobID := strings.TrimSuffix(e.Name(), ".jsonl")
		runs, err := d.store.Runs(jobID)
		if err != nil {
			continue
		}
		for _, r := range runs {
			if r.Status != model.StatusRunning {
				continue
			}
			now := time.Now()
			r.Status = model.StatusFailure
			reason := "daemon_died"
			r.Reason = &reason
			r.FinishedAt = &now
			if err := d.store.AppendRun(r); err != nil {
				d.log.Error("cannot close orphaned run", "run", r.RunID, "error", err)
				continue
			}
			d.log.Warn("closed an orphaned run", "run", r.RunID)
		}
	}
}

// notify reports a finished run through the job's notifier, when the outcome is one of
// the events it subscribed to (docs/spec/03-job-model.md §4.6).
func (d *Daemon) notify(j *model.Resolved, run *model.Run) {
	d.notifyEvent(j, string(run.Status), fmt.Sprintf("%s: %s", j.Name, run.Status))
}
