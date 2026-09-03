package store

import (
	"time"

	"github.com/huketo/herdr-cron/internal/model"
)

// OneShotCompleted reports whether a one-time job's single Occurrence is spent — it ran, or a
// scheduler recorded why it did not — and is therefore the whole of the `completed` state a
// one-shot ever has. Nothing is written to jobs.yaml when a one-shot fires: the authored file
// stays the author's, and this projection over state.json answers "is this still going to
// happen?" for the CLI, the TUI and the daemon alike.
//
// The catch-up watermark is the evidence, and deliberately so. It is written *before* the run
// executes (docs/spec/03-job-model.md §4.2), so a crash mid-run still counts as spent: a
// one-shot that fires twice is worse than one that fires never. A manual `job run` also claims
// it, which is correct — the job was asked to happen once and it happened once.
func OneShotCompleted(j *model.Resolved, js *JobState) bool {
	if j == nil || js == nil || js.LastScheduledAt == nil {
		return false
	}
	at, ok := j.Schedule.Instant()
	if !ok {
		return false
	}
	// The watermark of a scheduler-triggered run is the wall clock truncated to the second,
	// so an At carrying a fraction of a second would otherwise never look claimed.
	return !js.LastScheduledAt.Before(at.Truncate(time.Second))
}
