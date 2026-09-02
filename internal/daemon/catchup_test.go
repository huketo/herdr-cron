package daemon

import (
	"testing"
	"time"

	"github.com/huketo/herdr-cron/internal/model"
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
