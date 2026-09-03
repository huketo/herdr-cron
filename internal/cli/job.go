package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/huketo/herdr-cron/internal/config"
	"github.com/huketo/herdr-cron/internal/model"
	"github.com/huketo/herdr-cron/internal/schedule"
	"github.com/huketo/herdr-cron/internal/store"
)

// ------------------------------------------------------------------ payloads

type jobListResult struct {
	Type        string       `json:"type"`
	GeneratedAt string       `json:"generatedAt"`
	Daemon      daemonStatus `json:"daemon"`
	Jobs        []jobSummary `json:"jobs"`
}

type daemonStatus struct {
	Status string `json:"status"`
	PID    int    `json:"pid,omitempty"`
}

type jobSummary struct {
	ID                  string                 `json:"id"`
	Name                string                 `json:"name"`
	Kind                model.Kind             `json:"kind"`
	Enabled             bool                   `json:"enabled"`
	EnabledSource       string                 `json:"enabledSource"`
	Schedule            model.ResolvedSchedule `json:"schedule"`
	Completed           *bool                  `json:"completed,omitempty"`
	Tags                []string               `json:"tags"`
	NextRunAt           *string                `json:"nextRunAt"`
	LastRun             *lastRun               `json:"lastRun"`
	ConsecutiveFailures int                    `json:"consecutiveFailures"`
}

type lastRun struct {
	RunID       string       `json:"runId"`
	Status      model.Status `json:"status"`
	FinishedAt  *time.Time   `json:"finishedAt"`
	DurationSec *float64     `json:"durationSec"`
}

type jobResult struct {
	Type       string          `json:"type"`
	Job        *model.Resolved `json:"job"`
	Completed  *bool           `json:"completed,omitempty"`
	NextRuns   []string        `json:"nextRuns"`
	RecentRuns []*model.Run    `json:"recentRuns"`
}

type jobWrittenResult struct {
	Type     string          `json:"type"`
	Job      *model.Resolved `json:"job"`
	NextRuns []string        `json:"nextRuns"`
	Warnings []config.Issue  `json:"warnings"`
	DryRun   bool            `json:"dryRun,omitempty"`
}

type jobRemovedResult struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Purged bool   `json:"purged"`
}

type jobEnabledResult struct {
	Type          string `json:"type"`
	ID            string `json:"id"`
	Enabled       bool   `json:"enabled"`
	EnabledSource string `json:"enabledSource"`
	Reason        string `json:"reason"`
}

// ------------------------------------------------------------------ commands

func jobCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{Use: "job", Short: "Manage job definitions"}
	cmd.AddCommand(
		jobListCmd(g), jobGetCmd(g), jobAddCmd(g, false), jobAddCmd(g, true),
		jobRmCmd(g), jobEnableCmd(g, false), jobEnableCmd(g, true),
		jobRunCmd(g), jobCancelCmd(g),
	)
	return cmd
}

// loadAll reads jobs.yaml plus the mutable state a summary needs.
func (g *globals) loadAll(id string) (*config.Loaded, *store.Store, *store.State, *store.Overrides, error) {
	roots, err := g.roots()
	if err != nil {
		return nil, nil, nil, nil, failure(id, "io_error", err.Error(), ExitError, nil)
	}
	loaded, errs := config.Load(roots.JobsFile())
	if len(errs) > 0 {
		return nil, nil, nil, nil, failure(id, "config_invalid",
			fmt.Sprintf("%s has %d error(s)", roots.JobsFile(), len(errs)), ExitError, errs)
	}
	st := store.New(roots)
	state, err := st.LoadState()
	if err != nil {
		return nil, nil, nil, nil, failure(id, "io_error", err.Error(), ExitError, nil)
	}
	ov, err := st.LoadOverrides()
	if err != nil {
		return nil, nil, nil, nil, failure(id, "io_error", err.Error(), ExitError, nil)
	}
	return loaded, st, state, ov, nil
}

func jobListCmd(g *globals) *cobra.Command {
	var stateFilter, kind string
	var tags []string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List jobs",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			id := "cli:job:list"
			loaded, st, state, ov, err := g.loadAll(id)
			if err != nil {
				return err
			}
			res := jobListResult{
				Type:        "job_list",
				GeneratedAt: time.Now().Format(time.RFC3339),
				Daemon:      daemonBrief(st.Roots()),
				Jobs:        []jobSummary{},
			}
			for _, j := range loaded.Jobs {
				enabled, src := store.EffectiveEnabled(j.Enabled, ov.Overrides[j.ID])
				if !matchState(stateFilter, enabled) || !matchTags(tags, j.Tags) {
					continue
				}
				if kind != "" && string(j.Kind) != kind {
					continue
				}
				res.Jobs = append(res.Jobs, summarise(j, enabled, src, state.Jobs[j.ID]))
			}
			sortJobs(res.Jobs)
			emit(os.Stdout, g, Envelope{ID: id, Result: res})
			return nil
		},
	}
	cmd.Flags().StringVar(&stateFilter, "state", "all", "active | paused | all")
	cmd.Flags().StringVar(&kind, "kind", "", "shell | agent")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "only jobs carrying every given tag")
	return cmd
}

func matchState(filter string, enabled bool) bool {
	switch filter {
	case "active":
		return enabled
	case "paused":
		return !enabled
	default:
		return true
	}
}

func matchTags(want, have []string) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			if h == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func summarise(j *model.Resolved, enabled bool, src string, js *store.JobState) jobSummary {
	s := jobSummary{
		ID: j.ID, Name: j.Name, Kind: j.Kind,
		Enabled: enabled, EnabledSource: src,
		Schedule: j.Schedule, Tags: j.Tags,
	}
	if j.Schedule.OneShot() {
		completed := store.OneShotCompleted(j, js)
		s.Completed = &completed
	}
	if runs := nextRuns(j, 1); len(runs) > 0 {
		s.NextRunAt = &runs[0]
	}
	if js != nil {
		s.ConsecutiveFailures = js.ConsecutiveFailures
		if js.LastRunID != "" {
			s.LastRun = &lastRun{RunID: js.LastRunID, Status: js.LastStatus, FinishedAt: js.LastFinishedAt}
		}
	}
	return s
}

// baseRuns reports the un-jittered occurrences, which is what an OS scheduler entry
// fires at.
func baseRuns(j *model.Resolved, n int) []string {
	return occurrences(j, n, 0)
}

// nextRuns reports the jittered instants, because that is when the job actually runs.
func nextRuns(j *model.Resolved, n int) []string {
	return occurrences(j, n, time.Duration(j.Schedule.JitterSec)*time.Second)
}

func occurrences(j *model.Resolved, n int, jitter time.Duration) []string {
	sch, err := schedule.FromResolved(j.Schedule)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, t := range sch.NextN(time.Now(), n, jitter) {
		out = append(out, t.Format(time.RFC3339))
	}
	return out
}

func jobGetCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "get <job-id>",
		Short: "Show one job with its next runs and recent history",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			id := "cli:job:get"
			loaded, st, state, ov, err := g.loadAll(id)
			if err != nil {
				return err
			}
			j, ok := loaded.Job(args[0])
			if !ok {
				return failure(id, "job_not_found", fmt.Sprintf("no job with id %q", args[0]),
					ExitError, nil)
			}
			j.Enabled, j.EnabledSource = store.EffectiveEnabled(j.Enabled, ov.Overrides[j.ID])
			var completed *bool
			if j.Schedule.OneShot() {
				value := store.OneShotCompleted(j, state.Jobs[j.ID])
				completed = &value
			}

			runs, err := st.Runs(j.ID)
			if err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}
			emit(os.Stdout, g, Envelope{ID: id, Result: jobResult{
				Type: "job", Job: j, Completed: completed,
				NextRuns: nextRuns(j, 5), RecentRuns: tail(runs, 10),
			}})
			return nil
		},
	}
}

func tail[T any](v []T, n int) []T {
	if len(v) <= n {
		if v == nil {
			return []T{}
		}
		return v
	}
	return v[len(v)-n:]
}

func jobAddCmd(g *globals, update bool) *cobra.Command {
	var e config.Edit
	var name, description, scheduleExpr, timezone, catchup, concurrency, cwd, timeout string
	var command, prompt, agentKind, session, noOpMarker string
	var env, tags []string
	var maxAttempts, maxRunsPerDay int
	var paused, dryRun bool

	use, short := "add", "Add a job to jobs.yaml"
	if update {
		use, short = "update <job-id>", "Change fields of an existing job"
	}

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args: func(c *cobra.Command, args []string) error {
			if update {
				return cobra.ExactArgs(1)(c, args)
			}
			return cobra.NoArgs(c, args)
		},
		RunE: func(c *cobra.Command, args []string) error {
			id := "cli:job:add"
			if update {
				id = "cli:job:update"
				e.ID = args[0]
			}
			if e.ID == "" {
				return failure(id, "usage", "--id is required", ExitUsage, nil)
			}
			if command != "" && prompt != "" {
				return failure(id, "usage", "--command and --prompt are mutually exclusive", ExitUsage, nil)
			}
			if !update && command == "" && prompt == "" {
				return failure(id, "usage", "one of --command or --prompt is required", ExitUsage, nil)
			}
			if !update && scheduleExpr == "" {
				return failure(id, "usage", "--schedule is required", ExitUsage, nil)
			}
			loc, err := schedule.LoadLocation(timezone)
			if err != nil {
				return failure(id, "usage", err.Error(), ExitUsage, nil)
			}
			if scheduleExpr != "" {
				now := time.Now()
				spec, form, err := schedule.ParseExpr(scheduleExpr, now, loc)
				if err != nil {
					return failure(id, "usage", err.Error(), ExitUsage, nil)
				}
				if form == schedule.FormAt {
					at, err := time.Parse(time.RFC3339, spec.At)
					if err != nil {
						return failure(id, "usage", err.Error(), ExitUsage, nil)
					}
					// A one-shot in the past is refused rather than written: it has no
					// Occurrence left to fire, so it would sit in jobs.yaml as a job that
					// never runs and never explains itself.
					if !at.After(now) {
						msg := fmt.Sprintf(
							"scheduled instant %q has already passed (now %s); for a relative instant, use --schedule \"+2h\"",
							spec.At, now.In(loc).Format(time.RFC3339))
						return failure(id, "usage", msg, ExitUsage, nil)
					}
					// Store the instant, never the relative text: jobs.yaml is re-read on
					// every reload, so a stored "+2h" would re-anchor and never arrive.
					scheduleExpr = spec.At
				}
			}

			bind := func(flag string, dst **string, src *string) {
				if c.Flags().Changed(flag) {
					*dst = src
				}
			}
			bind("name", &e.Name, &name)
			bind("description", &e.Description, &description)
			bind("timezone", &e.Timezone, &timezone)
			bind("catchup", &e.Catchup, &catchup)
			bind("concurrency", &e.Concurrency, &concurrency)
			bind("cwd", &e.Cwd, &cwd)
			bind("timeout", &e.Timeout, &timeout)
			bind("agent-kind", &e.AgentKind, &agentKind)
			bind("session", &e.Session, &session)
			bind("no-op-marker", &e.NoOpMarker, &noOpMarker)
			if scheduleExpr != "" {
				e.Schedule = &scheduleExpr
			}
			if command != "" {
				e.Command = &command
			}
			if prompt != "" {
				e.Prompt = &prompt
			}
			if c.Flags().Changed("max-attempts") {
				e.MaxAttempts = &maxAttempts
			}
			if c.Flags().Changed("max-runs-per-day") {
				e.MaxRunsPerDay = &maxRunsPerDay
			}
			if len(tags) > 0 {
				e.Tags = tags
			}
			if len(env) > 0 {
				e.Env = map[string]string{}
				for _, kv := range env {
					k, v, ok := strings.Cut(kv, "=")
					if !ok {
						return failure(id, "usage", fmt.Sprintf("--env %q must be KEY=VALUE", kv), ExitUsage, nil)
					}
					e.Env[k] = v
				}
			}
			if paused {
				// A job that has never run has no state worth overriding, so --paused
				// writes the declared value (docs/spec/03-job-model.md §5).
				f := false
				e.Enabled = &f
			}

			roots, err := g.roots()
			if err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}

			mutate := config.AddJob(e)
			if update {
				mutate = config.UpdateJob(e)
			}
			if dryRun {
				return dryRunEdit(g, id, roots.JobsFile(), mutate, e.ID)
			}

			loaded, issues, err := config.Apply(roots.JobsFile(), mutate)
			switch {
			case err != nil && strings.Contains(err.Error(), config.ErrJobExists.Error()):
				return failure(id, "job_exists", err.Error(), ExitError, nil)
			case err != nil && strings.Contains(err.Error(), config.ErrJobNotFound.Error()):
				return failure(id, "job_not_found", err.Error(), ExitError, nil)
			case err != nil:
				return failure(id, "config_invalid", err.Error(), ExitError, nil)
			case len(issues) > 0:
				return failure(id, "config_invalid",
					"the resulting jobs.yaml would be invalid; nothing was written", ExitError, issues)
			}

			j, _ := loaded.Job(e.ID)
			emit(os.Stdout, g, Envelope{ID: id, Result: jobWrittenResult{
				Type: "job_written", Job: j, NextRuns: nextRuns(j, 5),
				Warnings: warningsFor(loaded, e.ID),
			}})
			return nil
		},
	}

	f := cmd.Flags()
	if !update {
		f.StringVar(&e.ID, "id", "", "job id, ^[a-z0-9][a-z0-9._-]{0,127}$")
	}
	f.StringVar(&name, "name", "", "display name")
	f.StringVar(&description, "description", "", "description")
	f.StringVar(&scheduleExpr, "schedule", "", "cron expression, @descriptor, duration, or RFC 3339 instant")
	f.StringVar(&timezone, "timezone", "", "IANA timezone")
	f.StringVar(&catchup, "catchup", "", "off | latest | all")
	f.StringVar(&concurrency, "concurrency", "", "skip | queue | cancel_previous | allow")
	f.StringVar(&cwd, "cwd", "", "working directory")
	f.StringVar(&timeout, "timeout", "", "duration, e.g. 45m")
	f.StringVar(&command, "command", "", "shell command; implies kind: shell")
	f.StringVar(&prompt, "prompt", "", "agent prompt; implies kind: agent")
	f.StringVar(&agentKind, "agent-kind", "", "agent kind for kind: agent")
	f.StringVar(&session, "session", "", "Herdr session for kind: agent")
	f.StringVar(&noOpMarker, "no-op-marker", "", "output that means the run did nothing")
	f.StringSliceVar(&tags, "tag", nil, "repeatable tag")
	f.StringArrayVar(&env, "env", nil, "repeatable KEY=VALUE")
	f.IntVar(&maxAttempts, "max-attempts", 1, "total attempts including the first")
	f.IntVar(&maxRunsPerDay, "max-runs-per-day", 0, "0 means unlimited")
	f.BoolVar(&paused, "paused", false, "write enabled: false")
	f.BoolVar(&dryRun, "dry-run", false, "validate and print, write nothing")
	return cmd
}

// dryRunEdit applies the mutation to a copy and reports the result without writing.
func dryRunEdit(g *globals, id, path string, mutate func(*yaml.Node) error, jobID string) error {
	tmp, err := os.CreateTemp("", "herdr-cron-dry-*.yaml")
	if err != nil {
		return failure(id, "io_error", err.Error(), ExitError, nil)
	}
	name := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(name) }()

	if b, err := os.ReadFile(path); err == nil {
		if err := os.WriteFile(name, b, 0o600); err != nil {
			return failure(id, "io_error", err.Error(), ExitError, nil)
		}
	}
	loaded, issues, err := config.Apply(name, mutate)
	switch {
	case err != nil:
		return failure(id, "config_invalid", err.Error(), ExitError, nil)
	case len(issues) > 0:
		return failure(id, "config_invalid", "the resulting jobs.yaml would be invalid", ExitError, issues)
	}
	j, _ := loaded.Job(jobID)
	emit(os.Stdout, g, Envelope{ID: id, Result: jobWrittenResult{
		Type: "job_written", Job: j, NextRuns: nextRuns(j, 5),
		Warnings: warningsFor(loaded, jobID), DryRun: true,
	}})
	return nil
}

func warningsFor(l *config.Loaded, jobID string) []config.Issue {
	out := []config.Issue{}
	for _, w := range l.Warnings {
		if w.JobID == jobID {
			out = append(out, w)
		}
	}
	return out
}

func jobRmCmd(g *globals) *cobra.Command {
	var yes, purge bool
	cmd := &cobra.Command{
		Use:   "rm <job-id>",
		Short: "Remove a job from jobs.yaml",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			id := "cli:job:rm"
			if !yes {
				return failure(id, "usage", "refusing to remove without --yes", ExitUsage, nil)
			}
			roots, err := g.roots()
			if err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}
			_, issues, err := config.Apply(roots.JobsFile(), config.RemoveJob(args[0]))
			switch {
			case err != nil && strings.Contains(err.Error(), config.ErrJobNotFound.Error()):
				return failure(id, "job_not_found", err.Error(), ExitError, nil)
			case err != nil:
				return failure(id, "config_invalid", err.Error(), ExitError, nil)
			case len(issues) > 0:
				return failure(id, "config_invalid",
					"the resulting jobs.yaml would be invalid; nothing was written", ExitError, issues)
			}
			if err := store.New(roots).ForgetJob(args[0], purge); err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}
			emit(os.Stdout, g, Envelope{ID: id,
				Result: jobRemovedResult{Type: "job_removed", ID: args[0], Purged: purge}})
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the removal")
	cmd.Flags().BoolVar(&purge, "purge", false, "also delete the job's history and logs")
	return cmd
}

func jobEnableCmd(g *globals, resume bool) *cobra.Command {
	use, short := "pause <job-id>", "Stop scheduling a job without editing jobs.yaml"
	if resume {
		use, short = "resume <job-id>", "Resume a paused job"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			id := "cli:job:pause"
			if resume {
				id = "cli:job:resume"
			}
			loaded, st, _, _, err := g.loadAll(id)
			if err != nil {
				return err
			}
			j, ok := loaded.Job(args[0])
			if !ok {
				return failure(id, "job_not_found", fmt.Sprintf("no job with id %q", args[0]),
					ExitError, nil)
			}
			// The override never rewrites the user's YAML; it records the declared value so
			// a later hand edit invalidates it (docs/spec/03-job-model.md §5).
			if err := st.SetEnabled(j.ID, resume, j.Enabled, "manual"); err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}
			ov, err := st.LoadOverrides()
			if err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}
			enabled, src := store.EffectiveEnabled(j.Enabled, ov.Overrides[j.ID])
			emit(os.Stdout, g, Envelope{ID: id, Result: jobEnabledResult{
				Type: "job_enabled_changed", ID: j.ID, Enabled: enabled,
				EnabledSource: src, Reason: "manual",
			}})
			return nil
		},
	}
}

// sortJobs keeps list output stable regardless of map iteration.
func sortJobs(js []jobSummary) {
	sort.Slice(js, func(i, k int) bool { return js[i].ID < js[k].ID })
}

// effectiveEnabled resolves a job's enabled state against the override store, which is
// the same resolution job list and job get use (docs/spec/03-job-model.md §5).
func effectiveEnabled(j *model.Resolved, ov *store.Overrides) (bool, string) {
	if ov == nil {
		return j.Enabled, "file"
	}
	return store.EffectiveEnabled(j.Enabled, ov.Overrides[j.ID])
}
