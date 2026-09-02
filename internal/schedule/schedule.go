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
	"time"

	"github.com/go-co-op/gocron/v2"
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
