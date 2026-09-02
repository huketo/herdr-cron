//go:build windows

package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/huketo/herdr-cron/internal/model"
)

// New returns the platform backend.
func New() (Backend, error) { return schtasks{}, nil }

type schtasks struct{}

func (schtasks) Name() string { return "windows-task-scheduler" }

// Task names live in a herdr-cron folder, which is the marker: Task Scheduler has no
// comment field, so the namespace is what makes ownership recognisable and removal safe.
func taskName(jobID string) string {
	if jobID == "" {
		return `\herdr-cron\daemon`
	}
	return `\herdr-cron\` + jobID
}

func (s schtasks) Install(req Request) ([]Entry, []string, error) {
	var entries []Entry
	warnings := []string{
		"Task Scheduler registration is [UNVERIFIED]: no Windows machine was available " +
			"when this was written. Verify with `schtasks /Query /TN \\herdr-cron\\ /V /FO LIST`",
	}

	create := func(jobID string, trigger []string) {
		name := taskName(jobID)
		args := []string{"/Create", "/F", "/TN", name, "/TR", command(jobID, req)}
		args = append(args, trigger...)
		// /IT runs only when the user is logged on, which avoids needing the password a
		// non-interactive task would require.
		args = append(args, "/IT")
		if out, err := run("schtasks", args...); err != nil {
			entries = append(entries, Entry{JobID: jobID, Name: name, State: "error", Detail: out})
			return
		}
		entries = append(entries, Entry{JobID: jobID, Name: name, State: "ok"})
	}

	if req.Driver == DriverDaemon {
		create("", []string{"/SC", "ONLOGON"})
		return entries, warnings, nil
	}
	for _, j := range req.Jobs {
		trigger, err := triggerFor(j)
		if err != nil {
			entries = append(entries, Entry{JobID: j.ID, Name: taskName(j.ID),
				State: "error", Detail: err.Error()})
			continue
		}
		create(j.ID, trigger)
	}
	return entries, warnings, nil
}

// command wraps run-once so the trigger provenance is set without a wrapper script.
func command(jobID string, req Request) string {
	if jobID == "" {
		return fmt.Sprintf(`cmd /c set HERDR_CRON_TRIGGER=scheduler ^& "%s" daemon`, req.Binary)
	}
	return fmt.Sprintf(`cmd /c set HERDR_CRON_TRIGGER=scheduler ^& "%s" run-once %s`,
		req.Binary, jobID)
}

func (s schtasks) Uninstall(req Request) ([]Entry, error) {
	var entries []Entry
	drop := func(jobID string) {
		name := taskName(jobID)
		out, err := run("schtasks", "/Delete", "/F", "/TN", name)
		e := Entry{JobID: jobID, Name: name, State: "missing"}
		if err != nil && !strings.Contains(strings.ToLower(out), "cannot find") {
			e.State, e.Detail = "error", out
		}
		entries = append(entries, e)
	}
	if req.Driver == DriverDaemon {
		drop("")
		return entries, nil
	}
	for _, j := range req.Jobs {
		drop(j.ID)
	}
	return entries, nil
}

func (s schtasks) Status(req Request) ([]Entry, error) {
	var entries []Entry
	check := func(jobID string) {
		name := taskName(jobID)
		out, err := run("schtasks", "/Query", "/TN", name)
		e := Entry{JobID: jobID, Name: name, State: "ok"}
		if err != nil {
			e.State, e.Detail = "missing", out
		}
		entries = append(entries, e)
	}
	if req.Driver == DriverDaemon {
		check("")
		return entries, nil
	}
	for _, j := range req.Jobs {
		check(j.ID)
	}
	return entries, nil
}

// triggerFor maps a schedule onto schtasks flags, refusing anything the grammar cannot
// express exactly.
func triggerFor(j *model.Resolved) ([]string, error) {
	switch j.Schedule.Type {
	case "every":
		mins := j.Schedule.EverySec / 60
		if mins < 1 {
			return nil, fmt.Errorf("%w: Task Scheduler's smallest interval is one minute",
				ErrUntranslatable)
		}
		if mins > 1439 {
			return nil, fmt.Errorf("%w: /MO for MINUTE tops out below a day", ErrUntranslatable)
		}
		return []string{"/SC", "MINUTE", "/MO", fmt.Sprint(mins)}, nil
	case "at":
		t, err := time.Parse(time.RFC3339, j.Schedule.At)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUntranslatable, err)
		}
		return []string{"/SC", "ONCE", "/SD", t.Format("01/02/2006"), "/ST", t.Format("15:04")}, nil
	case "cron":
		return cronToSchtasks(j.Schedule.Expression)
	default:
		return nil, fmt.Errorf("%w: unknown schedule type %q", ErrUntranslatable, j.Schedule.Type)
	}
}

var schtasksDays = []string{"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"}

func cronToSchtasks(expr string) ([]string, error) {
	switch expr {
	case "@hourly":
		return []string{"/SC", "HOURLY"}, nil
	case "@daily", "@midnight":
		return []string{"/SC", "DAILY", "/ST", "00:00"}, nil
	}
	fields := strings.Fields(expr)
	if len(fields) == 6 {
		fields = fields[1:]
	}
	if len(fields) != 5 {
		return nil, fmt.Errorf("%w: expected 5 or 6 fields, got %d", ErrUntranslatable, len(fields))
	}
	min, hour, dom, mon, dow := fields[0], fields[1], fields[2], fields[3], fields[4]
	if strings.ContainsAny(min+hour, ",-/*") {
		return nil, fmt.Errorf("%w: /ST needs a single hour and minute", ErrUntranslatable)
	}
	at := fmt.Sprintf("%s:%s", pad2(hour), pad2(min))

	if dom != "*" && dow != "*" {
		return nil, fmt.Errorf("%w: day-of-month and day-of-week are both set", ErrUntranslatable)
	}
	if mon != "*" {
		return nil, fmt.Errorf("%w: month restrictions need /SC MONTHLY /M", ErrUntranslatable)
	}
	switch {
	case dow != "*":
		days, err := schtasksDOW(dow)
		if err != nil {
			return nil, err
		}
		return []string{"/SC", "WEEKLY", "/D", days, "/ST", at}, nil
	case dom != "*":
		if strings.ContainsAny(dom, ",-/") {
			return nil, fmt.Errorf("%w: /D for MONTHLY takes a single day", ErrUntranslatable)
		}
		return []string{"/SC", "MONTHLY", "/D", dom, "/ST", at}, nil
	default:
		return []string{"/SC", "DAILY", "/ST", at}, nil
	}
}

func schtasksDOW(f string) (string, error) {
	name := func(s string) (string, error) {
		var n int
		if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
			return "", fmt.Errorf("%w: day-of-week %q", ErrUntranslatable, f)
		}
		if n == 7 {
			n = 0
		}
		if n < 0 || n > 6 {
			return "", fmt.Errorf("%w: day-of-week %q", ErrUntranslatable, f)
		}
		return schtasksDays[n], nil
	}
	var out []string
	if strings.Contains(f, "-") {
		parts := strings.SplitN(f, "-", 2)
		var lo, hi int
		if _, err := fmt.Sscanf(parts[0], "%d", &lo); err != nil {
			return "", fmt.Errorf("%w: day-of-week %q", ErrUntranslatable, f)
		}
		if _, err := fmt.Sscanf(parts[1], "%d", &hi); err != nil {
			return "", fmt.Errorf("%w: day-of-week %q", ErrUntranslatable, f)
		}
		for d := lo; d <= hi; d++ {
			n, err := name(fmt.Sprint(d))
			if err != nil {
				return "", err
			}
			out = append(out, n)
		}
		return strings.Join(out, ","), nil
	}
	for _, p := range strings.Split(f, ",") {
		n, err := name(p)
		if err != nil {
			return "", err
		}
		out = append(out, n)
	}
	return strings.Join(out, ","), nil
}

func pad2(v string) string {
	if len(v) == 1 {
		return "0" + v
	}
	return v
}
