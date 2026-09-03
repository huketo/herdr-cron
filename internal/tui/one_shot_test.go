package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/huketo/herdr-cron/internal/store"
)

func TestCompletedOneShotIsShownWhereNextRunWouldAppear(t *testing.T) {
	roots := testRoots(t)
	if err := roots.EnsureState(); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	jobs := fmt.Sprintf(`version: 1
jobs:
  - id: once-done
    schedule: { at: %q }
    kind: shell
    shell: { command: "true" }
`, at.Format(time.RFC3339))
	if err := os.WriteFile(roots.JobsFile(), []byte(jobs), 0o644); err != nil {
		t.Fatal(err)
	}
	state := &store.State{Jobs: map[string]*store.JobState{
		"once-done": {LastScheduledAt: &at},
	}}
	if err := store.New(roots).SaveState(state); err != nil {
		t.Fatal(err)
	}

	snap := Load(roots)
	job, ok := snap.Job("once-done")
	if !ok {
		t.Fatal("one-shot job was not loaded")
	}
	if !job.Completed {
		t.Fatal("completed one-shot was not marked completed")
	}
	if !job.NextRunAt.IsZero() {
		t.Fatalf("NextRunAt = %s, want no remaining occurrence", job.NextRunAt)
	}

	m := New(roots)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	m = asModel(t, updated)
	if content := m.View().Content; !strings.Contains(content, "completed") {
		t.Errorf("job list does not replace the empty next-run cell with completed:\n%s", content)
	}
	if detail := detailText(job); !strings.Contains(detail, "next") || !strings.Contains(detail, "completed") {
		t.Errorf("job detail does not show completed in the next-run field:\n%s", detail)
	}
}
