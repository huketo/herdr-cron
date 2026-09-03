package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/huketo/herdr-cron/internal/config"
	"github.com/huketo/herdr-cron/internal/daemon"
	"github.com/huketo/herdr-cron/internal/model"
	"github.com/huketo/herdr-cron/internal/paths"
	"github.com/huketo/herdr-cron/internal/schedule"
	"github.com/huketo/herdr-cron/internal/store"
)

// Snapshot is everything one render needs. It is a plain file read: the TUI never locks
// to render (docs/spec/06-tui.md §1.2).
type Snapshot struct {
	At          time.Time
	Jobs        []JobView
	ConfigError string
	Daemon      DaemonView
	Roots       paths.Roots
}

// JobView is a resolved job plus the mutable state the list and detail screens show.
type JobView struct {
	Job                 *model.Resolved
	Enabled             bool
	EnabledSource       string
	AutoDisabled        bool
	Completed           bool
	NextRunAt           time.Time
	NextRuns            []time.Time
	ConsecutiveFailures int
	RunsToday           int
	Last                *model.Run
	Running             bool
}

// DaemonView is the liveness of the scheduler, which the header always shows.
type DaemonView struct {
	Status string // running | stale | stopped
	PID    int
}

// ScheduleText renders the three schedule forms of docs/spec/03-job-model.md §2 into the
// one Schedule column the list has: the cron expression verbatim, "every <duration>", or
// "at <time>".
func (j JobView) ScheduleText() string {
	s := j.Job.Schedule
	switch s.Type {
	case "cron":
		return s.Expression
	case "every":
		return "every " + (time.Duration(s.EverySec) * time.Second).String()
	default:
		return "at " + s.At
	}
}

// Load reads the whole store. Errors are folded into the snapshot rather than returned,
// because a broken jobs.yaml must be visible in the header, not fatal to the TUI
// (docs/spec/06-tui.md §7.4).
func Load(roots paths.Roots) Snapshot {
	snap := Snapshot{At: time.Now(), Roots: roots, Daemon: daemonView(roots)}

	loaded, errs := config.Load(roots.JobsFile())
	if len(errs) > 0 {
		snap.ConfigError = errs[0].String()
		if len(errs) > 1 {
			snap.ConfigError += " (+" + strconv.Itoa(len(errs)-1) + " more)"
		}
		return snap
	}

	st := store.New(roots)
	state, err := st.LoadState()
	if err != nil {
		state = &store.State{Jobs: map[string]*store.JobState{}}
	}
	ov, err := st.LoadOverrides()
	if err != nil {
		ov = &store.Overrides{Overrides: map[string]*store.Override{}}
	}

	for _, j := range loaded.Jobs {
		js := state.Jobs[j.ID]
		v := JobView{Job: j, Completed: j.Schedule.OneShot() && store.OneShotCompleted(j, js)}
		v.Enabled, v.EnabledSource = store.EffectiveEnabled(j.Enabled, ov.Overrides[j.ID])
		if o := ov.Overrides[j.ID]; o != nil && o.Reason == "auto_failures" && !v.Enabled {
			v.AutoDisabled = true
		}
		if js != nil {
			v.ConsecutiveFailures = js.ConsecutiveFailures
			if js.RunsToday != nil && js.RunsToday.Date == time.Now().Format("2006-01-02") {
				v.RunsToday = js.RunsToday.Count
			}
		}
		if runs, err := st.Runs(j.ID); err == nil && len(runs) > 0 {
			last := runs[len(runs)-1]
			v.Last = last
			v.Running = last.Status == model.StatusRunning
		}
		if v.Enabled {
			v.NextRuns = nextRuns(j, 5)
			if len(v.NextRuns) > 0 {
				v.NextRunAt = v.NextRuns[0]
			}
		}
		snap.Jobs = append(snap.Jobs, v)
	}
	sort.Slice(snap.Jobs, func(a, b int) bool { return snap.Jobs[a].Job.ID < snap.Jobs[b].Job.ID })
	return snap
}

// Runs reads one job's history, newest last.
func Runs(roots paths.Roots, jobID string) []*model.Run {
	runs, err := store.New(roots).Runs(jobID)
	if err != nil {
		return nil
	}
	return runs
}

// LogText reads a run's captured output.
func LogText(roots paths.Roots, run *model.Run) string {
	if run == nil {
		return ""
	}
	path := roots.LogFile(run.JobID, run.RunID)
	if run.LogPath != "" {
		path = filepath.Join(roots.State, filepath.FromSlash(run.LogPath))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "(no log on disk)"
	}
	return string(b)
}

// Job looks up one job by ID. The snapshot is a slice, not a map, because render order is
// the list's order; a linear scan over a handful of jobs is not worth a second index.
func (s Snapshot) Job(id string) (JobView, bool) {
	for _, j := range s.Jobs {
		if j.Job.ID == id {
			return j, true
		}
	}
	return JobView{}, false
}

func daemonView(roots paths.Roots) DaemonView {
	hb := daemon.ReadHeartbeat(roots)
	if hb == nil {
		return DaemonView{Status: "stopped"}
	}
	v := DaemonView{Status: "stale", PID: hb.PID}
	if time.Since(hb.HeartbeatAt) < 60*time.Second && daemon.LockHeld(roots) {
		v.Status = "running"
	}
	return v
}

func nextRuns(j *model.Resolved, n int) []time.Time {
	sch, err := schedule.FromResolved(j.Schedule)
	if err != nil {
		return nil
	}
	return sch.NextN(time.Now(), n, time.Duration(j.Schedule.JitterSec)*time.Second)
}
