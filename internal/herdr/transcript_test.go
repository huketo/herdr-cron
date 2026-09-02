package herdr

import (
	"strings"
	"testing"
)

// The transcript echoes the prompt, and a no-op marker is normally named inside its own
// prompt, so a substring search would match the echo on every run. This is the exact
// transcript captured from a real headless probe
// (docs/spec/07-herdr-integration.md §4.2).
const probeTranscript = `
 ▐▛███▛█   Claude Code v2.1.251
▝▜██████▀  Opus 5 (1M context) with high effort · Claude Team
  ▝▝ ▝▝    ~/huketo/jjalcloud


❯ Reply with exactly the single word HEADLESS-OK and nothing else. Do not use any tools.

● HEADLESS-OK

✻ Sautéed for 1s · done 오전 10:46
`

func TestFinalAssistantTextFromTheProbeTranscript(t *testing.T) {
	if got := FinalAssistantText(probeTranscript, "claude"); got != "HEADLESS-OK" {
		t.Fatalf("FinalAssistantText = %q, want %q", got, "HEADLESS-OK")
	}
}

// The marker appears in the prompt echo. Extraction must not treat that as the answer.
func TestPromptEchoIsNotTheAnswer(t *testing.T) {
	transcript := `❯ Audit the repo. If everything is current, reply with exactly HEARTBEAT_OK and stop.

● I found three outdated dependencies and opened an issue.

✻ Thought for 4s · done`
	got := FinalAssistantText(transcript, "claude")
	if got == "HEARTBEAT_OK" {
		t.Fatal("the prompt echo was mistaken for the assistant's answer")
	}
	if got != "I found three outdated dependencies and opened an issue." {
		t.Fatalf("FinalAssistantText = %q", got)
	}
}

func TestMultiLineAndMultiTurnBlocks(t *testing.T) {
	transcript := `❯ first question

● first answer

❯ second question

● second answer, line one
second answer, line two

⏵⏵ auto mode on`
	want := "second answer, line one\nsecond answer, line two"
	if got := FinalAssistantText(transcript, "claude"); got != want {
		t.Fatalf("FinalAssistantText = %q, want %q", got, want)
	}
}

func TestNoAssistantBlockFallsBackAndEmptyStaysEmpty(t *testing.T) {
	if got := FinalAssistantText("some plain output\nlast line", "codex"); got != "last line" {
		t.Errorf("fallback = %q, want the last non-status line", got)
	}
	if got := FinalAssistantText("", "claude"); got != "" {
		t.Errorf("empty transcript produced %q", got)
	}
	if got := FinalAssistantText("❯ only the user spoke\n\n✻ done", "claude"); got != "" {
		t.Errorf("a user-only transcript produced %q, want \"\"", got)
	}
}

// Herdr names must match [a-z][a-z0-9_-]{0,31}; job ids may be 128 bytes and contain '.'.
func TestAgentNameFitsTheHerdrGrammar(t *testing.T) {
	cases := []string{
		"nightly-deps",
		"repo.audit.weekly",
		"a-very-long-job-identifier-that-goes-well-past-the-thirty-two-character-limit",
		"x",
	}
	for _, id := range cases {
		got := AgentName(id, "run-1")
		if !validHerdrName(got) {
			t.Errorf("AgentName(%q) = %q, which does not match [a-z][a-z0-9_-]{0,31}", id, got)
		}
		if got != AgentName(id, "run-2") {
			t.Errorf("AgentName(%q) is not deterministic across runs", id)
		}
	}
	if got := AgentName("nightly-deps", "r"); got != "cron-nightly-deps" {
		t.Errorf("AgentName = %q, want cron-nightly-deps", got)
	}
	a := AgentNameForRun("nightly-deps", "run-a")
	b := AgentNameForRun("nightly-deps", "run-b")
	if a == b {
		t.Error("the collision form must differ per run")
	}
	if !validHerdrName(a) {
		t.Errorf("collision name %q does not match the grammar", a)
	}
}

func validHerdrName(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	if s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

func TestVersionGateOrdering(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0.8.2", "0.8.2", 0},
		{"0.8.1", "0.8.2", -1},
		{"0.9.0", "0.8.2", 1},
		{"0.10.0", "0.9.9", 1},
		{"1.0.0-rc1", "0.8.2", 1},
		{"0.8", "0.8.2", -1},
	} {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// A real `recent-unwrapped` snapshot of claude's full-screen UI, captured from an
// unattended run on 2026-09-02. The footer contains its own `●` (`● high · /effort`), so a
// naive backwards scan for the assistant marker returns the status bar instead of the
// answer. The empty input prompt is the boundary.
const fullScreenTranscript = "\n ▐▛███▛█   Claude Code v2.1.258\n" +
	"▝▜██████▀  Opus 5 (1M context) with high effort · Claude Team\n" +
	"  ▝▝ ▝▝    ~/huketo/jjalcloud\n\n" +
	"  Fable 5.1 writes better code and reports progress on long tasks.\n\n" +
	"❯ You are being run by herdr-cron on a schedule. There is no human watching this session.\n" +
	"  Reply with exactly the single word HEADLESS-OK and nothing else. Do not use any tools.\n\n" +
	"● Usage limit reached · continuing automatically at 2pm · esc or type to cancel\n\n" +
	"✻ Brewed for 0s · done 오후 1:02\n\n\n\n" +
	"                                                            ● high · /effort\n" +
	"──────────────────────────────────────────────────────────────────────────\n" +
	"❯\u00a0\n" +
	"──────────────────────────────────────────────────────────────────────────\n" +
	"  ⚠ Usage limit reached · continuing automatically at 2pm · esc to cancel\n" +
	"  [Opus 5 (1M context)] │ jjalcloud git:(main)\n"

func TestFooterMarkerIsNotTheAnswer(t *testing.T) {
	got := FinalAssistantText(fullScreenTranscript, "claude")
	if strings.Contains(got, "/effort") {
		t.Fatalf("the status footer was mistaken for the answer: %q", got)
	}
	want := "Usage limit reached · continuing automatically at 2pm · esc or type to cancel"
	if got != want {
		t.Fatalf("FinalAssistantText = %q, want %q", got, want)
	}
}
