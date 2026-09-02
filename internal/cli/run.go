package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/huketo/herdr-cron/internal/config"
	"github.com/huketo/herdr-cron/internal/model"
	"github.com/huketo/herdr-cron/internal/paths"
	"github.com/huketo/herdr-cron/internal/store"
)

type runListResult struct {
	Type string       `json:"type"`
	Runs []*model.Run `json:"runs"`
}

type logLine struct {
	Type  string `json:"type"`
	RunID string `json:"runId"`
	Line  string `json:"line"`
}

func runCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{Use: "run", Short: "Inspect run history"}
	cmd.AddCommand(runListCmd(g), runGetCmd(g), runLogsCmd(g))
	return cmd
}

// allRuns merges every job's history. With the default retention this is a few hundred
// kilobytes, so a linear scan is the right implementation (docs/spec/04-storage.md §5).
func allRuns(st *store.Store, loaded *config.Loaded, jobID string) ([]*model.Run, error) {
	ids := []string{}
	if jobID != "" {
		ids = append(ids, jobID)
	} else {
		seen := map[string]bool{}
		for _, j := range loaded.Jobs {
			ids = append(ids, j.ID)
			seen[j.ID] = true
		}
		// History outlives its definition unless it was purged, so include orphans.
		entries, err := os.ReadDir(filepath.Join(st.Roots().State, "runs"))
		if err == nil {
			for _, e := range entries {
				name := e.Name()
				if !strings.HasSuffix(name, ".jsonl") {
					continue
				}
				id := strings.TrimSuffix(name, ".jsonl")
				if !seen[id] {
					ids = append(ids, id)
				}
			}
		}
	}

	var out []*model.Run
	for _, id := range ids {
		runs, err := st.Runs(id)
		if err != nil {
			return nil, err
		}
		out = append(out, runs...)
	}
	sort.SliceStable(out, func(i, k int) bool {
		return runOrder(out[i]).Before(runOrder(out[k]))
	})
	return out, nil
}

func runOrder(r *model.Run) time.Time {
	switch {
	case r.StartedAt != nil:
		return *r.StartedAt
	case r.ScheduledAt != nil:
		return *r.ScheduledAt
	case r.FinishedAt != nil:
		return *r.FinishedAt
	default:
		return time.Time{}
	}
}

func runListCmd(g *globals) *cobra.Command {
	var jobID, status, since string
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List runs, newest last",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			id := "cli:run:list"
			loaded, st, _, _, err := g.loadAll(id)
			if err != nil {
				return err
			}
			runs, rerr := allRuns(st, loaded, jobID)
			if rerr != nil {
				return failure(id, "io_error", rerr.Error(), ExitError, nil)
			}
			var cutoff time.Time
			if since != "" {
				cutoff, err = parseSince(since)
				if err != nil {
					return failure(id, "usage", err.Error(), ExitUsage, nil)
				}
			}
			out := []*model.Run{}
			for _, r := range runs {
				if !matchStatus(status, r.Status) {
					continue
				}
				if !cutoff.IsZero() && runOrder(r).Before(cutoff) {
					continue
				}
				out = append(out, r)
			}
			if limit > 0 {
				out = tail(out, limit)
			}
			emit(os.Stdout, g, Envelope{ID: id, Result: runListResult{Type: "run_list", Runs: out}})
			return nil
		},
	}
	cmd.Flags().StringVar(&jobID, "job", "", "only this job's runs")
	cmd.Flags().StringVar(&status, "status", "all", "ok | failed | running | all, or a literal status")
	cmd.Flags().IntVar(&limit, "limit", 50, "keep only the newest N")
	cmd.Flags().StringVar(&since, "since", "", "RFC 3339 instant or a duration ago, e.g. 24h")
	return cmd
}

// matchStatus accepts the three convenience groups plus any literal status.
func matchStatus(filter string, s model.Status) bool {
	switch filter {
	case "", "all":
		return true
	case "ok":
		return s == model.StatusSuccess || s == model.StatusNoOp
	case "failed":
		return s == model.StatusFailure || s == model.StatusTimeout ||
			s == model.StatusBlocked || s == model.StatusCancelled
	case "running":
		return s == model.StatusRunning
	default:
		return string(s) == filter
	}
}

func parseSince(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return time.Time{}, fmt.Errorf("--since %q is neither an RFC 3339 instant nor a duration", v)
	}
	return time.Now().Add(-d), nil
}

func findRun(st *store.Store, loaded *config.Loaded, runID string) (*model.Run, error) {
	runs, err := allRuns(st, loaded, jobIDOf(runID))
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		if r.RunID == runID {
			return r, nil
		}
	}
	return nil, nil
}

// jobIDOf recovers the job id from a run id, which is <jobId>-<timestamp>[-m][-rN].
// A wrong guess only costs a wider scan, never a wrong answer.
func jobIDOf(runID string) string {
	i := strings.LastIndex(runID, "-2")
	if i <= 0 {
		return ""
	}
	return runID[:i]
}

func runGetCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "get <run-id>",
		Short: "Show one run record",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			id := "cli:run:get"
			loaded, st, _, _, err := g.loadAll(id)
			if err != nil {
				return err
			}
			r, ferr := findRun(st, loaded, args[0])
			if ferr != nil {
				return failure(id, "io_error", ferr.Error(), ExitError, nil)
			}
			if r == nil {
				return failure(id, "run_not_found", fmt.Sprintf("no run with id %q", args[0]), ExitError, nil)
			}
			emit(os.Stdout, g, Envelope{ID: id, Result: runResult{Type: "run", Run: r}})
			return nil
		},
	}
}

func runLogsCmd(g *globals) *cobra.Command {
	var tailN int
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs <run-id>",
		Short: "Print a run's captured output",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			id := "cli:run:logs"
			loaded, st, _, _, err := g.loadAll(id)
			if err != nil {
				return err
			}
			r, ferr := findRun(st, loaded, args[0])
			if ferr != nil {
				return failure(id, "io_error", ferr.Error(), ExitError, nil)
			}
			if r == nil {
				return failure(id, "run_not_found", fmt.Sprintf("no run with id %q", args[0]), ExitError, nil)
			}
			path := logPath(st.Roots(), r)
			f, oerr := os.Open(path)
			if oerr != nil {
				return failure(id, "io_error", fmt.Sprintf("no log at %s", path), ExitError, nil)
			}
			defer func() { _ = f.Close() }()

			lines, rerr := readLines(f)
			if rerr != nil {
				return failure(id, "io_error", rerr.Error(), ExitError, nil)
			}
			if tailN > 0 {
				lines = tail(lines, tailN)
			}
			// Raw text, not an envelope: a log is a stream (docs/spec/05-cli.md §3.2).
			for _, l := range lines {
				printLogLine(g, r.RunID, l)
			}
			if follow {
				return followLog(g, f, st, loaded, r.RunID)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&tailN, "tail", 0, "print only the last N lines")
	cmd.Flags().BoolVar(&follow, "follow", false, "keep printing as the run writes")
	return cmd
}

func logPath(roots paths.Roots, r *model.Run) string {
	if r.LogPath == "" {
		return roots.LogFile(r.JobID, r.RunID)
	}
	return filepath.Join(roots.State, filepath.FromSlash(r.LogPath))
}

func readLines(f io.Reader) ([]string, error) {
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	out := []string{}
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out, sc.Err()
}

func printLogLine(g *globals, runID, line string) {
	if g.output == "text" {
		fmt.Fprintln(os.Stdout, line)
		return
	}
	emitRaw(logLine{Type: "log_line", RunID: runID, Line: line})
}

// followLog polls the open file, which is the same 100 ms cadence the trigger protocol
// uses; there is no socket to be notified over (docs/spec/04-storage.md §8).
func followLog(g *globals, f *os.File, st *store.Store, loaded *config.Loaded, runID string) error {
	for {
		lines, err := readLines(f)
		if err != nil {
			return err
		}
		for _, l := range lines {
			printLogLine(g, runID, l)
		}
		// The run's terminal record is what ends the follow, so it must be re-read; the
		// copy we started from is a snapshot.
		r, err := findRun(st, loaded, runID)
		if err != nil {
			return err
		}
		if r != nil && r.Status.Terminal() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}
