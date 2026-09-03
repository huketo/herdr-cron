// Package cli implements the command surface of docs/spec/05-cli.md.
//
// This build ships the two commands that need neither a daemon nor Herdr:
// `validate` and `run-once`.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/huketo/herdr-cron/internal/config"
	"github.com/huketo/herdr-cron/internal/model"
	"github.com/huketo/herdr-cron/internal/paths"
	"github.com/huketo/herdr-cron/internal/runner"
	"github.com/huketo/herdr-cron/internal/schedule"
	"github.com/huketo/herdr-cron/internal/store"
	"github.com/huketo/herdr-cron/internal/tui"
	"github.com/huketo/herdr-cron/skills"
)

// Exit codes (docs/spec/05-cli.md §2.2).
const (
	ExitOK      = 0
	ExitError   = 1
	ExitUsage   = 2
	ExitBlocked = 3
)

type globals struct {
	output     string
	configFile string
	stateDir   string
	quiet      bool
	// info is the resolved build provenance. It reaches `--version`, the
	// `schema` payload, and the daemon heartbeat.
	info BuildInfo
}

// Envelope is the single response shape (docs/spec/05-cli.md §2).
type Envelope struct {
	ID     string `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

// Error is the failure half of an Envelope. Code is the stable part and the
// only thing an agent should branch on — Message is prose and carries no
// compatibility promise (docs/spec/05-cli.md §2.1). Hint names the command that
// would fix it, so a caller can recover without consulting the docs.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Details any    `json:"details,omitempty"`
}

// fail carries an exit code alongside the envelope error.
type fail struct {
	env  Envelope
	code int
}

func (f *fail) Error() string { return f.env.Error.Message }

func failure(id, code, msg string, exit int, details any) *fail {
	return &fail{env: Envelope{ID: id, Error: &Error{Code: code, Message: msg, Details: details}}, code: exit}
}

// Execute builds the command tree and runs it, returning the process exit code.
func Execute(args []string, info BuildInfo) int {
	g := &globals{info: info.Resolve()}
	root := newRoot(g)
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		return ExitOK
	}
	var f *fail
	if ok := asFail(err, &f); ok {
		emit(os.Stdout, g, f.env)
		fmt.Fprintln(os.Stderr, "herdr-cron: "+f.env.Error.Message)
		return f.code
	}
	env := Envelope{ID: "cli", Error: &Error{Code: "usage", Message: err.Error()}}
	emit(os.Stdout, g, env)
	fmt.Fprintln(os.Stderr, "herdr-cron: "+err.Error())
	return ExitUsage
}

// NewRootCommand builds the whole command tree against a caller-supplied
// BuildInfo. It is exported so the documentation drift guard walks the real
// surface rather than a hand-maintained copy of it.
func NewRootCommand(info BuildInfo) *cobra.Command {
	return newRoot(&globals{output: "json", info: info})
}

func newRoot(g *globals) *cobra.Command {
	var printSkill, showVersion bool
	root := &cobra.Command{
		Use:           "herdr-cron",
		Short:         "Schedule automated work for coding agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if printSkill {
				// Byte-identical to skills/herdr-cron/SKILL.md, guaranteed by //go:embed
				// and a test (docs/spec/08-agent-skill.md §2).
				if err := skills.Print(os.Stdout); err != nil {
					return failure("cli", "internal", err.Error(), ExitError, nil)
				}
				return nil
			}
			if showVersion {
				return printVersion(cmd.OutOrStdout(), g)
			}
			// Bare invocation launches the TUI on a TTY. Without one it is a usage
			// error, never a hang: an agent that pipes a TUI has hung, and a hang is
			// worse than an error (docs/spec/05-cli.md §1).
			if !isTerminal(os.Stdout) {
				return failure("cli", "usage",
					"stdout is not a terminal; run a subcommand (see --help)", ExitUsage, nil)
			}
			roots, err := g.roots()
			if err != nil {
				return failure("cli", "io_error", err.Error(), ExitError, nil)
			}
			if err := tui.Run(roots); err != nil {
				return failure("cli", "internal", err.Error(), ExitError, nil)
			}
			return nil
		},
	}
	root.Flags().BoolVar(&printSkill, "skill", false, "print the bundled SKILL.md and exit")
	root.Flags().BoolVarP(&showVersion, "version", "V", false, "print the version and exit")
	root.PersistentFlags().StringVarP(&g.output, "output", "o", "json", "json | text")
	root.PersistentFlags().StringVar(&g.configFile, "config", "", "path to jobs.yaml")
	root.PersistentFlags().StringVar(&g.stateDir, "state-dir", "", "state root")
	root.PersistentFlags().BoolVarP(&g.quiet, "quiet", "q", false, "suppress warnings")

	root.AddCommand(jobCmd(g), runCmd(g), validateCmd(g), runOnceCmd(g),
		daemonCmd(g), statusCmd(g), reloadCmd(g),
		schemaCmd(g), completionCmd(), installCLICmd(g), serviceCmd(g))
	return root
}

// asFail unwraps to the *fail an internal command returned, so the exit code
// and the JSON envelope survive a wrapping fmt.Errorf on the way out. A bare
// type assertion silently degraded a wrapped failure to exit 2.
func asFail(err error, out **fail) bool {
	return errors.As(err, out)
}

func (g *globals) roots() (paths.Roots, error) {
	return paths.Resolve(paths.Overrides{ConfigFile: g.configFile, StateDir: g.stateDir})
}

func emit(w io.Writer, g *globals, env Envelope) {
	if g.output == "text" {
		emitText(w, env)
		return
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(env)
}

func emitText(w io.Writer, env Envelope) {
	if env.Error != nil {
		fmt.Fprintf(w, "error %s: %s\n", env.Error.Code, env.Error.Message)
		return
	}
	b, _ := json.MarshalIndent(env.Result, "", "  ")
	fmt.Fprintln(w, string(b))
}

// emitRaw writes one compact JSON object per line, used by the streaming log output.
func emitRaw(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintln(os.Stdout, string(b))
}

type validationResult struct {
	Type         string          `json:"type"`
	Valid        bool            `json:"valid"`
	ScheduleType string          `json:"scheduleType,omitempty"`
	Errors       []config.Issue  `json:"errors"`
	Warnings     []config.Issue  `json:"warnings"`
	NextRuns     []string        `json:"nextRuns,omitempty"`
	Jobs         []jobValidation `json:"jobs,omitempty"`
}

type jobValidation struct {
	ID       string   `json:"id"`
	NextRuns []string `json:"nextRuns"`
}

func validateCmd(g *globals) *cobra.Command {
	var expr string
	var next int
	var tz string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a schedule expression or the whole jobs.yaml",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			id := "cli:validate"
			if next <= 0 {
				next = 5
			}
			if expr != "" {
				return validateExpr(g, id, expr, tz, next)
			}
			return validateFile(g, id, next)
		},
	}
	cmd.Flags().StringVar(&expr, "schedule", "", "a cron expression, @descriptor, duration, or RFC 3339 instant")
	cmd.Flags().StringVar(&tz, "timezone", "local", "IANA timezone for --schedule")
	cmd.Flags().IntVar(&next, "next", 5, "how many upcoming fire times to print")
	return cmd
}

// parseScheduleExpr delegates the --schedule shape rules to the same parser used when the
// expression is written, so validation cannot assign a different schedule form.
func parseScheduleExpr(expr string, loc *time.Location) (schedule.Spec, string, error) {
	return schedule.ParseExpr(expr, time.Now(), loc)
}

func validateExpr(g *globals, id, expr, tz string, next int) error {
	loc, err := schedule.LoadLocation(tz)
	if err != nil {
		return failure(id, "usage", err.Error(), ExitUsage, nil)
	}
	spec, kind, err := parseScheduleExpr(expr, loc)
	if err != nil {
		return failure(id, "config_invalid", err.Error(), ExitError, nil)
	}
	sch, err := schedule.Parse(spec)
	if err != nil {
		return failure(id, "config_invalid", err.Error(), ExitError, nil)
	}
	res := validationResult{Type: "validation", Valid: true, ScheduleType: kind,
		Errors: []config.Issue{}, Warnings: []config.Issue{}}
	for _, t := range sch.NextN(time.Now(), next, 0) {
		res.NextRuns = append(res.NextRuns, t.Format(time.RFC3339))
	}
	emit(os.Stdout, g, Envelope{ID: id, Result: res})
	return nil
}

func validateFile(g *globals, id string, next int) error {
	roots, err := g.roots()
	if err != nil {
		return failure(id, "io_error", err.Error(), ExitError, nil)
	}
	loaded, errs := config.Load(roots.JobsFile())
	if len(errs) > 0 {
		res := validationResult{Type: "validation", Valid: false, Errors: errs, Warnings: []config.Issue{}}
		emit(os.Stdout, g, Envelope{ID: id, Result: res})
		return failure(id, "config_invalid",
			fmt.Sprintf("%s has %d error(s)", roots.JobsFile(), len(errs)), ExitError, errs)
	}
	res := validationResult{Type: "validation", Valid: true, Errors: []config.Issue{}, Warnings: loaded.Warnings}
	if res.Warnings == nil {
		res.Warnings = []config.Issue{}
	}
	for _, j := range loaded.Jobs {
		sch, err := schedule.FromResolved(j.Schedule)
		if err != nil {
			continue
		}
		jv := jobValidation{ID: j.ID}
		for _, t := range sch.NextN(time.Now(), next, time.Duration(j.Schedule.JitterSec)*time.Second) {
			jv.NextRuns = append(jv.NextRuns, t.Format(time.RFC3339))
		}
		res.Jobs = append(res.Jobs, jv)
	}
	emit(os.Stdout, g, Envelope{ID: id, Result: res})
	return nil
}

// runResult is the `run` payload of docs/spec/05-cli.md §2.
type runResult struct {
	Type string     `json:"type"`
	Run  *model.Run `json:"run"`
}

// ------------------------------------------------------------------ run-once

func runOnceCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "run-once <job-id>",
		Short: "Execute exactly one run of a job in this process",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			id := "cli:run-once"
			roots, err := g.roots()
			if err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}
			loaded, errs := config.Load(roots.JobsFile())
			if len(errs) > 0 {
				return failure(id, "config_invalid",
					fmt.Sprintf("%s has %d error(s)", roots.JobsFile(), len(errs)), ExitError, errs)
			}
			job, ok := loaded.Job(args[0])
			if !ok {
				return failure(id, "job_not_found", fmt.Sprintf("no job with id %q", args[0]), ExitError, nil)
			}
			st := store.New(roots)
			opt := runner.Options{Trigger: triggerFromEnv()}
			run, err := runner.RunOnce(context.Background(), st, job, opt)
			if err != nil {
				return failure(id, "io_error", err.Error(), ExitError, nil)
			}

			// Exactly one of result or error, never both (docs/spec/05-cli.md §2).
			switch run.Status {
			case model.StatusSuccess, model.StatusNoOp, model.StatusSkipped:
				emit(os.Stdout, g, Envelope{ID: id, Result: runResult{Type: "run", Run: run}})
				return nil
			case model.StatusBlocked:
				return &fail{env: Envelope{ID: id, Error: &Error{Code: "agent_blocked",
					Message: "the run is blocked and needs a human", Details: runResult{Type: "run", Run: run}}},
					code: ExitBlocked}
			default:
				return &fail{env: Envelope{ID: id, Error: &Error{Code: "run_failed",
					Message: fmt.Sprintf("run %s finished %s", run.RunID, run.Status),
					Details: runResult{Type: "run", Run: run}}}, code: ExitError}
			}
		},
	}
}

// triggerFromEnv lets a generated OS-scheduler entry mark its runs as scheduled without
// extending the published run-once surface (docs/spec/05-cli.md §1.2).
func triggerFromEnv() model.Trigger {
	switch v := model.Trigger(os.Getenv("HERDR_CRON_TRIGGER")); v {
	case model.TriggerScheduler, model.TriggerManual, model.TriggerCatchup,
		model.TriggerRetry, model.TriggerStartup:
		return v
	default:
		return model.TriggerManual
	}
}
