//go:build darwin

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/huketo/herdr-cron/internal/model"
)

// New returns the platform backend.
func New() (Backend, error) { return launchd{}, nil }

type launchd struct{}

func (launchd) Name() string { return "launchd" }

// agentDir is Apple's documented location for a per-user agent: the LaunchAgents
// subdirectory of the user's Library directory.
func agentDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents")
}

func label(jobID string) string {
	if jobID == "" {
		return "dev.herdr.cron"
	}
	return "dev.herdr.cron." + jobID
}

func plistPath(jobID string) string { return filepath.Join(agentDir(), label(jobID)+".plist") }

func (l launchd) Install(req Request) ([]Entry, []string, error) {
	var entries []Entry
	var warnings []string

	write := func(jobID, body string) error {
		path := plistPath(jobID)
		if err := writeFenced(path, keyOf(jobID), body); err != nil {
			return err
		}
		// load -w is the form with evidence behind it; `launchctl bootstrap gui/$UID` is
		// the modern spelling and is unverified here (docs/spec/02-architecture.md §4.2).
		if out, err := run("launchctl", "unload", "-w", path); err != nil && !strings.Contains(out, "Could not find") {
			warnings = append(warnings, "unload: "+out)
		}
		if out, err := run("launchctl", "load", "-w", path); err != nil {
			warnings = append(warnings, "load: "+out)
		}
		entries = append(entries, Entry{JobID: jobID, Name: label(jobID), Path: path, State: "ok"})
		return nil
	}

	if req.Driver == DriverDaemon {
		if err := write("", fenced("daemon", daemonPlist(req))); err != nil {
			return nil, nil, err
		}
		return entries, warnings, nil
	}
	for _, j := range req.Jobs {
		body, err := jobPlist(j, req)
		if err != nil {
			entries = append(entries, Entry{JobID: j.ID, Name: label(j.ID),
				State: "error", Detail: err.Error()})
			continue
		}
		if err := write(j.ID, fenced(j.ID, body)); err != nil {
			return nil, nil, err
		}
	}
	warnings = append(warnings,
		"launchd has no documented equivalent of systemd Persistent=: whether a missed "+
			"StartCalendarInterval fires after wake is unverified, so catch-up on macOS "+
			"relies on herdr-cron's own reconciliation pass under the daemon driver")
	return entries, warnings, nil
}

// keyOf is the marker key: the daemon artefact uses "daemon", jobs use their id.
func keyOf(jobID string) string {
	if jobID == "" {
		return "daemon"
	}
	return jobID
}

func (l launchd) Uninstall(req Request) ([]Entry, error) {
	var entries []Entry
	drop := func(jobID string) {
		path := plistPath(jobID)
		_, _ = run("launchctl", "unload", "-w", path)
		e, _ := removeFenced(path, keyOf(jobID))
		e.Name = label(jobID)
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

func (l launchd) Status(req Request) ([]Entry, error) {
	var entries []Entry
	if req.Driver == DriverDaemon {
		e := entryState(plistPath(""), "daemon", fenced("daemon", daemonPlist(req)))
		e.Name = label("")
		return append(entries, e), nil
	}
	for _, j := range req.Jobs {
		body, err := jobPlist(j, req)
		if err != nil {
			entries = append(entries, Entry{JobID: j.ID, Name: label(j.ID),
				State: "error", Detail: err.Error()})
			continue
		}
		e := entryState(plistPath(j.ID), j.ID, fenced(j.ID, body))
		e.Name = label(j.ID)
		entries = append(entries, e)
	}
	return entries, nil
}

// A plist cannot carry a `#` comment, so the marker lives in a String value that launchd
// ignores but ownedBy can still find.
func plistHeader() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`
}

func daemonPlist(req Request) string {
	return plistHeader() + fmt.Sprintf(`  <key>Label</key>            <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>daemon</string>
  </array>
  <key>RunAtLoad</key>          <true/>
  <key>KeepAlive</key>          <true/>
</dict>
</plist>
`, label(""), req.Binary)
}

func jobPlist(j *model.Resolved, req Request) (string, error) {
	interval, err := calendarInterval(j)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(plistHeader())
	fmt.Fprintf(&b, "  <key>Label</key>            <string>%s</string>\n", label(j.ID))
	b.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	fmt.Fprintf(&b, "    <string>%s</string>\n    <string>run-once</string>\n    <string>%s</string>\n",
		req.Binary, j.ID)
	b.WriteString("  </array>\n")
	b.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n" +
		"    <key>HERDR_CRON_TRIGGER</key> <string>scheduler</string>\n  </dict>\n")
	if j.Cwd != "" {
		fmt.Fprintf(&b, "  <key>WorkingDirectory</key>  <string>%s</string>\n", j.Cwd)
	}
	b.WriteString(interval)
	b.WriteString("</dict>\n</plist>\n")
	return b.String(), nil
}

// calendarInterval renders StartCalendarInterval or StartInterval.
func calendarInterval(j *model.Resolved) (string, error) {
	switch j.Schedule.Type {
	case "every":
		return fmt.Sprintf("  <key>StartInterval</key>     <integer>%d</integer>\n",
			j.Schedule.EverySec), nil
	case "at":
		t, err := time.Parse(time.RFC3339, j.Schedule.At)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrUntranslatable, err)
		}
		return fmt.Sprintf(`  <key>StartCalendarInterval</key>
  <dict>
    <key>Month</key>  <integer>%d</integer>
    <key>Day</key>    <integer>%d</integer>
    <key>Hour</key>   <integer>%d</integer>
    <key>Minute</key> <integer>%d</integer>
  </dict>
`, int(t.Month()), t.Day(), t.Hour(), t.Minute()), nil
	case "cron":
		return cronToCalendarInterval(j.Schedule.Expression)
	default:
		return "", fmt.Errorf("%w: unknown schedule type %q", ErrUntranslatable, j.Schedule.Type)
	}
}

// cronToCalendarInterval handles the exactly-translatable expressions and refuses the
// rest: launchd's dict has no ranges, steps or lists, so anything richer than a single
// value per field cannot be expressed without silently changing the schedule.
func cronToCalendarInterval(expr string) (string, error) {
	switch expr {
	case "@hourly":
		return "  <key>StartCalendarInterval</key>\n  <dict>\n    <key>Minute</key> <integer>0</integer>\n  </dict>\n", nil
	case "@daily", "@midnight":
		return "  <key>StartCalendarInterval</key>\n  <dict>\n    <key>Hour</key> <integer>0</integer>\n    <key>Minute</key> <integer>0</integer>\n  </dict>\n", nil
	}
	fields := strings.Fields(expr)
	if len(fields) == 6 {
		fields = fields[1:]
	}
	if len(fields) != 5 {
		return "", fmt.Errorf("%w: expected 5 or 6 fields, got %d", ErrUntranslatable, len(fields))
	}
	var parts []string
	add := func(key, field string) error {
		if field == "*" {
			return nil
		}
		if strings.ContainsAny(field, ",-/") {
			return fmt.Errorf("%w: launchd cannot express %q in %s", ErrUntranslatable, field, key)
		}
		var n int
		if _, err := fmt.Sscanf(field, "%d", &n); err != nil {
			return fmt.Errorf("%w: %s %q", ErrUntranslatable, key, field)
		}
		parts = append(parts, fmt.Sprintf("    <key>%s</key> <integer>%d</integer>", key, n))
		return nil
	}
	for _, spec := range []struct{ key, field string }{
		{"Minute", fields[0]}, {"Hour", fields[1]},
		{"Day", fields[2]}, {"Month", fields[3]}, {"Weekday", fields[4]},
	} {
		if err := add(spec.key, spec.field); err != nil {
			return "", err
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("%w: every-minute schedules need StartInterval", ErrUntranslatable)
	}
	return "  <key>StartCalendarInterval</key>\n  <dict>\n" +
		strings.Join(parts, "\n") + "\n  </dict>\n", nil
}
