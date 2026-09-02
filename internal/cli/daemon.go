package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/huketo/herdr-cron/internal/config"
	"github.com/huketo/herdr-cron/internal/daemon"
	"github.com/huketo/herdr-cron/internal/model"
	"github.com/huketo/herdr-cron/internal/paths"
	"github.com/huketo/herdr-cron/internal/store"
)

// daemon.TriggerGrace is how long a client waits for a daemon to claim its trigger before
// reporting daemon_unreachable (docs/spec/04-storage.md §8).

type statusResult struct {
	Type        string            `json:"type"`
	Daemon      daemonStatusFull  `json:"daemon"`
	Roots       map[string]string `json:"roots"`
	JobCount    int               `json:"jobCount"`
	ConfigError *string           `json:"configError"`
	NextRuns    []nextRun         `json:"nextRuns"`
}

type daemonStatusFull struct {
	Status      string     `json:"status"` // running | stale | stopped
	PID         int        `json:"pid,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	HeartbeatAt *time.Time `json:"heartbeatAt,omitempty"`
	Driver      string     `json:"driver,omitempty"`
	Version     string     `json:"version,omitempty"`
}

type nextRun struct {
	JobID string `json:"jobId"`
	At    string `json:"at"`
}

type reloadResult struct {
	Type     string `json:"type"`
	Accepted bool   `json:"accepted"`
}

type runStartedResult struct {
	Type  string `json:"type"`
	RunID string `json:"runId"`
	JobID string `json:"jobId"`
	Wait  bool   `json:"wait"`
}

// ------------------------------------------------------------------- daemon

func daemonCmd(g *globals) *cobra.Command {
	var foreground, detach bool
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the schedule",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			id := "cli:daemon"
			if foreground && detach {
				return failure(id, "usage", "--foreground and --detach are mutually exclusive", ExitUsage, nil)
			}
			roots, err := g.roots()
			if err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}
			if detach {
				return spawnDetached(g, id, roots)
			}

			level := slog.LevelInfo
			log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
			driver := "daemon"
			if foreground {
				driver = "foreground"
			}

			ctx, stop := signal.NotifyContext(context.Background(),
				os.Interrupt, syscall.SIGTERM)
			defer stop()

			d := daemon.New(roots, log, versionOr(g.info.Version), driver)
			switch err := d.Run(ctx); {
			case errors.Is(err, daemon.ErrAlreadyRunning):
				return failure(id, "daemon_already_running",
					"another daemon holds "+filepath.Join(roots.State, "daemon.lock"), ExitError, nil)
			case err != nil:
				return failure(id, "internal", err.Error(), ExitError, nil)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "log to stderr; the Herdr-pane driver")
	cmd.Flags().BoolVar(&detach, "detach", false, "start in the background and return once the lock is held")
	return cmd
}

// spawnDetached starts the daemon as a background process and waits for its first
// heartbeat. It is idempotent, because a Herdr [[startup]] hook re-runs on every server
// start and every live handoff (docs/spec/05-cli.md §3.3).
func spawnDetached(g *globals, id string, roots paths.Roots) error {
	if err := roots.EnsureState(); err != nil {
		return failure(id, "io_error", err.Error(), ExitError, nil)
	}
	if alive(roots) {
		hb := readHeartbeat(roots)
		emit(os.Stdout, g, Envelope{ID: id, Result: map[string]any{
			"type": "daemon_started", "pid": hb.PID, "alreadyRunning": true,
		}})
		return nil
	}

	self, err := os.Executable()
	if err != nil {
		return failure(id, "io_error", err.Error(), ExitError, nil)
	}
	args := []string{"daemon"}
	if g.configFile != "" {
		args = append(args, "--config", g.configFile)
	}
	if g.stateDir != "" {
		args = append(args, "--state-dir", g.stateDir)
	}
	logPath := filepath.Join(roots.State, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return failure(id, "io_error", err.Error(), ExitError, nil)
	}
	defer func() { _ = logFile.Close() }()

	// Deliberately not CommandContext: this child must outlive the process that
	// starts it. `daemon --detach` exists so a Herdr [[startup]] hook can return
	// immediately (docs/spec/05-cli.md §3.3), and binding the daemon to this
	// CLI's context would kill the scheduler the moment the hook exits.
	//nolint:noctx // the detached daemon must survive its parent
	cmd := exec.Command(self, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		return failure(id, "io_error", err.Error(), ExitError, nil)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(daemon.TriggerGrace)
	for time.Now().Before(deadline) {
		if alive(roots) {
			hb := readHeartbeat(roots)
			emit(os.Stdout, g, Envelope{ID: id, Result: map[string]any{
				"type": "daemon_started", "pid": hb.PID, "alreadyRunning": false,
			}})
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return failure(id, "internal",
		"the daemon did not write a heartbeat; see "+logPath, ExitError, nil)
}

// ------------------------------------------------------------------- status

func readHeartbeat(roots paths.Roots) *daemon.Heartbeat { return daemon.ReadHeartbeat(roots) }

// fresh is the heartbeat half of the liveness test (docs/spec/04-storage.md §7).
func fresh(hb *daemon.Heartbeat) bool {
	return time.Since(hb.HeartbeatAt) < 60*time.Second
}

// alive reports whether a daemon is actually serving this state root.
func alive(roots paths.Roots) bool {
	hb := readHeartbeat(roots)
	return hb != nil && fresh(hb) && daemon.LockHeld(roots)
}

func daemonState(roots paths.Roots) daemonStatusFull {
	hb := readHeartbeat(roots)
	if hb == nil {
		return daemonStatusFull{Status: "stopped"}
	}
	s := daemonStatusFull{
		Status: "stale", PID: hb.PID, Driver: hb.Driver, Version: hb.Version,
		StartedAt: &hb.StartedAt, HeartbeatAt: &hb.HeartbeatAt,
	}
	if fresh(hb) && daemon.LockHeld(roots) {
		s.Status = "running"
	}
	return s
}

// daemonBrief is the two-field projection carried by payloads whose subject is
// the jobs, not the scheduler — `job_list` (docs/spec/05-cli.md §3.1).
//
// It delegates rather than re-deriving, so `job list` and `status` can never
// disagree about whether a daemon is up. They did: `job list` shipped a
// hardcoded "stopped", so an agent deciding whether `job run` would work was
// told "no daemon" while one was running and answering triggers.
func daemonBrief(roots paths.Roots) daemonStatus {
	full := daemonState(roots)
	return daemonStatus{Status: full.Status, PID: full.PID}
}

func statusCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report daemon liveness, roots, and the next occurrences",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			id := "cli:status"
			roots, err := g.roots()
			if err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}
			res := statusResult{
				Type:   "status",
				Daemon: daemonState(roots),
				Roots: map[string]string{
					"config": roots.Config, "state": roots.State, "jobs": roots.JobsFile(),
				},
				NextRuns: []nextRun{},
			}
			if hb := readHeartbeat(roots); hb != nil {
				res.ConfigError = hb.ConfigError
			}

			loaded, errs := config.Load(roots.JobsFile())
			if len(errs) > 0 {
				msg := fmt.Sprintf("%s has %d error(s)", roots.JobsFile(), len(errs))
				res.ConfigError = &msg
				emit(os.Stdout, g, Envelope{ID: id, Result: res})
				return nil
			}
			res.JobCount = len(loaded.Jobs)

			st := store.New(roots)
			ov, _ := st.LoadOverrides()
			for _, j := range loaded.Jobs {
				if ov != nil {
					if enabled, _ := store.EffectiveEnabled(j.Enabled, ov.Overrides[j.ID]); !enabled {
						continue
					}
				}
				for _, at := range nextRuns(j, 1) {
					res.NextRuns = append(res.NextRuns, nextRun{JobID: j.ID, At: at})
				}
			}
			sortNextRuns(res.NextRuns)
			if len(res.NextRuns) > 3 {
				res.NextRuns = res.NextRuns[:3]
			}
			emit(os.Stdout, g, Envelope{ID: id, Result: res})
			return nil
		},
	}
}

func sortNextRuns(v []nextRun) {
	for i := 1; i < len(v); i++ {
		for k := i; k > 0 && v[k].At < v[k-1].At; k-- {
			v[k], v[k-1] = v[k-1], v[k]
		}
	}
}

// ------------------------------------------------------------ trigger client

func reloadCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Ask the daemon to re-read jobs.yaml",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			id := "cli:reload"
			roots, err := g.roots()
			if err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}
			tr := daemon.Trigger{ID: daemon.NewTriggerID(), CreatedAt: time.Now(),
				Action: "reload", RequestedBy: "cli", Wait: true}
			path, err := daemon.WriteTrigger(roots, tr)
			if err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}
			if _, err := daemon.AwaitTrigger(roots, tr, path, true); err != nil {
				return failure(id, "daemon_unreachable", err.Error(), ExitError, nil)
			}
			emit(os.Stdout, g, Envelope{ID: id,
				Result: reloadResult{Type: "reload_requested", Accepted: true}})
			return nil
		},
	}
}

func jobRunCmd(g *globals) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "run <job-id>",
		Short: "Ask the daemon to run a job now",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			id := "cli:job:run"
			loaded, st, _, _, err := g.loadAll(id)
			if err != nil {
				return err
			}
			if _, ok := loaded.Job(args[0]); !ok {
				return failure(id, "job_not_found",
					fmt.Sprintf("no job with id %q", args[0]), ExitError, nil)
			}
			roots := st.Roots()
			tr := daemon.Trigger{ID: daemon.NewTriggerID(), CreatedAt: time.Now(),
				Action: "run", JobID: args[0], RequestedBy: "cli", Wait: wait}
			path, werr := daemon.WriteTrigger(roots, tr)
			if werr != nil {
				return failure(id, "io_error", werr.Error(), ExitError, nil)
			}
			res, aerr := daemon.AwaitTrigger(roots, tr, path, wait)
			if aerr != nil {
				return failure(id, "daemon_unreachable",
					aerr.Error()+"; use `herdr-cron run-once` when no daemon is running",
					ExitError, nil)
			}
			if !wait || res == nil {
				emit(os.Stdout, g, Envelope{ID: id, Result: runStartedResult{
					Type: "run_started", JobID: args[0], Wait: wait}})
				return nil
			}
			if res.Status == "error" {
				return failure(id, "internal", res.Error, ExitError, nil)
			}
			run, ferr := findRun(st, loaded, res.RunID)
			if ferr != nil || run == nil {
				return failure(id, "run_not_found",
					"the daemon reported "+res.Status+" but no record was found", ExitError, nil)
			}
			return emitRun(g, id, run)
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "block until the run is terminal")
	return cmd
}

func jobCancelCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <job-id>",
		Short: "Cancel the job's running execution",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			id := "cli:job:cancel"
			roots, err := g.roots()
			if err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}
			tr := daemon.Trigger{ID: daemon.NewTriggerID(), CreatedAt: time.Now(),
				Action: "cancel", JobID: args[0], RequestedBy: "cli", Wait: true}
			path, werr := daemon.WriteTrigger(roots, tr)
			if werr != nil {
				return failure(id, "io_error", werr.Error(), ExitError, nil)
			}
			res, aerr := daemon.AwaitTrigger(roots, tr, path, true)
			if aerr != nil {
				return failure(id, "daemon_unreachable", aerr.Error(), ExitError, nil)
			}
			if res != nil && res.Status == "error" {
				return failure(id, "run_not_found", res.Error, ExitError, nil)
			}
			emit(os.Stdout, g, Envelope{ID: id, Result: map[string]any{
				"type": "run_cancelled", "jobId": args[0]}})
			return nil
		},
	}
}

// emitRun applies the exit-code contract of docs/spec/05-cli.md §2.2.
func emitRun(g *globals, id string, run *model.Run) error {
	switch run.Status {
	case model.StatusSuccess, model.StatusNoOp, model.StatusSkipped:
		emit(os.Stdout, g, Envelope{ID: id, Result: runResult{Type: "run", Run: run}})
		return nil
	case model.StatusBlocked:
		return &fail{env: Envelope{ID: id, Error: &Error{Code: "agent_blocked",
			Message: "the run is blocked and needs a human",
			Details: runResult{Type: "run", Run: run}}}, code: ExitBlocked}
	default:
		return &fail{env: Envelope{ID: id, Error: &Error{Code: "run_failed",
			Message: fmt.Sprintf("run %s finished %s", run.RunID, run.Status),
			Details: runResult{Type: "run", Run: run}}}, code: ExitError}
	}
}
