// Package schedule parses the three schedule forms of docs/spec/03-job-model.md §2 and
// answers "when does this fire next" without a running scheduler.
//
// Cron parsing goes through gocron's own NewDefaultCron, which is exported for exactly this
// purpose, so the CLI's verdict and the daemon's behaviour cannot diverge
// (docs/research/2026-09-02-gocron-scheduling-engine.md §10).
package schedule

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-co-op/gocron/v2"

	"github.com/huketo/herdr-cron/internal/model"
)

// Spec is a parsed-but-unvalidated schedule.
type Spec struct {
	Cron     string
	Every    time.Duration
	At       string
	Location *time.Location
}

// Schedule answers next-fire questions.
type Schedule struct {
	spec Spec
	cron gocron.Cron
	at   time.Time
}

// LoadLocation resolves "local", "UTC", or an IANA name.
func LoadLocation(name string) (*time.Location, error) {
	switch name {
	case "", "local", "Local":
		return time.Local, nil
	case "UTC":
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q", name)
	}
	return loc, nil
}

// Form names the schedule form an expression resolved to. The values are the ones
// model.ResolvedSchedule.Type carries, so a caller never translates between two vocabularies.
const (
	FormCron  = "cron"
	FormEvery = "every"
	FormAt    = "at"
)

// ParseExpr disambiguates one schedule expression by shape and is the only place that does
// it: the CLI's --schedule, the jobs.yaml writer, and validate all resolve the same text the
// same way (docs/spec/05-cli.md §3.1).
//
// A leading "+" is a relative one-shot ("+2h") and is normalised here into the absolute
// instant it means. Relative text is never stored: jobs.yaml is re-read on every reload, so a
// stored "+2h" would re-anchor to each reload and never arrive. The "+" prefix is also why
// this check runs before the duration form — time.ParseDuration accepts "+2h" happily, and
// "run this once in two hours" silently becoming "run this every two hours" is the worst
// available outcome.
func ParseExpr(expr string, now time.Time, loc *time.Location) (Spec, string, error) {
	if loc == nil {
		loc = time.Local
	}
	s := Spec{Location: loc}
	switch {
	case strings.HasPrefix(expr, "@"):
		s.Cron = expr
		return s, FormCron, nil
	case strings.HasPrefix(expr, "+"):
		d, err := time.ParseDuration(expr)
		if err != nil {
			return s, "", fmt.Errorf("invalid relative instant %q: expected a duration after the +, e.g. +2h", expr)
		}
		if d <= 0 {
			return s, "", fmt.Errorf("invalid relative instant %q: must be a positive duration", expr)
		}
		s.At = now.In(loc).Add(d).Truncate(time.Second).Format(time.RFC3339)
		return s, FormAt, nil
	case isInstant(expr):
		s.At = expr
		return s, FormAt, nil
	case !strings.ContainsAny(expr, " \t"):
		d, err := time.ParseDuration(expr)
		if err != nil {
			return s, "", fmt.Errorf("%q is neither a duration nor a cron expression", expr)
		}
		s.Every = d
		return s, FormEvery, nil
	default:
		s.Cron = expr
		return s, FormCron, nil
	}
}

func isInstant(v string) bool {
	_, err := time.Parse(time.RFC3339, v)
	return err == nil
}

// Parse validates a spec.
func Parse(s Spec) (*Schedule, error) {
	if s.Location == nil {
		s.Location = time.Local
	}
	sch := &Schedule{spec: s}
	switch {
	case s.Cron != "":
		// withSeconds is always true: robfig's SecondOptional parser still accepts 5-field
		// expressions in that mode, so one code path serves both.
		c := gocron.NewDefaultCron(true)
		if err := c.IsValid(s.Cron, s.Location, time.Now()); err != nil {
			return nil, fmt.Errorf("invalid cron expression %q: %w", s.Cron, err)
		}
		sch.cron = c
	case s.Every > 0:
		if s.Every < time.Second {
			return nil, errors.New("every must be at least 1s")
		}
	case s.At != "":
		t, err := time.Parse(time.RFC3339, s.At)
		if err != nil {
			return nil, fmt.Errorf("invalid at %q: expected RFC 3339, e.g. 2026-12-24T18:00:00+09:00", s.At)
		}
		sch.at = t
	default:
		return nil, errors.New("exactly one of cron, every, or at is required")
	}
	return sch, nil
}

// Next returns the first occurrence strictly after from.
func (s *Schedule) Next(from time.Time) (time.Time, bool) {
	switch {
	case s.cron != nil:
		n := s.cron.Next(from.In(s.spec.Location))
		if n.IsZero() {
			return time.Time{}, false
		}
		return n, true
	case s.spec.Every > 0:
		return from.Add(s.spec.Every), true
	default:
		if s.at.After(from) {
			return s.at, true
		}
		return time.Time{}, false
	}
}

// NextN returns up to n occurrences after from, with jitter applied.
func (s *Schedule) NextN(from time.Time, n int, jitter time.Duration) []time.Time {
	out := make([]time.Time, 0, n)
	cur := from
	for len(out) < n {
		next, ok := s.Next(cur)
		if !ok {
			break
		}
		out = append(out, next.Add(jitter).In(s.spec.Location))
		cur = next
	}
	return out
}

// FireSkew bounds how far the wall clock at fire time may sit from the Occurrence the fire
// belongs to. gocron's timer has been observed tripping several seconds early on a machine
// whose clock steps — a VM syncing NTP, a laptop resuming — and a task can equally be handed
// over a little late.
const FireSkew = 10 * time.Second

// Occurrence names the scheduled instant that a fire at now belongs to, which is what the
// catch-up watermark and the run id must record. The wall clock is not it: a watermark written
// six seconds before the Occurrence it just ran leaves that Occurrence looking missed, and the
// next reconciliation pass replays a job that already ran, which for kind: agent is a paid
// invocation the author never asked for (issue #12).
//
// A cron schedule is snapped to its own grid: the last Occurrence at or before now, or the one
// just ahead when the fire came in early. A one-time schedule is its instant exactly. An
// interval schedule reports false, because "every 30m" is measured from the last run and has no
// grid to snap to; the caller keeps the clock it already has.
func (s *Schedule) Occurrence(now time.Time) (time.Time, bool) {
	if !s.at.IsZero() {
		return s.at, true
	}
	if s.cron == nil {
		return time.Time{}, false
	}
	// Next is strictly-after, so the walk starts just outside the window it may claim.
	cur, ok := s.Next(now.Add(-FireSkew - time.Second))
	if !ok {
		return time.Time{}, false
	}
	var last time.Time
	for !cur.After(now) {
		last = cur
		next, ok := s.Next(cur)
		if !ok {
			break
		}
		cur = next
	}
	if !last.IsZero() {
		return last.In(s.spec.Location), true
	}
	// Nothing has come due yet, so this is an early fire for the Occurrence just ahead.
	// Anything further out is not a fire this schedule can explain.
	if cur.Sub(now) <= FireSkew {
		return cur.In(s.spec.Location), true
	}
	return time.Time{}, false
}

// FromResolved builds a Schedule from a job that config.Load has already resolved. Every caller
// that answers "when does this fire" goes through here, so the CLI's prediction, the TUI's
// countdown and the daemon's own arithmetic cannot drift apart.
func FromResolved(rs model.ResolvedSchedule) (*Schedule, error) {
	loc, err := LoadLocation(rs.Timezone)
	if err != nil {
		return nil, err
	}
	spec := Spec{Location: loc}
	switch rs.Type {
	case "cron":
		spec.Cron = rs.Expression
	case "every":
		spec.Every = time.Duration(rs.EverySec) * time.Second
	case model.ScheduleAt:
		spec.At = rs.At
	default:
		return nil, fmt.Errorf("unknown schedule type %q", rs.Type)
	}
	return Parse(spec)
}
