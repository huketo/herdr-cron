package daemon

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/huketo/herdr-cron/internal/model"
)

// notifyTimeout bounds a notifier: reporting must never hold up the schedule.
const notifyTimeout = 10 * time.Second

// notifyEvent fires the job's notifier when the event is subscribed.
//
// Delivery is best effort by design. `herdr notification show` provably returns
// {"shown": false, "reason": "no_foreground_client"} on a headless server
// (docs/research/2026-09-02-herdr-plugin-integration.md §9.5), which is the normal case
// for a scheduler, so a failure here never changes a run's outcome
// (docs/spec/03-job-model.md §4.6).
func (d *Daemon) notifyEvent(j *model.Resolved, event, summary string) {
	if !subscribed(j.Notify.On, event) {
		return
	}
	argv := j.Notify.Command
	if len(argv) == 0 {
		argv = []string{"herdr", "notification", "show", j.Name, "--body", summary}
	} else {
		argv = expand(argv, map[string]string{
			"{{.JobID}}":   j.ID,
			"{{.JobName}}": j.Name,
			"{{.Event}}":   event,
			"{{.Summary}}": summary,
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		d.log.Warn("notifier failed", "job", j.ID, "event", event,
			"command", argv[0], "error", err, "output", strings.TrimSpace(string(out)))
		return
	}
	d.log.Debug("notified", "job", j.ID, "event", event,
		"output", strings.TrimSpace(string(out)))
}

func subscribed(on []string, event string) bool {
	for _, e := range on {
		if e == event {
			return true
		}
	}
	return false
}

func expand(argv []string, vars map[string]string) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		for k, v := range vars {
			a = strings.ReplaceAll(a, k, v)
		}
		out[i] = a
	}
	return out
}
