package schedule

import (
	"testing"
	"time"
)

func cronSchedule(t *testing.T, expr string) *Schedule {
	t.Helper()
	sch, err := Parse(Spec{Cron: expr, Location: time.UTC})
	if err != nil {
		t.Fatalf("parse %q: %v", expr, err)
	}
	return sch
}

// The wall clock at fire time is not the Occurrence. gocron has been observed tripping seconds
// early, and a watermark written before the Occurrence it just ran makes the next
// reconciliation pass replay a job that already ran (issue #12).
func TestOccurrenceSnapsToTheCronGrid(t *testing.T) {
	daily := cronSchedule(t, "0 20 * * *")
	base := time.Date(2026, 9, 2, 20, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"fired six seconds early", base.Add(-6 * time.Second), base},
		{"fired exactly on time", base, base},
		{"fired a little late", base.Add(3 * time.Second), base},
		{"fired at the edge of the skew", base.Add(-FireSkew), base},
	} {
		got, ok := daily.Occurrence(tc.now)
		if !ok {
			t.Errorf("%s: no occurrence for %s", tc.name, tc.now)
			continue
		}
		if !got.Equal(tc.want) {
			t.Errorf("%s: occurrence = %s, want %s", tc.name, got, tc.want)
		}
	}
}

// Beyond the skew there is no Occurrence this fire can be attributed to, and inventing one
// would move a run into a slot it does not belong to.
func TestOccurrenceRefusesAnUnexplainableClock(t *testing.T) {
	daily := cronSchedule(t, "0 20 * * *")
	base := time.Date(2026, 9, 2, 20, 0, 0, 0, time.UTC)

	if got, ok := daily.Occurrence(base.Add(-time.Minute)); ok {
		t.Errorf("a fire a minute early resolved to %s; it is not an occurrence of this schedule", got)
	}
	if got, ok := daily.Occurrence(base.Add(11 * time.Second)); ok {
		t.Errorf("a fire eleven seconds late resolved to %s, want no occurrence", got)
	}
}

// A grid finer than the skew must not snap forward: the run belongs to the Occurrence that has
// already come due, never to one still ahead.
func TestOccurrenceNeverSnapsForwardOnAFineGrid(t *testing.T) {
	every5s := cronSchedule(t, "*/5 * * * * *")
	now := time.Date(2026, 9, 2, 20, 0, 7, 0, time.UTC)

	got, ok := every5s.Occurrence(now)
	if !ok {
		t.Fatal("no occurrence for a schedule that fires every five seconds")
	}
	if want := time.Date(2026, 9, 2, 20, 0, 5, 0, time.UTC); !got.Equal(want) {
		t.Errorf("occurrence = %s, want %s", got, want)
	}
	if got.After(now) {
		t.Errorf("occurrence %s is in the future", got)
	}
}

func TestOccurrenceOfAOneTimeScheduleIsItsInstant(t *testing.T) {
	at := time.Date(2026, 12, 24, 18, 0, 0, 0, time.UTC)
	sch, err := Parse(Spec{At: at.Format(time.RFC3339), Location: time.UTC})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, ok := sch.Occurrence(at.Add(2 * time.Second))
	if !ok || !got.Equal(at) {
		t.Fatalf("occurrence = %s (%v), want %s", got, ok, at)
	}
}

// An interval schedule is measured from the last run, so it has no grid and the caller keeps
// the clock it already has.
func TestOccurrenceOfAnIntervalScheduleIsUnknowable(t *testing.T) {
	sch, err := Parse(Spec{Every: 30 * time.Minute, Location: time.UTC})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, ok := sch.Occurrence(time.Now()); ok {
		t.Errorf("occurrence = %s, want none for an interval schedule", got)
	}
}
