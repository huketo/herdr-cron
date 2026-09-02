package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/huketo/herdr-cron/internal/daemon"
	"github.com/huketo/herdr-cron/internal/paths"
)

// TestJobListAndStatusAgreeOnLiveness is the regression guard for a lie an
// agent acts on.
//
// `job list` used to carry a hardcoded `"daemon": {"status": "stopped"}` — the
// field was in the payload shape of docs/spec/05-cli.md §3.1 but was never
// wired to a liveness check. So an agent that read `job list` to decide whether
// `job run` would be claimed was told there was no daemon while one was running
// and answering triggers, and the documented recovery
// (`service install --driver daemon`) was the wrong move. `status` reported it
// correctly the whole time, which is what makes this a disagreement between two
// paths rather than one broken path.
//
// This drives the real commands and parses their envelopes, because the bug
// lived at the callsite: asserting on the helper alone still passed with the
// hardcoded literal in place.
func TestJobListAndStatusAgreeOnLiveness(t *testing.T) {
	tests := []struct {
		name       string
		heartbeat  bool
		age        time.Duration
		wantStatus string
	}{
		{name: "no heartbeat at all", heartbeat: false, wantStatus: "stopped"},
		// Fresh heartbeat but no lock held: a kill -9 leaves exactly this, and
		// it is why liveness is heartbeat AND lock (docs/spec/04-storage.md §7).
		{name: "fresh heartbeat with no lock", heartbeat: true, age: time.Second, wantStatus: "stale"},
		{name: "stale heartbeat", heartbeat: true, age: 5 * time.Minute, wantStatus: "stale"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			roots := testRoots(t)
			if tc.heartbeat {
				writeHeartbeat(t, roots, tc.age)
			}

			fromList := runForDaemonBlock(t, "job", "list")
			fromStatus := runForDaemonBlock(t, "status")

			if fromStatus["status"] != tc.wantStatus {
				t.Errorf("status reported %v, want %q", fromStatus["status"], tc.wantStatus)
			}
			if fromList["status"] != fromStatus["status"] {
				t.Errorf("`job list` reports daemon %v while `status` reports %v; "+
					"both payloads must come from the same liveness check",
					fromList["status"], fromStatus["status"])
			}
			if fromList["pid"] != fromStatus["pid"] {
				t.Errorf("`job list` reports pid %v while `status` reports %v",
					fromList["pid"], fromStatus["pid"])
			}
		})
	}
}

// runForDaemonBlock executes one command and returns its `result.daemon` object.
//
// The command bodies write to os.Stdout directly rather than to
// cmd.OutOrStdout(), so the stream is swapped for a pipe here instead of being
// injected. That is a property of the code under test, not a preference.
func runForDaemonBlock(t *testing.T, args ...string) map[string]any {
	t.Helper()

	saved := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- b
	}()

	code := Execute(args, BuildInfo{Version: "0.0.0-test"})

	os.Stdout = saved
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	out := <-done

	if code != ExitOK {
		t.Fatalf("`%v` exited %d: %s", args, code, out)
	}

	var env struct {
		Result struct {
			Daemon map[string]any `json:"daemon"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("`%v` produced unparseable output %q: %v", args, out, err)
	}
	if env.Result.Daemon == nil {
		t.Fatalf("`%v` carries no result.daemon block; the payload shape of 05-cli.md §3.1 requires one", args)
	}
	return env.Result.Daemon
}

// testRoots points every root at a temp dir with a valid single-job jobs.yaml,
// so a developer's real daemon and real schedule cannot make these tests pass
// or fail.
func testRoots(t *testing.T) paths.Roots {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HERDR_CRON_HOME", home)
	// Set explicitly so an ambient plugin environment cannot reach in; the
	// variable is ignored by Resolve, and internal/paths guards that.
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "")

	roots, err := paths.Resolve(paths.Overrides{})
	if err != nil {
		t.Fatalf("resolve roots: %v", err)
	}
	if err := roots.EnsureState(); err != nil {
		t.Fatalf("ensure state: %v", err)
	}
	if err := os.MkdirAll(roots.Config, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	jobs := "version: 1\njobs:\n  - id: probe\n    schedule: { every: 1h }\n    kind: shell\n    shell: { command: \"true\" }\n"
	if err := os.WriteFile(roots.JobsFile(), []byte(jobs), 0o644); err != nil {
		t.Fatalf("write jobs.yaml: %v", err)
	}
	return roots
}

// writeHeartbeat plants a daemon.json aged by the given amount. No lock is
// taken, which is the kill -9 shape.
func writeHeartbeat(t *testing.T, roots paths.Roots, age time.Duration) {
	t.Helper()

	hb := daemon.Heartbeat{
		PID:         4242,
		StartedAt:   time.Now().Add(-age - time.Minute),
		HeartbeatAt: time.Now().Add(-age),
		Driver:      "daemon",
		Version:     "0.0.0-test",
	}
	b, err := json.Marshal(hb)
	if err != nil {
		t.Fatalf("marshal heartbeat: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(roots.DaemonFile()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(roots.DaemonFile(), b, 0o644); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
}
