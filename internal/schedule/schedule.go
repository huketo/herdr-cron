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
