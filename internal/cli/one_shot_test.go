package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/huketo/herdr-cron/internal/config"
	"github.com/huketo/herdr-cron/internal/schedule"
	"github.com/huketo/herdr-cron/internal/store"
)

func TestParseScheduleExprTreatsLeadingPlusAsRelativeInstant(t *testing.T) {
	before := time.Now()
	spec, form, err := parseScheduleExpr("+2h", time.UTC)
	if err != nil {
		t.Fatalf("parseScheduleExpr: %v", err)
	}
	if form != schedule.FormAt {
		t.Fatalf("form = %q, want %q", form, schedule.FormAt)
	}
	if spec.Every != 0 {
		t.Fatalf("Every = %s; +2h must not become a repeating schedule", spec.Every)
	}
	instant, err := time.Parse(time.RFC3339, spec.At)
	if err != nil {
		t.Fatalf("At = %q, want an absolute RFC 3339 instant: %v", spec.At, err)
	}
	if earliest := before.Add(2 * time.Hour).Truncate(time.Second); instant.Before(earliest) {
		t.Errorf("At = %s, earlier than %s", instant, earliest)
	}
	if latest := time.Now().Add(2 * time.Hour).Truncate(time.Second); instant.After(latest) {
		t.Errorf("At = %s, later than %s", instant, latest)
	}
}

func TestJobAddRejectsPastOneShotAsUsageError(t *testing.T) {
	_ = testRoots(t)
	past := time.Now().Add(-400 * 24 * time.Hour).Truncate(time.Second).Format(time.RFC3339)

	code, out := runCLICaptured(t, "job", "add", "--id", "past", "--command", "true", "--schedule", past)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d; output: %s", code, ExitUsage, out)
	}
	var env Envelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("decode response %q: %v", out, err)
	}
	if env.Error == nil || env.Error.Code != "usage" {
		t.Fatalf("error = %+v, want code usage", env.Error)
	}
	if !strings.Contains(env.Error.Message, past) || !strings.Contains(env.Error.Message, "now") || !strings.Contains(env.Error.Message, "+2h") {
		t.Errorf("message = %q, want the scheduled instant, current time, and relative syntax", env.Error.Message)
	}
}

func TestJobAddStoresRelativeOneShotAsAbsoluteInstant(t *testing.T) {
	roots := testRoots(t)
	before := time.Now()

	code, out := runCLICaptured(t, "job", "add", "--id", "relative", "--command", "true", "--schedule", "+2h")
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d; output: %s", code, ExitOK, out)
	}
	body, err := os.ReadFile(roots.JobsFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "+2h") {
		t.Fatalf("jobs.yaml retained the relative expression:\n%s", body)
	}
	loaded, issues := config.Load(roots.JobsFile())
	if len(issues) > 0 {
		t.Fatalf("load jobs.yaml: %v", issues)
	}
	job, ok := loaded.Job("relative")
	if !ok {
		t.Fatal("relative job was not written")
	}
	instant, ok := job.Schedule.Instant()
	if !ok {
		t.Fatalf("schedule = %+v, want a one-shot instant", job.Schedule)
	}
	if instant.Before(before.Add(2*time.Hour).Truncate(time.Second)) || instant.After(time.Now().Add(2*time.Hour).Truncate(time.Second)) {
		t.Errorf("stored instant = %s, want approximately two hours from now", instant)
	}
}

func TestJobListCompletedFieldDistinguishesOneShotFromRepeating(t *testing.T) {
	roots := testRoots(t)
	doneAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	pendingAt := time.Now().Add(time.Hour).Truncate(time.Second)
	jobs := fmt.Sprintf(`version: 1
jobs:
  - id: once-done
    schedule: { at: %q }
    kind: shell
    shell: { command: "true" }
  - id: once-pending
    schedule: { at: %q }
    kind: shell
    shell: { command: "true" }
  - id: repeating
    schedule: { every: 1h }
    kind: shell
    shell: { command: "true" }
`, doneAt.Format(time.RFC3339), pendingAt.Format(time.RFC3339))
	if err := os.WriteFile(roots.JobsFile(), []byte(jobs), 0o644); err != nil {
		t.Fatal(err)
	}
	state := &store.State{Jobs: map[string]*store.JobState{
		"once-done": {LastScheduledAt: &doneAt},
	}}
	if err := store.New(roots).SaveState(state); err != nil {
		t.Fatal(err)
	}

	code, out := runCLICaptured(t, "job", "list")
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d; output: %s", code, ExitOK, out)
	}
	var env struct {
		Result struct {
			Jobs []map[string]any `json:"jobs"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("decode response %q: %v", out, err)
	}
	byID := make(map[string]map[string]any, len(env.Result.Jobs))
	for _, job := range env.Result.Jobs {
		id, _ := job["id"].(string)
		byID[id] = job
	}
	if got := byID["once-done"]["completed"]; got != true {
		t.Errorf("once-done completed = %v, want true", got)
	}
	if got := byID["once-pending"]["completed"]; got != false {
		t.Errorf("once-pending completed = %v, want false", got)
	}
	if _, ok := byID["repeating"]["completed"]; ok {
		t.Errorf("repeating response unexpectedly contains completed: %v", byID["repeating"])
	}

	code, out = runCLICaptured(t, "job", "get", "once-done")
	if code != ExitOK {
		t.Fatalf("job get exit code = %d, want %d; output: %s", code, ExitOK, out)
	}
	var got struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode job get response %q: %v", out, err)
	}
	if got.Result["completed"] != true {
		t.Errorf("job get completed = %v, want true", got.Result["completed"])
	}
}

func runCLICaptured(t *testing.T, args ...string) (int, []byte) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		t.Fatal(err)
	}

	savedOut, savedErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	outDone := make(chan []byte, 1)
	errDone := make(chan struct{}, 1)
	go func() {
		b, _ := io.ReadAll(outR)
		outDone <- b
	}()
	go func() {
		_, _ = io.Copy(io.Discard, errR)
		errDone <- struct{}{}
	}()

	code := Execute(args, BuildInfo{Version: "0.0.0-test"})
	os.Stdout, os.Stderr = savedOut, savedErr
	_ = outW.Close()
	_ = errW.Close()
	out := <-outDone
	<-errDone
	_ = outR.Close()
	_ = errR.Close()
	return code, out
}
