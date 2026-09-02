//go:build linux

package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/huketo/herdr-cron/internal/model"
)

// A wrong OnCalendar is worse than no timer: the job fires, silently, at the wrong time.
// Everything that cannot be translated exactly must be refused
// (docs/spec/02-architecture.md §4.1).
func TestCronToCalendar(t *testing.T) {
	ok := []struct{ in, want string }{
		{"17 3 * * 1-5", "Mon..Fri *-*-* 03:17:00"},
		{"0 4 * * *", "*-*-* 04:00:00"},
		{"30 * * * *", "*-*-* *:30:00"},
		{"0 0 1 * *", "*-*-01 00:00:00"},
		{"0 9 * * 1", "Mon *-*-* 09:00:00"},
		{"0 9 * * 0", "Sun *-*-* 09:00:00"},
		{"0 9 * * 7", "Sun *-*-* 09:00:00"}, // 7 and 0 are both Sunday in cron
		{"0 9 * * 1,3,5", "Mon,Wed,Fri *-*-* 09:00:00"},
		{"5 0 * 3 *", "*-03-* 00:05:00"},
		{"0 * * * *", "*-*-* *:00:00"},
		{"@hourly", "*-*-* *:00:00"},
		{"@daily", "*-*-* 00:00:00"},
		{"@weekly", "Sun *-*-* 00:00:00"},
		{"@monthly", "*-*-01 00:00:00"},
		{"@yearly", "*-01-01 00:00:00"},
		{"0 17 3 * * 1-5", "Mon..Fri *-*-* 03:17:00"}, // 6 fields: seconds are dropped
	}
	for _, tc := range ok {
		got, err := cronToCalendar(tc.in)
		if err != nil {
			t.Errorf("%q: unexpected error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.in, got, tc.want)
		}
	}

	refuse := []struct{ in, because string }{
		{"0 4 1 * 1", "cron ORs day-of-month with day-of-week; systemd ANDs them"},
		{"*/15 * * * *", "step syntax"},
		{"0 9,17 * * *", "list syntax in the hour field"},
		{"@reboot", "not a calendar event"},
		{"0 4 * *", "too few fields"},
		{"0 9 * * 9", "day-of-week out of range"},
	}
	for _, tc := range refuse {
		if got, err := cronToCalendar(tc.in); err == nil {
			t.Errorf("%q was translated to %q but must be refused: %s", tc.in, got, tc.because)
		} else if !errors.Is(err, ErrUntranslatable) {
			t.Errorf("%q: error %v does not wrap ErrUntranslatable", tc.in, err)
		}
	}
}

// Persistent=true is the entire reason the os-scheduler driver exists: it is what gives a
// laptop that was powered off a single catch-up run, for free.
func TestTimerCarriesPersistentUnlessCatchupIsOff(t *testing.T) {
	base := &model.Resolved{ID: "j", Schedule: model.ResolvedSchedule{
		Type: "cron", Expression: "0 4 * * *", Catchup: model.CatchupLatest}}
	if body := jobTimer(base, "OnCalendar=*-*-* 04:00:00"); !strings.Contains(body, "Persistent=true") {
		t.Errorf("catchup: latest must set Persistent=true:\n%s", body)
	}

	off := *base
	off.Schedule.Catchup = model.CatchupOff
	if body := jobTimer(&off, "OnCalendar=*-*-* 04:00:00"); !strings.Contains(body, "Persistent=false") {
		t.Errorf("catchup: off must set Persistent=false:\n%s", body)
	}
}

// The unit must set the run provenance, or every OS-driven run is misfiled as manual.
func TestJobUnitSetsTheTriggerAndTheTimeout(t *testing.T) {
	j := &model.Resolved{ID: "audit", Cwd: "/srv/repo", TimeoutSec: 600,
		Schedule: model.ResolvedSchedule{Type: "cron", Expression: "0 4 * * *"}}
	body := jobUnit(j, Request{Binary: "/usr/bin/herdr-cron"})

	for _, want := range []string{
		"ExecStart=/usr/bin/herdr-cron run-once audit",
		"Environment=HERDR_CRON_TRIGGER=scheduler",
		"WorkingDirectory=/srv/repo",
		// One minute past the job's own timeout, so herdr-cron records `timeout` itself
		// instead of systemd killing the process first.
		"TimeoutStartSec=660",
		"Type=oneshot",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the unit is missing %q:\n%s", want, body)
		}
	}
}

// An interval job gets no OS catch-up, and the artefact must say so by construction:
// Persistent= does not apply to OnUnitActiveSec=.
func TestIntervalJobsUseOnUnitActiveSec(t *testing.T) {
	j := &model.Resolved{ID: "tick", Schedule: model.ResolvedSchedule{Type: "every", EverySec: 1800}}
	got, err := onCalendar(j, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "OnUnitActiveSec=1800s") {
		t.Errorf("interval translation = %q", got)
	}
}

// Marker fencing is what makes registration reversible without touching anything a human
// wrote.
func TestFenceIsRecognisedAndForeignFilesAreNot(t *testing.T) {
	body := fenced("audit", "[Timer]\nOnCalendar=daily\n")
	if !strings.Contains(body, "# herdr-cron:audit:begin") ||
		!strings.Contains(body, "# herdr-cron:audit:end") ||
		!strings.Contains(body, ":sha256=") {
		t.Fatalf("the fence is incomplete:\n%s", body)
	}

	dir := t.TempDir()
	owned := dir + "/owned.timer"
	foreign := dir + "/foreign.timer"
	if err := writeFenced(owned, "audit", body); err != nil {
		t.Fatal(err)
	}
	if err := writeFenced(foreign, "audit", "[Timer]\nOnCalendar=daily\n"); err != nil {
		t.Fatal(err)
	}
	// The second write had no fence, so a later write must refuse to clobber it.
	if err := writeFenced(foreign, "audit", body); err == nil {
		t.Error("writeFenced overwrote a file it does not own")
	}

	if e := entryState(owned, "audit", body); e.State != "ok" {
		t.Errorf("owned file state = %q, want ok", e.State)
	}
	if e := entryState(owned, "audit", body+"\n# changed\n"); e.State != "stale" {
		t.Errorf("changed file state = %q, want stale", e.State)
	}
	if e := entryState(foreign, "audit", body); e.State != "orphan" {
		t.Errorf("foreign file state = %q, want orphan", e.State)
	}
	if e := entryState(dir+"/absent.timer", "audit", body); e.State != "missing" {
		t.Errorf("absent file state = %q, want missing", e.State)
	}

	if e, _ := removeFenced(foreign, "audit"); e.State != "orphan" {
		t.Errorf("removeFenced deleted a foreign file (state %q)", e.State)
	}
	if e, _ := removeFenced(owned, "audit"); e.State != "missing" {
		t.Errorf("removeFenced left an owned file behind (state %q)", e.State)
	}
}
