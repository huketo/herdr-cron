// Package paths resolves the config and state roots specified in docs/spec/04-storage.md §1.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// Roots are the two directories herdr-cron owns.
type Roots struct {
	Config string // directory holding jobs.yaml and config.toml
	State  string // directory holding state.json, runs/, logs/, triggers/

	// configFile is set only when an explicit jobs.yaml path was given.
	configFile string
}

// Overrides carries the flag-level overrides, which win over everything else.
type Overrides struct {
	ConfigFile string // --config: path to jobs.yaml, not a directory
	StateDir   string // --state-dir
}

// Resolve applies the precedence of docs/spec/04-storage.md §1:
// flags, then HERDR_CRON_*, then XDG_*, then the per-OS defaults.
func Resolve(ov Overrides) (Roots, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Roots{}, err
	}

	cfg, state := defaults(home)

	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		cfg = filepath.Join(v, appName)
	}
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		state = filepath.Join(v, appName)
	}
	// HERDR_PLUGIN_STATE_DIR is deliberately NOT consulted.
	//
	// Herdr sets it for plugin-private state, and 04-storage.md §1 originally
	// required it as the state root so that nothing durable would live under
	// HERDR_PLUGIN_ROOT, which Herdr replaces wholesale on every update. The
	// premise is right; the conclusion was wrong. The default state root is
	// already outside HERDR_PLUGIN_ROOT, so honouring the variable bought
	// nothing — and it cost the single-instance guarantee.
	//
	// Observed on 2026-09-02: the daemon started by the [[startup]] hook
	// resolved ~/.local/state/herdr/plugins/huketo.herdr-cron while a daemon
	// started from a terminal resolved ~/.local/state/herdr-cron. Both read
	// the same jobs.yaml. daemon.lock lives in the state root, so each held its
	// own lock, both were live at once, and every occurrence of a 15-second job
	// executed twice. For a kind: agent job that is two agent runs and two
	// bills per occurrence — the exact failure this project exists to prevent.
	//
	// The state root must be a function of the machine, never of which front
	// door started the process: D4 says both front doors are the same binary
	// over the same on-disk state. A plugin that genuinely needs a private
	// root can still pass --state-dir or set HERDR_CRON_STATE_DIR.
	if v := os.Getenv("HERDR_CRON_HOME"); v != "" {
		cfg = filepath.Join(v, "config")
		state = filepath.Join(v, "state")
	}
	if v := os.Getenv("HERDR_CRON_STATE_DIR"); v != "" {
		state = v
	}

	r := Roots{Config: cfg, State: state}

	if v := os.Getenv("HERDR_CRON_CONFIG"); v != "" {
		r.Config = filepath.Dir(v)
		r.configFile = v
	}
	if ov.StateDir != "" {
		r.State = ov.StateDir
	}
	if ov.ConfigFile != "" {
		r.Config = filepath.Dir(ov.ConfigFile)
		r.configFile = ov.ConfigFile
	}
	return r, nil
}

const appName = "herdr-cron"

func defaults(home string) (cfg, state string) {
	switch runtime.GOOS {
	case "windows":
		// LocalAppData, never Roaming: a roaming job database would fire jobs against
		// absolute paths that do not exist on the other machine.
		base := os.Getenv("LocalAppData")
		if base == "" {
			base = filepath.Join(home, "AppData", "Local")
		}
		root := filepath.Join(base, appName)
		return filepath.Join(root, "config"), filepath.Join(root, "state")
	case "darwin":
		// Config, data and state collapse into one directory on macOS, so split them here.
		root := filepath.Join(home, "Library", "Application Support", appName)
		return filepath.Join(root, "config"), filepath.Join(root, "state")
	default:
		return filepath.Join(home, ".config", appName),
			filepath.Join(home, ".local", "state", appName)
	}
}

// JobsFile is the path of jobs.yaml.
func (r Roots) JobsFile() string {
	if r.configFile != "" {
		return r.configFile
	}
	return filepath.Join(r.Config, "jobs.yaml")
}

// StateFile is state.json: the mutable per-job state — catch-up watermark, last run and
// outcome, consecutive-failure count, runs-today counter. Written only by the executing
// scheduler process (docs/spec/04-storage.md §4).
func (r Roots) StateFile() string { return filepath.Join(r.State, "state.json") }

// OverridesFile is overrides.json: the effective-enabled overrides written by job
// pause/resume, the TUI toggle and the auto-disable breaker. It is a separate file from
// state.json because it has three writers and must be writable with no daemon running
// (docs/spec/04-storage.md §4).
func (r Roots) OverridesFile() string { return filepath.Join(r.State, "overrides.json") }

// LogDir holds one <runId>.log per run of jobID — interleaved stdout+stderr for shell jobs,
// the captured transcript for agent jobs (docs/spec/04-storage.md §6).
func (r Roots) LogDir(jobID string) string {
	return filepath.Join(r.State, "logs", jobID)
}

// OverridesLock is the advisory-lock sidecar held across every read-modify-write of
// overrides.json, by whichever of the CLI, the TUI or the daemon is writing. It is what makes
// job pause work both while a daemon runs and while none does (docs/spec/04-storage.md §9).
func (r Roots) OverridesLock() string { return filepath.Join(r.State, "overrides.lock") }

// DaemonFile is daemon.json: the heartbeat record (pid, startedAt, heartbeatAt, driver,
// configError) rewritten every 15 s. Half of the socketless liveness protocol; daemon.lock is
// the authoritative half, since a kill -9 leaves a fresh-looking heartbeat behind
// (docs/spec/04-storage.md §7).
func (r Roots) DaemonFile() string { return filepath.Join(r.State, "daemon.json") }

// TriggersDir holds one <ulid>.json request file per job run, job cancel or reload; the daemon
// claims a file by renaming it, which is what makes double-processing impossible without a
// lock (docs/spec/04-storage.md §8). Pause and resume do not use this channel.
func (r Roots) TriggersDir() string { return filepath.Join(r.State, "triggers") }

// TmpDir stages every atomic write. It lives under the state root so the rename onto the
// target never crosses a filesystem boundary (docs/spec/04-storage.md §2).
func (r Roots) TmpDir() string { return filepath.Join(r.State, "tmp") }

// RunsFile is jobID's append-only history, one JSON object per line: a "running" record when a
// run starts and a second, terminal record when it finishes. Readers reduce by runId
// (docs/spec/04-storage.md §5).
func (r Roots) RunsFile(jobID string) string {
	return filepath.Join(r.State, "runs", jobID+".jsonl")
}

// LogFile is the absolute path of one run's captured output, opened for streaming so that
// run logs --follow works while the run is live. Run records store the relative form; see
// LogFileRel.
func (r Roots) LogFile(jobID, runID string) string {
	return filepath.Join(r.State, "logs", jobID, runID+".log")
}

// LogFileRel is the repository-relative form recorded in a run record.
func (r Roots) LogFileRel(jobID, runID string) string {
	return filepath.ToSlash(filepath.Join("logs", jobID, runID+".log"))
}

// EnsureState creates the state directories that every command may write to.
func (r Roots) EnsureState() error {
	for _, d := range []string{r.State, r.TriggersDir(), r.TmpDir(), filepath.Join(r.State, "runs"), filepath.Join(r.State, "logs")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
