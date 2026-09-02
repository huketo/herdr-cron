//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/huketo/herdr-cron/internal/model"
)

// New returns the platform backend.
func New() (Backend, error) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		// Linux without systemd is unsupported by this driver, and no crontab entry is
		// ever written: crontab cannot express Persistent= (docs/spec/02-architecture.md §4.1).
		return nil, fmt.Errorf("%w: systemctl not found; use the daemon driver", ErrUnsupported)
	}
	return systemd{}, nil
}

type systemd struct{}

func (systemd) Name() string { return "systemd-user" }

func unitDir() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "systemd", "user")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

func (s systemd) Install(req Request) ([]Entry, []string, error) {
	var entries []Entry
	var warnings []string

	if req.Driver == DriverDaemon {
		name := "herdr-cron.service"
		path := filepath.Join(unitDir(), name)
		body := fenced("daemon", daemonUnit(req))
		if err := writeFenced(path, "daemon", body); err != nil {
			return nil, nil, err
		}
		if out, err := run("systemctl", "--user", "daemon-reload"); err != nil {
			warnings = append(warnings, "daemon-reload: "+out)
		}
		if out, err := run("systemctl", "--user", "enable", "--now", name); err != nil {
			warnings = append(warnings, "enable: "+out)
		}
		warnings = append(warnings, linger()...)
		entries = append(entries, Entry{Name: name, Path: path, State: "ok"})
		return entries, warnings, nil
	}

	for _, j := range req.Jobs {
		cal, err := onCalendar(j, req)
		if err != nil {
			// Refuse rather than register something that fires at the wrong time.
			entries = append(entries, Entry{JobID: j.ID, Name: "herdr-cron-" + j.ID,
				State: "error", Detail: err.Error()})
			continue
		}
		svcName := "herdr-cron-" + j.ID + ".service"
		timName := "herdr-cron-" + j.ID + ".timer"
		svcPath := filepath.Join(unitDir(), svcName)
		timPath := filepath.Join(unitDir(), timName)

		if err := writeFenced(svcPath, j.ID, fenced(j.ID, jobUnit(j, req))); err != nil {
			return nil, nil, err
		}
		if err := writeFenced(timPath, j.ID, fenced(j.ID, jobTimer(j, cal))); err != nil {
			return nil, nil, err
		}
		entries = append(entries,
			Entry{JobID: j.ID, Name: svcName, Path: svcPath, State: "ok"},
			Entry{JobID: j.ID, Name: timName, Path: timPath, State: "ok"})
	}

	if out, err := run("systemctl", "--user", "daemon-reload"); err != nil {
		warnings = append(warnings, "daemon-reload: "+out)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name, ".timer") || e.State != "ok" {
			continue
		}
		if out, err := run("systemctl", "--user", "enable", "--now", e.Name); err != nil {
			warnings = append(warnings, e.Name+": "+out)
		}
	}
	warnings = append(warnings, linger()...)
	return entries, warnings, nil
}

// linger is what makes user timers fire while logged out. It never fails an install.
func linger() []string {
	user := os.Getenv("USER")
	if user == "" {
		return nil
	}
	if out, err := run("loginctl", "enable-linger", user); err != nil {
		return []string{"enable-linger (jobs will not fire while logged out): " + out}
	}
	return nil
}

func (s systemd) Uninstall(req Request) ([]Entry, error) {
	var entries []Entry
	if req.Driver == DriverDaemon {
		_, _ = run("systemctl", "--user", "disable", "--now", "herdr-cron.service")
		e, _ := removeFenced(filepath.Join(unitDir(), "herdr-cron.service"), "daemon")
		entries = append(entries, e)
	}
	for _, j := range req.Jobs {
		_, _ = run("systemctl", "--user", "disable", "--now", "herdr-cron-"+j.ID+".timer")
		for _, suffix := range []string{".timer", ".service"} {
			e, _ := removeFenced(filepath.Join(unitDir(), "herdr-cron-"+j.ID+suffix), j.ID)
			entries = append(entries, e)
		}
	}
	// Units for jobs that have since left jobs.yaml are still herdr-cron's, so an
	// uninstall must take them too — otherwise a removed job keeps firing run-once and
	// failing job_not_found forever.
	entries = append(entries, sweep(req)...)
	_, _ = run("systemctl", "--user", "daemon-reload")
	return entries, nil
}

func (s systemd) Status(req Request) ([]Entry, error) {
	var entries []Entry
	if req.Driver == DriverDaemon {
		entries = append(entries, entryState(filepath.Join(unitDir(), "herdr-cron.service"),
			"daemon", fenced("daemon", daemonUnit(req))))
		return entries, nil
	}
	for _, j := range req.Jobs {
		cal, err := onCalendar(j, req)
		if err != nil {
			entries = append(entries, Entry{JobID: j.ID, Name: "herdr-cron-" + j.ID,
				State: "error", Detail: err.Error()})
			continue
		}
		entries = append(entries,
			entryState(filepath.Join(unitDir(), "herdr-cron-"+j.ID+".service"), j.ID,
				fenced(j.ID, jobUnit(j, req))),
			entryState(filepath.Join(unitDir(), "herdr-cron-"+j.ID+".timer"), j.ID,
				fenced(j.ID, jobTimer(j, cal))))
	}
	entries = append(entries, orphans(req)...)
	return entries, nil
}

// sweep removes every remaining marker-owned unit, whatever job it names. Files without
// our marker are left alone and reported.
func sweep(req Request) []Entry {
	var out []Entry
	for _, pattern := range []string{"herdr-cron-*.timer", "herdr-cron-*.service"} {
		names, err := filepath.Glob(filepath.Join(unitDir(), pattern))
		if err != nil {
			continue
		}
		for _, p := range names {
			id := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
			id = strings.TrimPrefix(id, "herdr-cron-")
			if strings.HasSuffix(p, ".timer") {
				_, _ = run("systemctl", "--user", "disable", "--now", filepath.Base(p))
			}
			e, _ := removeFenced(p, id)
			if e.State == "missing" {
				e.Detail = "removed a unit left over from a job no longer in jobs.yaml"
			}
			out = append(out, e)
		}
	}
	return out
}

// orphans finds units for jobs that are no longer in jobs.yaml — the drift the spec calls
// "orphan" (docs/spec/02-architecture.md §4.4).
func orphans(req Request) []Entry {
	live := map[string]bool{}
	for _, j := range req.Jobs {
		live[j.ID] = true
	}
	names, err := filepath.Glob(filepath.Join(unitDir(), "herdr-cron-*.timer"))
	if err != nil {
		return nil
	}
	var out []Entry
	for _, p := range names {
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "herdr-cron-"), ".timer")
		if live[id] {
			continue
		}
		out = append(out, Entry{JobID: id, Name: filepath.Base(p), Path: p, State: "orphan",
			Detail: "registered but no longer in jobs.yaml"})
	}
	return out
}

func daemonUnit(req Request) string {
	return fmt.Sprintf(`[Unit]
Description=herdr-cron scheduler
After=network-online.target time-sync.target

[Service]
Type=simple
ExecStart=%s daemon
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, req.Binary)
}

func jobUnit(j *model.Resolved, req Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Unit]\nDescription=herdr-cron job %s\n", j.ID)
	b.WriteString("After=network-online.target time-sync.target\n\n[Service]\nType=oneshot\n")
	fmt.Fprintf(&b, "ExecStart=%s run-once %s\n", req.Binary, j.ID)
	// The provenance of a run started by the OS scheduler is `scheduler`, not `manual`.
	b.WriteString("Environment=HERDR_CRON_TRIGGER=scheduler\n")
	if j.Cwd != "" {
		fmt.Fprintf(&b, "WorkingDirectory=%s\n", j.Cwd)
	}
	if j.TimeoutSec > 0 {
		// Plus a minute, so systemd never kills a run before herdr-cron's own timeout
		// records status: "timeout".
		fmt.Fprintf(&b, "TimeoutStartSec=%d\n", j.TimeoutSec+60)
	}
	return b.String()
}

func jobTimer(j *model.Resolved, calendar string) string {
	persistent := "false"
	if j.Schedule.Catchup != model.CatchupOff {
		// This is the whole reason the os-scheduler driver exists: Persistent= gives
		// exactly-one catch-up across power-off, for free.
		persistent = "true"
	}
	return fmt.Sprintf(`[Unit]
Description=herdr-cron timer for %s

[Timer]
%s
Persistent=%s
RandomizedDelaySec=0
AccuracySec=1s

[Install]
WantedBy=timers.target
`, j.ID, calendar, persistent)
}

// onCalendar translates a job schedule into systemd's grammar and then self-checks the
// translation against herdr-cron's own next-run prediction. A mismatch aborts the job
// rather than registering a timer that fires at the wrong moment
// (docs/spec/02-architecture.md §4.1).
func onCalendar(j *model.Resolved, req Request) (string, error) {
	switch j.Schedule.Type {
	case "every":
		if j.Schedule.EverySec <= 0 {
			return "", fmt.Errorf("%w: interval is not positive", ErrUntranslatable)
		}
		// OnUnitActiveSec= is the only expression for a fixed interval, and Persistent=
		// does not apply to it — so an interval job gets no OS catch-up.
		return fmt.Sprintf("OnBootSec=1min\nOnUnitActiveSec=%ds", j.Schedule.EverySec), nil
	case "at":
		t, err := time.Parse(time.RFC3339, j.Schedule.At)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrUntranslatable, err)
		}
		return "OnCalendar=" + t.Format("2006-01-02 15:04:05"), nil
	case "cron":
		expr, err := cronToCalendar(j.Schedule.Expression)
		if err != nil {
			return "", err
		}
		if err := verifyCalendar(expr, j, req); err != nil {
			return "", err
		}
		return "OnCalendar=" + expr, nil
	default:
		return "", fmt.Errorf("%w: unknown schedule type %q", ErrUntranslatable, j.Schedule.Type)
	}
}

var dowNames = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// cronToCalendar handles the expressions that translate exactly. It refuses the rest,
// because cron's day-of-month/day-of-week OR is an AND in systemd and a silently wrong
// timer is worse than no timer.
func cronToCalendar(expr string) (string, error) {
	fields := strings.Fields(expr)
	if strings.HasPrefix(expr, "@") {
		switch expr {
		case "@hourly":
			return "*-*-* *:00:00", nil
		case "@daily", "@midnight":
			return "*-*-* 00:00:00", nil
		case "@weekly":
			return "Sun *-*-* 00:00:00", nil
		case "@monthly":
			return "*-*-01 00:00:00", nil
		case "@yearly", "@annually":
			return "*-01-01 00:00:00", nil
		}
		return "", fmt.Errorf("%w: descriptor %q", ErrUntranslatable, expr)
	}
	if len(fields) == 6 {
		fields = fields[1:] // seconds are handled by the trailing :SS below
	}
	if len(fields) != 5 {
		return "", fmt.Errorf("%w: expected 5 or 6 fields, got %d", ErrUntranslatable, len(fields))
	}
	min, hour, dom, mon, dow := fields[0], fields[1], fields[2], fields[3], fields[4]

	if dom != "*" && dow != "*" {
		// cron ORs these two; systemd ANDs them. No exact translation exists.
		return "", fmt.Errorf("%w: day-of-month and day-of-week are both set, "+
			"which cron treats as OR and systemd as AND", ErrUntranslatable)
	}
	for _, f := range []string{min, hour, dom, mon} {
		if strings.ContainsAny(f, "/,") {
			return "", fmt.Errorf("%w: step or list syntax in %q", ErrUntranslatable, f)
		}
	}

	dowPart := ""
	if dow != "*" {
		names, err := dowRange(dow)
		if err != nil {
			return "", err
		}
		dowPart = names + " "
	}
	return fmt.Sprintf("%s*-%s-%s %s:%s:00", dowPart,
		pad2(mon, "*"), pad2(dom, "*"), pad2(hour, "*"), pad2(min, "*")), nil
}

func dowRange(f string) (string, error) {
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
		return dowNames[n], nil
	}
	if strings.Contains(f, "-") {
		parts := strings.SplitN(f, "-", 2)
		lo, err := name(parts[0])
		if err != nil {
			return "", err
		}
		hi, err := name(parts[1])
		if err != nil {
			return "", err
		}
		return lo + ".." + hi, nil
	}
	if strings.Contains(f, ",") {
		var out []string
		for _, p := range strings.Split(f, ",") {
			n, err := name(p)
			if err != nil {
				return "", err
			}
			out = append(out, n)
		}
		return strings.Join(out, ","), nil
	}
	return name(f)
}

func pad2(v, star string) string {
	if v == "*" {
		return star
	}
	if len(v) == 1 {
		return "0" + v
	}
	return v
}

// verifyCalendar asks systemd itself when the expression next fires and compares with
// herdr-cron's own prediction. Without this the translation is a guess.
func verifyCalendar(expr string, j *model.Resolved, req Request) error {
	if req.NextRun == nil {
		return nil
	}
	out, err := run("systemd-analyze", "calendar", expr)
	if err != nil {
		// No systemd-analyze on this box: the translation stays unverified,
		// which is a warning-level condition, not a reason to refuse the
		// install (docs/spec/02-architecture.md §4.1).
		//nolint:nilerr // an absent verifier is not an invalid schedule
		return nil
	}
	want, err := req.NextRun(j)
	if err != nil {
		// herdr-cron cannot predict this job's next run, so there is nothing to
		// compare systemd against. The schedule itself was already validated by
		// the caller; failing here would refuse an install over a missing
		// cross-check.
		//nolint:nilerr // nothing to compare against is not a disagreement
		return nil
	}
	// systemd-analyze prints "Next elapse: Wed 2026-09-03 03:17:00 KST".
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "Next elapse:") {
			line = l
			break
		}
	}
	if line == "" {
		return nil
	}
	// systemd prints "Thu 2026-09-03 03:17:00 KST" while herdr-cron predicts RFC 3339,
	// so the date and the time are matched separately — a substring match on the
	// RFC 3339 form can never succeed, because systemd never prints the `T`.
	if len(want) < 16 {
		return nil
	}
	date, hhmm := want[:10], want[11:16]
	if !strings.Contains(line, date) || !strings.Contains(line, hhmm) {
		return fmt.Errorf("%w: systemd would next fire %q but herdr-cron predicts %s",
			ErrUntranslatable, strings.TrimSpace(line), want)
	}
	return nil
}
