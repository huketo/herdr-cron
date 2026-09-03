// Package runner implements the run-once execution primitive of
// docs/spec/02-architecture.md §2 for kind: shell.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/huketo/herdr-cron/internal/model"
	"github.com/huketo/herdr-cron/internal/store"
)

// Options configure one run.
type Options struct {
	Trigger     model.Trigger
	ScheduledAt time.Time // zero for a manual run
	Attempt     int
}

// excerptBytes is the tail of the output kept on the run record; the rest lives in the log.
const excerptBytes = 2048

// RunOnce executes exactly one run of a job and returns its terminal record.
// It never consults a daemon.
func RunOnce(ctx context.Context, st *store.Store, job *model.Resolved, opt Options) (*model.Run, error) {
	if opt.Attempt < 1 {
		opt.Attempt = 1
	}
	if opt.Trigger == "" {
		opt.Trigger = model.TriggerManual
	}
	scheduled := opt.ScheduledAt
	if scheduled.IsZero() {
		scheduled = time.Now()
	}

	runID := model.NewRunID(job.ID, scheduled)
	if opt.Trigger == model.TriggerManual {
		runID += "-m"
	}
	if opt.Attempt > 1 {
		runID += fmt.Sprintf("-r%d", opt.Attempt)
	}

	host, _ := os.Hostname()
	run := &model.Run{
		RunID:   runID,
		JobID:   job.ID,
		Trigger: opt.Trigger,
		Attempt: opt.Attempt,
		Status:  model.StatusRunning,
		Host:    host,
	}
	if !opt.ScheduledAt.IsZero() {
		run.ScheduledAt = new(opt.ScheduledAt)
	}

	// Overlap. concurrency: skip records the occurrence rather than dropping it, which is
	// what makes "why did this not run" answerable.
	unlock, ok, err := st.TryLockRun(job.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		if job.Concurrency == model.ConcSkip {
			return finish(st, run, model.StatusSkipped, new("overlap"), nil, "")
		}
		// queue / cancel_previous / allow are daemon-side policies; a bare run-once waits.
		unlock, ok, err = st.TryLockRun(job.ID)
		if err != nil || !ok {
			return finish(st, run, model.StatusSkipped, new("overlap"), nil, "")
		}
	}
	defer unlock()

	// Limits.
	state, err := st.LoadState()
	if err != nil {
		return nil, err
	}
	js := state.Job(job.ID)
	today := time.Now().Format("2006-01-02")
	if js.RunsToday == nil || js.RunsToday.Date != today {
		js.RunsToday = &store.RunsToday{Date: today}
	}
	if job.Limits.MaxRunsPerDay > 0 && js.RunsToday.Count >= job.Limits.MaxRunsPerDay {
		return finish(st, run, model.StatusSkipped, new("limit_exceeded"), nil, "")
	}

	if job.Cwd != "" {
		if fi, err := os.Stat(job.Cwd); err != nil || !fi.IsDir() {
			return finish(st, run, model.StatusFailure, new("cwd_missing"), nil, "")
		}
	}

	started := time.Now()
	run.StartedAt = &started

	logFile, logRel, err := st.LogWriter(job.ID, runID)
	if err != nil {
		return nil, err
	}
	run.LogPath = logRel

	// The watermark and the "running" record are written before anything executes, so a
	// crash leaves the truth on disk.
	js.LastScheduledAt = &scheduled
	js.LastRunID = runID
	js.RunsToday.Count++
	if err := st.SaveState(state); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	if err := st.AppendRun(run); err != nil {
		_ = logFile.Close()
		return nil, err
	}

	var (
		status   model.Status
		reason   *string
		exitCode *int
		excerpt  string
	)
	switch job.Kind {
	case model.KindAgent:
		out := executeAgent(withTimeout(ctx, job), job, runID, logFile)
		status, excerpt, run.Herdr = out.status, out.excerpt, out.herdr
		if out.reason != "" {
			reason = &out.reason
		}
	default:
		status, reason, exitCode, excerpt = execute(ctx, job, logFile)
	}
	_ = logFile.Close()

	return finish(st, run, status, reason, exitCode, excerpt)
}

// withTimeout applies the job's deadline. The shell path builds its own so it can tell a
// deadline apart from a cancellation; the agent path only needs the bound.
func withTimeout(ctx context.Context, job *model.Resolved) context.Context {
	if job.TimeoutSec <= 0 {
		return ctx
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(job.TimeoutSec)*time.Second)
	_ = cancel // the run ends with the process; cancellation is the caller's job
	return ctx
}

func execute(ctx context.Context, job *model.Resolved, logFile io.Writer) (model.Status, *string, *int, string) {
	payload, ok := job.Payload.(model.ShellPayload)
	if !ok {
		return model.StatusFailure, new("herdr_unexpected"), nil,
			fmt.Sprintf("kind %q has no shell payload", job.Kind)
	}

	if job.TimeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(job.TimeoutSec)*time.Second)
		defer cancel()
	}

	name, args := shellCommand(payload)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = job.Cwd
	cmd.Env = environ(job.Env)
	configureProcessGroup(cmd) // a timeout must kill the tree, not just the shell

	tail := &tailBuffer{limit: excerptBytes}
	cmd.Stdout = io.MultiWriter(logFile, tail)
	cmd.Stderr = cmd.Stdout

	err := cmd.Run()
	switch {
	case err == nil:
		return model.StatusSuccess, nil, new(0), tail.String()
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return model.StatusTimeout, new("job_timeout"), nil, tail.String()
	case errors.Is(ctx.Err(), context.Canceled):
		return model.StatusCancelled, new(cancelReason(ctx)), nil, tail.String()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return model.StatusFailure, nil, new(ee.ExitCode()), tail.String()
	}
	fmt.Fprintf(logFile, "\nherdr-cron: %v\n", err)
	return model.StatusFailure, new("herdr_unexpected"), nil, err.Error()
}

// Cancel causes, so a cancelled run records why rather than guessing
// (docs/spec/03-job-model.md §6).
var (
	ErrCancelledByUser = errors.New("user")
	ErrSuperseded      = errors.New("superseded")
	ErrShutdown        = errors.New("shutdown")
)

func cancelReason(ctx context.Context) string {
	switch cause := context.Cause(ctx); {
	case errors.Is(cause, ErrCancelledByUser):
		return "user"
	case errors.Is(cause, ErrSuperseded):
		return "superseded"
	default:
		return "shutdown"
	}
}

// shellCommand resolves the shell: auto, none, or an explicit interpreter.
func shellCommand(p model.ShellPayload) (string, []string) {
	switch p.Shell {
	case "", "auto":
		if runtime.GOOS == "windows" {
			return "powershell", []string{"-NoProfile", "-Command", p.Command}
		}
		return "/bin/sh", []string{"-c", p.Command}
	case "none":
		fields := strings.Fields(p.Command)
		if len(fields) == 0 {
			return "/bin/sh", []string{"-c", p.Command}
		}
		return fields[0], fields[1:]
	default:
		return p.Shell, []string{"-c", p.Command}
	}
}

func environ(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

// RecordSkip writes the one skipped Run record that explains an Occurrence which never
// executed, and claims that Occurrence with the catch-up watermark so a later reconciliation
// pass does not record it a second time.
//
// RunOnce records its own refusals — overlap, limit_exceeded — because it discovers them
// after it has decided to run. This is for the refusals decided before any run is attempted,
// which today means a one-time Occurrence the scheduler arrived too late for
// (docs/spec/03-job-model.md §4.1). Like every skipped record it leaves lastStatus alone:
// state.json reports the last outcome that executed, and the explanation for a gap lives in
// the run history, which is where a reader looks for it.
func RecordSkip(st *store.Store, job *model.Resolved, scheduledAt time.Time, trigger model.Trigger, reason string) (*model.Run, error) {
	host, _ := os.Hostname()
	run := &model.Run{
		RunID:       model.NewRunID(job.ID, scheduledAt),
		JobID:       job.ID,
		Trigger:     trigger,
		Attempt:     1,
		ScheduledAt: &scheduledAt,
		Status:      model.StatusRunning,
		Host:        host,
	}
	run, err := finish(st, run, model.StatusSkipped, &reason, nil, "")
	if err != nil {
		return run, err
	}

	state, err := st.LoadState()
	if err != nil {
		return run, err
	}
	js := state.Job(job.ID)
	if js.LastScheduledAt == nil || js.LastScheduledAt.Before(scheduledAt) {
		js.LastScheduledAt = &scheduledAt
	}
	return run, st.SaveState(state)
}

func finish(st *store.Store, run *model.Run, status model.Status, reason *string, exitCode *int, excerpt string) (*model.Run, error) {
	now := time.Now()
	run.Status = status
	run.Reason = reason
	run.ExitCode = exitCode
	run.OutputExcerpt = excerpt
	run.FinishedAt = &now
	if run.StartedAt != nil {
		run.DurationSec = new(now.Sub(*run.StartedAt).Seconds())
	}
	if err := st.AppendRun(run); err != nil {
		return run, err
	}

	state, err := st.LoadState()
	if err != nil {
		return run, err
	}
	js := state.Job(run.JobID)
	if status != model.StatusSkipped {
		js.LastStatus = status
		js.LastFinishedAt = &now
		js.LastRunID = run.RunID
		switch status {
		case model.StatusFailure, model.StatusTimeout, model.StatusBlocked:
			js.ConsecutiveFailures++
		case model.StatusSuccess, model.StatusNoOp:
			js.ConsecutiveFailures = 0
		}
	}
	return run, st.SaveState(state)
}

// tailBuffer keeps the last limit bytes written through it.
type tailBuffer struct {
	limit int
	buf   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = t.buf[len(t.buf)-t.limit:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return strings.TrimSpace(string(t.buf)) }
