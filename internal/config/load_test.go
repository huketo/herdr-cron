package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "jobs.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const validJob = `version: 1
jobs:
  - id: smoke
    schedule:
      cron: "*/5 * * * *"
    kind: shell
    shell:
      command: "true"
`

// An unknown key must be an error, not a warning: a typo in catchup_window silently
// disabling catch-up is the failure mode this rule exists to prevent
// (docs/spec/03-job-model.md §1.2).
func TestUnknownKeyIsAnError(t *testing.T) {
	p := write(t, `version: 1
jobs:
  - id: smoke
    schedule:
      cron: "*/5 * * * *"
      catchup_windwo: 6h
    kind: shell
    shell:
      command: "true"
`)
	if _, errs := Load(p); len(errs) == 0 {
		t.Fatal("expected a load error for the misspelled key")
	}
}

// A bare number is ambiguous and must be rejected (docs/spec/03-job-model.md §1.2).
func TestBareNumberDurationIsRejected(t *testing.T) {
	p := write(t, strings.Replace(validJob, `      command: "true"`, "      command: \"true\"\n    timeout: 30", 1))
	_, errs := Load(p)
	if len(errs) == 0 {
		t.Fatal("expected a load error for `timeout: 30`")
	}
	if !strings.Contains(errs[0].Message, "ambiguous") {
		t.Fatalf("error should explain the ambiguity, got %q", errs[0].Message)
	}
}

func TestIDPatternAndDuplicates(t *testing.T) {
	for _, tc := range []struct{ name, id string }{
		{"uppercase", "Smoke"},
		{"leading dash", "-smoke"},
		{"space", "smo ke"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := write(t, strings.Replace(validJob, "id: smoke", "id: "+tc.id, 1))
			if _, errs := Load(p); len(errs) == 0 {
				t.Fatalf("expected id %q to be rejected", tc.id)
			}
		})
	}

	p := write(t, validJob+strings.TrimPrefix(validJob, "version: 1\njobs:\n"))
	_, errs := Load(p)
	if len(errs) == 0 {
		t.Fatal("expected duplicate ids to be rejected")
	}
}

// Defaults resolve, and a job-level value wins over the file default
// (docs/spec/03-job-model.md §1.2).
func TestDefaultsAndOverrides(t *testing.T) {
	p := write(t, `version: 1
defaults:
  timeout: 5m
  concurrency: allow
jobs:
  - id: a
    schedule: { every: 30m }
    kind: shell
    shell: { command: "true" }
  - id: b
    schedule: { every: 30m }
    kind: shell
    shell: { command: "true" }
    timeout: 90s
`)
	loaded, errs := Load(p)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	a, _ := loaded.Job("a")
	b, _ := loaded.Job("b")
	if a.TimeoutSec != 300 {
		t.Errorf("a.timeoutSec = %d, want 300 from defaults", a.TimeoutSec)
	}
	if b.TimeoutSec != 90 {
		t.Errorf("b.timeoutSec = %d, want 90 from the job", b.TimeoutSec)
	}
	if a.Concurrency != "allow" {
		t.Errorf("a.concurrency = %q, want allow from defaults", a.Concurrency)
	}
	if a.Limits.MaxConsecutiveFailures != 3 {
		t.Errorf("a.limits.maxConsecutiveFailures = %d, want the built-in 3", a.Limits.MaxConsecutiveFailures)
	}
	if a.Limits.MaxRunsPerDay != 0 {
		t.Errorf("shell jobs default to unlimited daily runs, got %d", a.Limits.MaxRunsPerDay)
	}
}

// Jitter is deterministic in the job id, which is what keeps next-run predictions stable
// (docs/spec/03-job-model.md §2.1).
func TestJitterIsDeterministicPerID(t *testing.T) {
	body := `version: 1
jobs:
  - id: alpha
    schedule: { cron: "0 9 * * *" }
    kind: shell
    shell: { command: "true" }
  - id: beta
    schedule: { cron: "0 9 * * *" }
    kind: shell
    shell: { command: "true" }
`
	first, errs := Load(write(t, body))
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	second, _ := Load(write(t, body))

	a1, _ := first.Job("alpha")
	a2, _ := second.Job("alpha")
	b1, _ := first.Job("beta")

	if a1.Schedule.JitterSec != a2.Schedule.JitterSec {
		t.Errorf("jitter is not stable across loads: %d vs %d", a1.Schedule.JitterSec, a2.Schedule.JitterSec)
	}
	if a1.Schedule.JitterSec == b1.Schedule.JitterSec {
		t.Errorf("two ids produced the same jitter %d; the offset must vary per id", a1.Schedule.JitterSec)
	}
	if a1.Schedule.JitterSec < 0 || a1.Schedule.JitterSec > 1800 {
		t.Errorf("jitter %d is outside the documented 0..30m bound", a1.Schedule.JitterSec)
	}
}

func TestKindPayloadMismatch(t *testing.T) {
	p := write(t, `version: 1
jobs:
  - id: smoke
    schedule: { every: 30m }
    kind: agent
    shell: { command: "true" }
`)
	if _, errs := Load(p); len(errs) == 0 {
		t.Fatal("kind: agent with a shell payload must be rejected")
	}
}
