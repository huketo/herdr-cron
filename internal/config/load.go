// Package config loads and validates jobs.yaml (docs/spec/03-job-model.md §7).
package config

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/huketo/herdr-cron/internal/herdr"
	"github.com/huketo/herdr-cron/internal/model"
	"github.com/huketo/herdr-cron/internal/schedule"
)

// Issue is one validation error or warning.
type Issue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	JobID   string `json:"jobId,omitempty"`
	Field   string `json:"field,omitempty"`
}

func (i Issue) String() string {
	if i.JobID != "" {
		return fmt.Sprintf("%s [%s%s]: %s", i.Code, i.JobID, dotField(i.Field), i.Message)
	}
	return fmt.Sprintf("%s: %s", i.Code, i.Message)
}

func dotField(f string) string {
	if f == "" {
		return ""
	}
	return "." + f
}

// Loaded is a validated jobs.yaml.
type Loaded struct {
	Path     string
	Jobs     []*model.Resolved
	Warnings []Issue
}

// Job returns the resolved job with the given id.
func (l *Loaded) Job(id string) (*model.Resolved, bool) {
	for _, j := range l.Jobs {
		if j.ID == id {
			return j, true
		}
	}
	return nil, false
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

const supportedVersion = 1

// Load reads, parses and validates jobs.yaml. Errors are returned as issues, not as a
// single error, so a caller can report every problem at once.
func Load(path string) (*Loaded, []Issue) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Loaded{Path: path}, nil
		}
		return nil, []Issue{{Code: "io_error", Message: err.Error()}}
	}

	var f model.File
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true) // an unknown key is an error, never a silent no-op
	if err := dec.Decode(&f); err != nil {
		return nil, []Issue{{Code: "config_invalid", Message: yamlMessage(err)}}
	}
	if f.Version != supportedVersion {
		return nil, []Issue{{Code: "config_invalid",
			Message: fmt.Sprintf("version %d is not supported; this build understands version %d", f.Version, supportedVersion)}}
	}

	l := &Loaded{Path: path}
	var errs []Issue
	seen := map[string]bool{}

	for i, j := range f.Jobs {
		where := j.ID
		if where == "" {
			where = fmt.Sprintf("jobs[%d]", i)
		}
		if !idRe.MatchString(j.ID) {
			errs = append(errs, Issue{Code: "config_invalid", JobID: where, Field: "id",
				Message: "id must match ^[a-z0-9][a-z0-9._-]{0,127}$"})
			continue
		}
		if seen[j.ID] {
			errs = append(errs, Issue{Code: "config_invalid", JobID: j.ID, Field: "id",
				Message: "duplicate id"})
			continue
		}
		seen[j.ID] = true

		res, jerrs, jwarns := resolve(j, f.Defaults)
		errs = append(errs, jerrs...)
		l.Warnings = append(l.Warnings, jwarns...)
		if len(jerrs) == 0 {
			l.Jobs = append(l.Jobs, res)
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return l, nil
}

func yamlMessage(err error) string {
	// yaml.v3 prefixes multi-error output with a banner; keep it, it names the line numbers.
	return strings.TrimSpace(err.Error())
}

// Built-in defaults from docs/spec/03-job-model.md §1.2 and §4.
const (
	defTimeout       = 30 * time.Minute
	defCatchupWindow = 168 * time.Hour
	// A one-shot's catch-up window is an hour, not a week. A missed recurrence replayed
	// late is early for the next one; a one-time "back up before the demo" replayed three
	// days later is an agent let loose on a repository nobody is watching any more.
	defOneShotCatchupWindow = time.Hour
	defRetryInitial         = 60 * time.Second
	defRetryMaxInt          = 30 * time.Minute
	defMaxConsecFail        = 3
	defAgentRunsDay         = 24
	maxJitter               = 30 * time.Minute
)

func resolve(j *model.Job, d *model.Defaults) (*model.Resolved, []Issue, []Issue) {
	var errs, warns []Issue
	bad := func(field, msg string) {
		errs = append(errs, Issue{Code: "config_invalid", JobID: j.ID, Field: field, Message: msg})
	}
	warn := func(code, field, msg string) {
		warns = append(warns, Issue{Code: code, JobID: j.ID, Field: field, Message: msg})
	}

	r := &model.Resolved{
		ID:          j.ID,
		Name:        firstNonEmpty(j.Name, j.ID),
		Description: j.Description,
		Tags:        j.Tags,
		Kind:        j.Kind,
	}
	if r.Tags == nil {
		r.Tags = []string{}
	}

	r.Enabled = true
	if d != nil && d.Enabled != nil {
		r.Enabled = *d.Enabled
	}
	if j.Enabled != nil {
		r.Enabled = *j.Enabled
	}
	r.EnabledSource = "file"

	// Timezone.
	tzName := firstNonEmpty(j.Schedule.Timezone, defStr(d, func(x *model.Defaults) string { return x.Timezone }), "local")
	loc, err := schedule.LoadLocation(tzName)
	if err != nil {
		bad("schedule.timezone", err.Error())
	}

	// Schedule: exactly one of cron, every, at.
	forms := 0
	if j.Schedule.Cron != "" {
		forms++
	}
	if j.Schedule.Every != nil {
		forms++
	}
	if j.Schedule.At != "" {
		forms++
	}
	switch {
	case forms == 0:
		bad("schedule", "exactly one of cron, every, or at is required")
	case forms > 1:
		bad("schedule", "exactly one of cron, every, or at may be set")
	}

	if loc != nil && forms == 1 {
		spec := schedule.Spec{
			Cron:     j.Schedule.Cron,
			At:       j.Schedule.At,
			Location: loc,
		}
		if j.Schedule.Every != nil {
			spec.Every = j.Schedule.Every.Std()
		}
		if _, err := schedule.Parse(spec); err != nil {
			bad("schedule", err.Error())
		}
		switch {
		case spec.Cron != "":
			r.Schedule.Type = "cron"
			r.Schedule.Expression = spec.Cron
		case spec.Every > 0:
			r.Schedule.Type = "every"
			r.Schedule.EverySec = int64(spec.Every / time.Second)
		default:
			r.Schedule.Type = "at"
			r.Schedule.At = spec.At
		}
	}
	r.Schedule.Timezone = tzName

	r.Schedule.Catchup = model.Catchup(firstNonEmpty(
		string(j.Schedule.Catchup),
		defStr(d, func(x *model.Defaults) string { return string(x.Catchup) }),
		string(model.CatchupLatest)))
	switch r.Schedule.Catchup {
	case model.CatchupOff, model.CatchupLatest, model.CatchupAll:
	default:
		bad("schedule.catchup", "must be off, latest, or all")
	}

	fallbackWindow := defCatchupWindow
	if r.Schedule.OneShot() {
		fallbackWindow = defOneShotCatchupWindow
	}
	cw := defTimeoutOr(j.Schedule.CatchupWindow, defDur(d, func(x *model.Defaults) *model.Duration { return x.CatchupWindow }), fallbackWindow)
	r.Schedule.CatchupWindowSec = int64(cw / time.Second)

	jitterSpec := firstNonEmpty(j.Schedule.Jitter, defStr(d, func(x *model.Defaults) string { return x.Jitter }), "auto")
	switch jitterSpec {
	case "auto":
		r.Schedule.JitterSec = int64(autoJitter(j.ID, r.Schedule) / time.Second)
	case "off":
		r.Schedule.JitterSec = 0
	default:
		v, err := time.ParseDuration(jitterSpec)
		if err != nil {
			bad("schedule.jitter", fmt.Sprintf("must be auto, off, or a duration: %v", err))
		} else {
			r.Schedule.JitterSec = int64(v / time.Second)
		}
	}
	// Jitter spreads a herd of jobs that share a cron minute. A one-shot names one instant
	// on purpose, and moving it is a bug the author cannot see: 18:00 fired at 18:23, with
	// validate predicting 18:00 because it never applied the offset.
	if r.Schedule.OneShot() && r.Schedule.JitterSec != 0 {
		if jitterSpec != "auto" {
			warn("jitter_ignored", "schedule.jitter",
				"a one-time schedule fires at the instant it names; jitter is ignored")
		}
		r.Schedule.JitterSec = 0
	}

	// Kind and payload.
	switch j.Kind {
	case model.KindShell:
		if j.Agent != nil {
			bad("agent", "kind is shell but an agent payload is present")
		}
		if j.Shell == nil || strings.TrimSpace(j.Shell.Command) == "" {
			bad("shell.command", "required and must be non-empty for kind: shell")
		} else {
			sh := firstNonEmpty(j.Shell.Shell, "auto")
			r.Payload = model.ShellPayload{Command: j.Shell.Command, Shell: sh}
		}
	case model.KindAgent:
		if j.Shell != nil {
			bad("shell", "kind is agent but a shell payload is present")
		}
		if j.Agent == nil || strings.TrimSpace(j.Agent.Prompt) == "" {
			bad("agent.prompt", "required and must be non-empty for kind: agent")
		} else {
			p := model.AgentPayload{
				AgentKind:  firstNonEmpty(j.Agent.AgentKind, "claude"),
				Prompt:     j.Agent.Prompt,
				Capture:    firstNonEmpty(j.Agent.Capture, "transcript"),
				NoOpMarker: j.Agent.NoOpMarker,
				Session:    firstNonEmpty(j.Agent.Session, "herdr-cron"),
				Worktree:   j.Agent.Worktree,
			}
			r.Payload = p
		}
	case "":
		bad("kind", "required; must be shell or agent")
	default:
		bad("kind", fmt.Sprintf("unknown kind %q; v1 supports shell and agent", j.Kind))
	}

	// cwd.
	cwd, err := expandPath(j.Cwd)
	if err != nil {
		bad("cwd", err.Error())
	} else if cwd != "" && !filepath.IsAbs(cwd) {
		bad("cwd", fmt.Sprintf("must be absolute after expansion, got %q", cwd))
	}
	r.Cwd = cwd
	r.Env = j.Env

	timeout := defTimeoutOr(j.Timeout, defDur(d, func(x *model.Defaults) *model.Duration { return x.Timeout }), defTimeout)
	r.TimeoutSec = int64(timeout / time.Second)
	if j.Timeout != nil && *j.Timeout == model.Duration(-1) {
		r.TimeoutSec = -1
	}

	r.Concurrency = model.Concurrency(firstNonEmpty(
		string(j.Concurrency),
		defStr(d, func(x *model.Defaults) string { return string(x.Concurrency) }),
		string(model.ConcSkip)))
	switch r.Concurrency {
	case model.ConcSkip, model.ConcQueue, model.ConcCancelPrevious, model.ConcAllow:
	default:
		bad("concurrency", "must be skip, queue, cancel_previous, or allow")
	}

	r.Retry = resolveRetry(j.Retry, defRetry(d))
	if r.Retry.Backoff != "exponential" && r.Retry.Backoff != "fixed" {
		bad("retry.backoff", "must be exponential or fixed")
	}
	if r.Retry.MaxAttempts < 1 {
		bad("retry.max_attempts", "must be >= 1")
	}

	r.Limits = resolveLimits(j.Limits, defLimits(d), j.Kind)
	r.Notify = resolveNotify(j.Notify, defNotify(d))

	// Level 4: environment warnings, which never block a write. A job may legitimately be
	// authored on a machine where its target repo has not been cloned yet
	// (docs/spec/03-job-model.md §7).
	if r.Cwd != "" {
		if st, err := os.Stat(r.Cwd); err != nil || !st.IsDir() {
			warn("cwd_missing", "cwd", fmt.Sprintf("%s does not exist on this machine", r.Cwd))
		}
	}
	if p, ok := r.Payload.(model.AgentPayload); ok {
		if _, err := herdr.Resolve(); err != nil {
			warn("herdr_unavailable", "kind",
				"no herdr binary was found; kind: agent jobs cannot run on this machine")
		}
		switch verdict, _ := herdr.CheckTrust(p.AgentKind, r.Cwd); verdict {
		case herdr.TrustUntrusted:
			// The verified unattended failure: agent start parks on a safety dialog
			// nobody can answer (docs/spec/07-herdr-integration.md §5).
			warn("cwd_not_trusted", "cwd", fmt.Sprintf(
				"%s has never been trusted for %s; a scheduled run would block. Fix it once: cd %s && %s",
				p.AgentKind, r.Cwd, r.Cwd, p.AgentKind))
		case herdr.TrustUnknown:
			warn("trust_unknown", "agent.agent_kind",
				"trust pre-flight unavailable for kind "+p.AgentKind)
		}
	}
	return r, errs, warns
}

// autoJitter is deterministic in the job id, so the same job always starts at the same
// offset (docs/spec/03-job-model.md §2.1).
func autoJitter(id string, s model.ResolvedSchedule) time.Duration {
	interval := time.Hour * 24
	if s.Type == "every" && s.EverySec > 0 {
		interval = time.Duration(s.EverySec) * time.Second
	}
	span := interval / 2
	if span > maxJitter {
		span = maxJitter
	}
	if span <= 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	return time.Duration(h.Sum64()%uint64(span/time.Second)) * time.Second
}

func resolveRetry(j, d *model.Retry) model.ResolvedRetry {
	out := model.ResolvedRetry{MaxAttempts: 1, Backoff: "exponential",
		InitialSec: int64(defRetryInitial / time.Second), MaxIntervalSec: int64(defRetryMaxInt / time.Second)}
	for _, r := range []*model.Retry{d, j} {
		if r == nil {
			continue
		}
		if r.MaxAttempts != nil {
			out.MaxAttempts = *r.MaxAttempts
		}
		if r.Backoff != "" {
			out.Backoff = r.Backoff
		}
		if r.Initial != nil {
			out.InitialSec = r.Initial.Seconds()
		}
		if r.MaxInterval != nil {
			out.MaxIntervalSec = r.MaxInterval.Seconds()
		}
	}
	return out
}

func resolveLimits(j, d *model.Limits, kind model.Kind) model.ResolvedLimits {
	out := model.ResolvedLimits{MaxConsecutiveFailures: defMaxConsecFail}
	if kind == model.KindAgent {
		out.MaxRunsPerDay = defAgentRunsDay
	}
	for _, l := range []*model.Limits{d, j} {
		if l == nil {
			continue
		}
		if l.MaxRunsPerDay != nil {
			out.MaxRunsPerDay = *l.MaxRunsPerDay
		}
		if l.MaxConsecutiveFailures != nil {
			out.MaxConsecutiveFailures = *l.MaxConsecutiveFailures
		}
	}
	return out
}
func resolveNotify(j, d *model.Notify) model.ResolvedNotify {
	out := model.ResolvedNotify{On: []string{"failure", "blocked", "auto_disabled"}}
	for _, n := range []*model.Notify{d, j} {
		if n == nil {
			continue
		}
		if n.On != nil {
			out.On = n.On
		}
		if n.Command != nil {
			out.Command = n.Command
		}
	}
	return out
}
func defRetry(d *model.Defaults) *model.Retry {
	if d == nil {
		return nil
	}
	return d.Retry
}

func defLimits(d *model.Defaults) *model.Limits {
	if d == nil {
		return nil
	}
	return d.Limits
}

func defNotify(d *model.Defaults) *model.Notify {
	if d == nil {
		return nil
	}
	return d.Notify
}

func defStr(d *model.Defaults, get func(*model.Defaults) string) string {
	if d == nil {
		return ""
	}
	return get(d)
}

func defDur(d *model.Defaults, get func(*model.Defaults) *model.Duration) *model.Duration {
	if d == nil {
		return nil
	}
	return get(d)
}

func defTimeoutOr(job, def *model.Duration, fallback time.Duration) time.Duration {
	if job != nil && *job != 0 {
		return job.Std()
	}
	if def != nil && *def != 0 {
		return def.Std()
	}
	return fallback
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// expandPath expands a leading ~ and any $VAR references.
func expandPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	p = os.ExpandEnv(p)
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Clean(p), nil
}
