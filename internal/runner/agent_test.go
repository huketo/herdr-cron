package runner

import (
	"strings"
	"testing"

	"github.com/huketo/herdr-cron/internal/model"
)

// no_op means "ran and correctly did nothing", and the comparison is exact against the
// extracted final assistant text — never a substring of the transcript, which echoes the
// prompt and therefore the marker (docs/spec/07-herdr-integration.md §4.2).
func TestNoOpRequiresAnExactMatch(t *testing.T) {
	payload := model.AgentPayload{NoOpMarker: "HEARTBEAT_OK"}
	transcript := "❯ reply with exactly HEARTBEAT_OK when nothing changed\n\n● HEARTBEAT_OK"

	if got := succeed(payload, "HEARTBEAT_OK", transcript); got.status != model.StatusNoOp {
		t.Errorf("exact match gave %s, want no_op", got.status)
	}
	if got := succeed(payload, "HEARTBEAT_OK and one more thing", transcript); got.status != model.StatusSuccess {
		t.Errorf("a longer answer gave %s, want success", got.status)
	}
	if got := succeed(payload, "", transcript); got.status != model.StatusSuccess {
		t.Errorf("an empty answer gave %s, want success", got.status)
	}

	// With no marker configured, a job is never promoted to no_op.
	if got := succeed(model.AgentPayload{}, "HEARTBEAT_OK", transcript); got.status != model.StatusSuccess {
		t.Errorf("no marker configured gave %s, want success", got.status)
	}
}

// The excerpt prefers the extracted answer and falls back to the transcript tail, so a run
// whose answer could not be located still records evidence.
func TestExcerptFallsBackToTheTranscript(t *testing.T) {
	if got := excerpt("the answer", "a long transcript"); got != "the answer" {
		t.Errorf("excerpt = %q, want the extracted answer", got)
	}
	if got := excerpt("   ", "tail of the transcript"); got != "tail of the transcript" {
		t.Errorf("excerpt = %q, want the transcript tail", got)
	}
	long := strings.Repeat("x", excerptBytes*2)
	if got := excerpt("", long); len(got) != excerptBytes {
		t.Errorf("excerpt kept %d bytes, want the last %d", len(got), excerptBytes)
	}
}

// The preamble is not optional: its absence is the documented cause of a scheduled agent
// stalling forever on a question (docs/spec/03-job-model.md §3.3).
func TestPreambleForbidsQuestions(t *testing.T) {
	for _, want := range []string{"no human watching", "Do not ask questions", "Do not wait for approval"} {
		if !strings.Contains(Preamble, want) {
			t.Errorf("the preamble no longer says %q", want)
		}
	}
}
