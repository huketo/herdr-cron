package store

import (
	"testing"
	"time"

	"github.com/huketo/herdr-cron/internal/model"
)

func atJob(at string) *model.Resolved {
	return &model.Resolved{ID: "once", Schedule: model.ResolvedSchedule{
		Type: model.ScheduleAt, At: at, Timezone: "UTC",
	}}
}

func watermark(t time.Time) *JobState { return &JobState{LastScheduledAt: &t} }

func TestOneShotCompleted(t *testing.T) {
	at := time.Date(2026, 9, 3, 18, 0, 0, 0, time.UTC)
	iso := at.Format(time.RFC3339)

	for _, tc := range []struct {
		name string
		job  *model.Resolved
		js   *JobState
		want bool
	}{
		{"no state at all", atJob(iso), nil, false},
		{"state without a watermark", atJob(iso), &JobState{}, false},
		{"watermark before the Occurrence", atJob(iso), watermark(at.Add(-time.Second)), false},
		{"watermark on the Occurrence", atJob(iso), watermark(at), true},
		{"watermark after the Occurrence", atJob(iso), watermark(at.Add(time.Hour)), true},
		// The scheduler's watermark is the wall clock truncated to the second, so an
		// instant carrying a fraction would never look claimed without the truncation.
		{"fractional Occurrence claimed", atJob(at.Add(500 * time.Millisecond).Format(time.RFC3339Nano)), watermark(at), true},
		// Only a one-time schedule has a single Occurrence to spend; a recurring job is
		// never "completed", however far its watermark has advanced.
		{"recurring job", &model.Resolved{ID: "every", Schedule: model.ResolvedSchedule{
			Type: "cron", Expression: "0 9 * * *", Timezone: "UTC",
		}}, watermark(at.Add(time.Hour)), false},
	} {
		if got := OneShotCompleted(tc.job, tc.js); got != tc.want {
			t.Errorf("%s: OneShotCompleted = %v, want %v", tc.name, got, tc.want)
		}
	}
}
