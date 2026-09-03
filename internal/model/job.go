// Package model holds the job and run records specified in docs/spec/03-job-model.md.
package model

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Duration is a time.Duration that marshals as a Go duration string in YAML and as
// seconds in JSON. Bare integers are rejected: "timeout: 30" is ambiguous.
type Duration time.Duration

// UnmarshalYAML accepts a time.ParseDuration string, the "-1" sentinel meaning never, or the
// empty string meaning "inherit the default". Anything numeric is rejected rather than guessed
// at, because "timeout: 30" reads as seconds to one author and minutes to the next
// (docs/spec/03-job-model.md §1.2).
func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	if s == "" {
		*d = 0
		return nil
	}
	// "-1" is the documented "never" sentinel for timeout; every other bare number is
	// ambiguous and is rejected rather than guessed at (docs/spec/03-job-model.md §1.2).
	if s == "-1" {
		*d = Duration(-1)
		return nil
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return fmt.Errorf("bare number %s is ambiguous; write a duration such as %ss or %sm", s, s, s)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// Std is the stdlib duration for arithmetic. The never sentinel arrives here as -1ns, so a
// caller that would otherwise treat it as an immediate deadline MUST test for it first.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Seconds is the JSON encoding of a duration (docs/spec/03-job-model.md §1.3): whole seconds,
// truncated, with the never sentinel preserved as -1 rather than collapsed to 0.
func (d Duration) Seconds() int64 {
	if d < 0 {
		return -1
	}
	return int64(time.Duration(d) / time.Second)
}

// Kind is the job kind. v1 supports exactly shell and agent.
type Kind string

const (
	// KindShell runs a command as a direct child process of the scheduler, in its own
	// process group, so it still works when Herdr is absent or its server is down
	// (docs/spec/03-job-model.md §3.1). Its only outcomes are success and failure by exit
	// code, plus timeout and cancellation.
	KindShell Kind = "shell"
	// KindAgent prompts an agent in a Herdr pane (docs/spec/03-job-model.md §3.2). It adds
	// two outcomes a shell job cannot produce: no_op, when the transcript ends in the
	// job's no_op_marker, and blocked, when the agent is parked on an approval dialog.
	KindAgent Kind = "agent"
)

// Catchup policies (docs/spec/03-job-model.md §4.1).
type Catchup string

const (
	// CatchupOff discards every missed occurrence; the next one is computed from now.
	CatchupOff Catchup = "off"
	// CatchupLatest is the default: exactly one run for the most recently missed
	// occurrence, older ones discarded, and only when that occurrence still falls inside
	// catchup_window (default 168h).
	CatchupLatest Catchup = "latest"
	// CatchupAll replays every missed occurrence inside the window, in chronological order
	// and never in parallel, capped at 100 per job per pass; the overflow is recorded once
	// as a skipped run with reason catchup_capped.
	CatchupAll Catchup = "all"
)

// Concurrency policies (§4.3).
type Concurrency string

const (
	// ConcSkip is the default: the new occurrence is recorded as a skipped run with reason
	// overlap and does not execute. Recording it rather than dropping it is what makes
	// "why did this not run at 03:00" answerable.
	ConcSkip Concurrency = "skip"
	// ConcQueue defers the new occurrence until the running one finishes, bounded to one
	// waiting run per job; a second waiter displaces the first with reason superseded.
	ConcQueue Concurrency = "queue"
	// ConcCancelPrevious kills the running one (cancelled, reason superseded) and starts
	// the new one.
	ConcCancelPrevious Concurrency = "cancel_previous"
	// ConcAllow lets both run. Nothing serialises them, including for agent jobs sharing a
	// working tree.
	ConcAllow Concurrency = "allow"
)

// File is the parsed jobs.yaml.
type File struct {
	Version  int       `yaml:"version"`
	Defaults *Defaults `yaml:"defaults"`
	Jobs     []*Job    `yaml:"jobs"`
}

// Defaults are applied to every job before validation.
type Defaults struct {
	Timezone      string      `yaml:"timezone"`
	Timeout       *Duration   `yaml:"timeout"`
	Concurrency   Concurrency `yaml:"concurrency"`
	Jitter        string      `yaml:"jitter"`
	Catchup       Catchup     `yaml:"catchup"`
	CatchupWindow *Duration   `yaml:"catchup_window"`
	Retry         *Retry      `yaml:"retry"`
	Limits        *Limits     `yaml:"limits"`
	Notify        *Notify     `yaml:"notify"`
	Enabled       *bool       `yaml:"enabled"`
}

// Job is one authored job definition.
type Job struct {
	ID          string            `yaml:"id"`
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Enabled     *bool             `yaml:"enabled"`
	Tags        []string          `yaml:"tags"`
	Schedule    Schedule          `yaml:"schedule"`
	Kind        Kind              `yaml:"kind"`
	Shell       *ShellSpec        `yaml:"shell"`
	Agent       *AgentSpec        `yaml:"agent"`
	Cwd         string            `yaml:"cwd"`
	Env         map[string]string `yaml:"env"`
	Timeout     *Duration         `yaml:"timeout"`
	Concurrency Concurrency       `yaml:"concurrency"`
	Retry       *Retry            `yaml:"retry"`
	Limits      *Limits           `yaml:"limits"`
	Notify      *Notify           `yaml:"notify"`
}

// Schedule carries exactly one of Cron, Every, or At.
type Schedule struct {
	Cron          string    `yaml:"cron"`
	Every         *Duration `yaml:"every"`
	At            string    `yaml:"at"`
	Timezone      string    `yaml:"timezone"`
	StartAt       string    `yaml:"start_at"`
	StopAt        string    `yaml:"stop_at"`
	Catchup       Catchup   `yaml:"catchup"`
	CatchupWindow *Duration `yaml:"catchup_window"`
	Jitter        string    `yaml:"jitter"`
}

// ShellSpec is the kind: shell payload (docs/spec/03-job-model.md §3.1). Command is REQUIRED
// and non-empty. Shell selects the interpreter: "auto" is /bin/sh -c on Unix and
// powershell -NoProfile -Command on Windows, "none" splits the command with shell-like quoting
// and execs it directly, and an explicit path is used as "<path> -c <command>".
type ShellSpec struct {
	Command string `yaml:"command"`
	Shell   string `yaml:"shell"`
}

// AgentSpec is the kind: agent payload (docs/spec/03-job-model.md §3.2). Prompt is REQUIRED
// and is prefixed with the non-optional scheduler preamble (§3.3), whose absence is the
// documented cause of an unattended agent stalling on a question. Capture is "transcript" or
// "none"; NoOpMarker, when the final assistant text equals it exactly, turns success into
// no_op; Session is a Herdr session name or "current"; Worktree is "false" or a branch to
// create/reuse; WaitTimeout defaults to the job's timeout.
type AgentSpec struct {
	AgentKind   string    `yaml:"agent_kind"`
	Prompt      string    `yaml:"prompt"`
	Capture     string    `yaml:"capture"`
	NoOpMarker  string    `yaml:"no_op_marker"`
	Session     string    `yaml:"session"`
	Worktree    string    `yaml:"worktree"`
	WaitTimeout *Duration `yaml:"wait_timeout"`
}

// Retry is the retry policy. MaxAttempts counts total attempts and defaults to 1 — no retry —
// because one attempt may be a paid LLM invocation. Backoff is "exponential"
// (Initial * 2^(attempt-1) with ±10% deterministic jitter, clamped to MaxInterval, default
// 30m) or "fixed". Terminal outcomes that no retry can change — blocked, no_op, skipped,
// cancelled, cwd_missing, limit_exceeded — are never retried (docs/spec/03-job-model.md §4.4).
type Retry struct {
	MaxAttempts *int      `yaml:"max_attempts"`
	Backoff     string    `yaml:"backoff"`
	Initial     *Duration `yaml:"initial"`
	MaxInterval *Duration `yaml:"max_interval"`
}

// Limits are the spend guardrails (docs/spec/03-job-model.md §4.5). MaxRunsPerDay defaults to
// 24 for kind: agent and 0 (unlimited) for kind: shell, because a shell run is nearly free and
// an agent run is not; exceeding it records a skipped run with reason limit_exceeded.
// MaxConsecutiveFailures defaults to 3 for both kinds and counts failure, timeout and blocked;
// reaching it writes an enabled override with reason auto_failures, which job resume clears.
// 0 means unlimited / never auto-disable in either field.
type Limits struct {
	MaxRunsPerDay          *int `yaml:"max_runs_per_day"`
	MaxConsecutiveFailures *int `yaml:"max_consecutive_failures"`
}

// Notify selects which outcomes fire the notifier. On defaults to
// [failure, blocked, auto_disabled] and may name any of success, no_op, failure, timeout,
// blocked, auto_disabled, catchup; Command defaults to the built-in Herdr notifier. Delivery
// is best effort and never changes a run's outcome — a headless host has no foreground client
// to show a notification to, and that is the normal case for a scheduler
// (docs/spec/03-job-model.md §4.6).
type Notify struct {
	On      []string `yaml:"on"`
	Command []string `yaml:"command"`
}

// Resolved is the canonical record emitted by job get / job list, with defaults applied,
// paths expanded and durations normalised (docs/spec/03-job-model.md §1.3).
type Resolved struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	Enabled       bool              `json:"enabled"`
	EnabledSource string            `json:"enabledSource"`
	Tags          []string          `json:"tags"`
	Schedule      ResolvedSchedule  `json:"schedule"`
	Kind          Kind              `json:"kind"`
	Payload       any               `json:"payload"`
	Cwd           string            `json:"cwd"`
	Env           map[string]string `json:"env,omitempty"`
	TimeoutSec    int64             `json:"timeoutSec"`
	Concurrency   Concurrency       `json:"concurrency"`
	Retry         ResolvedRetry     `json:"retry"`
	Limits        ResolvedLimits    `json:"limits"`
	Notify        ResolvedNotify    `json:"notify"`
}

// ResolvedSchedule is a schedule with exactly one of Expression, EverySec or At populated, as
// named by Type. Timezone is a resolved IANA name (never "local"), and JitterSec is the job's
// own deterministic offset, already computed — nextRunAt elsewhere always reports the jittered
// instant, because that is when the job actually runs (docs/spec/03-job-model.md §2.1).
type ResolvedSchedule struct {
	Type             string  `json:"type"` // cron | every | at
	Expression       string  `json:"expression,omitempty"`
	EverySec         int64   `json:"everySec,omitempty"`
	At               string  `json:"at,omitempty"`
	Timezone         string  `json:"timezone"`
	Catchup          Catchup `json:"catchup"`
	CatchupWindowSec int64   `json:"catchupWindowSec"`
	JitterSec        int64   `json:"jitterSec"`
}

// ScheduleAt is the Type value of a one-time schedule: one absolute instant, one Occurrence,
// and then nothing. It is spelled out here because three packages branch on it.
const ScheduleAt = "at"

// OneShot reports whether this schedule has exactly one Occurrence in its whole life.
func (s ResolvedSchedule) OneShot() bool { return s.Type == ScheduleAt }

// Instant is the single Occurrence of a one-time schedule, and the only place At is parsed
// once config.Load has accepted it. A false result means the schedule is not one-time; it
// never means the instant was unreadable, because an unparseable At is a load error.
func (s ResolvedSchedule) Instant() (time.Time, bool) {
	if !s.OneShot() {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s.At)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// ResolvedRetry is Retry with the defaults substituted and every interval in seconds, so a
// consumer never has to know that an absent max_attempts means 1.
type ResolvedRetry struct {
	MaxAttempts    int    `json:"maxAttempts"`
	Backoff        string `json:"backoff"`
	InitialSec     int64  `json:"initialSec"`
	MaxIntervalSec int64  `json:"maxIntervalSec"`
}

// ResolvedLimits is Limits with the per-kind defaults substituted, so a 0 here is the
// authored "unlimited" / "never auto-disable" and not an unset field.
type ResolvedLimits struct {
	MaxRunsPerDay          int `json:"maxRunsPerDay"`
	MaxConsecutiveFailures int `json:"maxConsecutiveFailures"`
}

// ResolvedNotify is Notify with the default outcome list and notifier command substituted, so
// the record shows what will actually be run on a failure.
type ResolvedNotify struct {
	On      []string `json:"on"`
	Command []string `json:"command,omitempty"`
}

// ShellPayload is the JSON form of ShellSpec, carried in Resolved.Payload when Kind is
// KindShell.
type ShellPayload struct {
	Command string `json:"command"`
	Shell   string `json:"shell"`
}

// AgentPayload is the JSON form of AgentSpec, carried in Resolved.Payload when Kind is
// KindAgent. Prompt here is the authored text; the scheduler preamble is prepended at run
// time, not stored. WaitTimeoutSec has the job's timeout substituted when unset.
type AgentPayload struct {
	AgentKind      string `json:"agentKind"`
	Prompt         string `json:"prompt"`
	Capture        string `json:"capture"`
	NoOpMarker     string `json:"noOpMarker,omitempty"`
	Session        string `json:"session"`
	Worktree       string `json:"worktree,omitempty"`
	WaitTimeoutSec int64  `json:"waitTimeoutSec"`
}

// Status is a run's outcome (docs/spec/03-job-model.md §6).
type Status string

// The run statuses of docs/spec/03-job-model.md §6. StatusRunning is the only non-terminal
// one; every other value ends the run and replaces the running record. Terminal does not mean
// retryable: only failure and timeout are ever retried (§4.4).
const (
	// StatusRunning is written when a run starts and replaced by a terminal record when it
	// finishes. A running record left with no terminal partner and no live process is the
	// honest trace of a killed scheduler, and is closed out on the next start as failure /
	// daemon_died.
	StatusRunning Status = "running"
	// StatusSuccess is a shell exit code of 0, or an agent run that completed.
	StatusSuccess Status = "success"
	// StatusNoOp is a completed agent run whose output equalled the job's no_op_marker:
	// ran, and correctly did nothing. It exists so a week of heartbeat history is not 300
	// indistinguishable green rows. Never retried.
	StatusNoOp Status = "no_op"
	// StatusFailure is a non-zero exit, or an agent run that could not complete. Retryable,
	// and it increments the consecutive-failure count that drives auto-disable.
	StatusFailure Status = "failure"
	// StatusTimeout is a run killed at its timeout, with reason job_timeout, wait_timeout
	// or agent_prompt_stalled. Retryable, and it counts toward auto-disable.
	StatusTimeout Status = "timeout"
	// StatusBlocked is an agent sitting on an approval or question UI. Terminal, NEVER
	// retried — nobody is watching, so it will not resolve itself — always notified, and it
	// counts toward auto-disable because an agent that cannot start is not a transient
	// fault.
	StatusBlocked Status = "blocked"
	// StatusSkipped never executed; reason is overlap, limit_exceeded, disabled,
	// catchup_capped, superseded, missed_window or catchup_off. Recorded rather than
	// dropped so the gap in the history has an explanation, and excluded from the
	// runs-per-day count. Never retried.
	StatusSkipped Status = "skipped"
	// StatusCancelled was killed by cancel_previous, by shutdown, or by job cancel; reason
	// is superseded, shutdown or user. Never retried — the cancellation was the intent.
	StatusCancelled Status = "cancelled"
)

// Reasons a scheduler refuses a one-time Occurrence outright. They are constants, unlike the
// reasons discovered mid-run, because the daemon writes them and the tests assert them: a
// one-shot that vanished with nothing written is the failure these two values exist to make
// impossible (docs/spec/03-job-model.md §4.1).
const (
	// ReasonMissedWindow is a one-time Occurrence that passed while no scheduler was
	// running and was already older than the job's catchup_window when one started.
	ReasonMissedWindow = "missed_window"
	// ReasonCatchupOff is a one-time Occurrence that passed while no scheduler was running
	// and whose job set catchup: off. The refusal is the author's own instruction; it is
	// recorded anyway, because "nothing happened and nobody said why" is the complaint this
	// product exists to answer.
	ReasonCatchupOff = "catchup_off"
)

// Terminal reports whether a status ends a run.
func (s Status) Terminal() bool { return s != StatusRunning }

// Trigger is a run's provenance.
type Trigger string

const (
	// TriggerScheduler is an occurrence that came due on schedule. It and TriggerCatchup
	// are the triggers jitter applies to.
	TriggerScheduler Trigger = "scheduler"
	// TriggerManual is job run, run-once, or the TUI. Jitter is NEVER applied to it, and
	// its run id uses the invocation instant with a -m suffix, having no scheduled one.
	TriggerManual Trigger = "manual"
	// TriggerCatchup is a missed occurrence replayed by the reconciliation pass, with the
	// run id derived from the missed instant — which is what makes replaying the same pass
	// twice a no-op (docs/spec/03-job-model.md §4.1).
	TriggerCatchup Trigger = "catchup"
	// TriggerRetry is a further attempt at a failed run, reusing the base run id with a
	// -r<attempt> suffix so the attempts of one occurrence stay grouped.
	TriggerRetry Trigger = "retry"
	// TriggerStartup is a run owed to the scheduler starting rather than to an occurrence
	// falling due.
	TriggerStartup Trigger = "startup"
)

// Run is one execution record, appended to runs/<jobId>.jsonl.
type Run struct {
	RunID         string     `json:"runId"`
	JobID         string     `json:"jobId"`
	Trigger       Trigger    `json:"trigger"`
	Attempt       int        `json:"attempt"`
	ScheduledAt   *time.Time `json:"scheduledAt"`
	StartedAt     *time.Time `json:"startedAt"`
	FinishedAt    *time.Time `json:"finishedAt"`
	DurationSec   *float64   `json:"durationSec"`
	Status        Status     `json:"status"`
	ExitCode      *int       `json:"exitCode"`
	Reason        *string    `json:"reason"`
	LogPath       string     `json:"logPath,omitempty"`
	OutputExcerpt string     `json:"outputExcerpt,omitempty"`
	Host          string     `json:"host,omitempty"`
	Herdr         *RunHerdr  `json:"herdr,omitempty"`
}

// RunHerdr records where an agent run actually happened, so that a pane can be reattached
// afterwards or its disappearance explained (reason pane_lost, agent_vanished). Absent for
// kind: shell runs, which have no Herdr surface at all.
type RunHerdr struct {
	Session   string `json:"session,omitempty"`
	PaneID    string `json:"paneId,omitempty"`
	AgentName string `json:"agentName,omitempty"`
}

// RunIDFormat is the compact UTC layout used by run ids.
const RunIDFormat = "20060102T150405Z"

// NewRunID is deterministic in (jobID, scheduledAt), which is what makes a repeated
// catch-up pass idempotent (docs/spec/03-job-model.md §6).
func NewRunID(jobID string, scheduledAt time.Time) string {
	return jobID + "-" + scheduledAt.UTC().Format(RunIDFormat)
}

// MarshalLine renders a run as one JSONL line.
func (r *Run) MarshalLine() ([]byte, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
