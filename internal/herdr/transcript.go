package herdr

import (
	"hash/fnv"
	"strings"
	"unicode"
)

// Assistant and user markers in a claude transcript, and the status-line prefixes that
// trail it (docs/spec/07-herdr-integration.md §4.2).
const (
	assistantMarker = '●' // U+25CF
	userMarker      = '❯' // U+276F
)

var statusRunes = map[rune]bool{'✻': true, '✳': true, '⏵': true, '·': true, '─': true, '⚠': true}

// FinalAssistantText extracts the last assistant block from a transcript, or "" when none
// can be located.
//
// A substring search for the marker is FORBIDDEN: the transcript echoes the prompt, and a
// no-op marker is normally named inside its own prompt, so strings.Contains would match
// the echo on every run (docs/spec/07-herdr-integration.md §4.2).
func FinalAssistantText(transcript, agentKind string) string {
	lines := strings.Split(strings.TrimRight(transcript, "\n"), "\n")

	// 0: cut the agent's own chrome. A `recent-unwrapped` snapshot of a full-screen TUI
	// ends with the input box and a status footer, and that footer contains its own `●`
	// (`● high · /effort` on claude), which a backwards scan would otherwise mistake for
	// the answer. The empty input prompt — a `❯` with nothing after it — is the boundary;
	// the echoed user prompt is a `❯` *with* text, so it survives the cut.
	for i := len(lines) - 1; i >= 0; i-- {
		if firstRune(lines[i]) == userMarker && isBlank(trimUserMarker(lines[i])) {
			lines = lines[:i]
			break
		}
	}

	// 1-2: drop trailing empty and status lines.
	end := len(lines)
	for end > 0 {
		l := strings.TrimRight(lines[end-1], " \t")
		if strings.TrimSpace(l) == "" || isStatusLine(l) {
			end--
			continue
		}
		break
	}
	lines = lines[:end]
	if len(lines) == 0 {
		return ""
	}

	// 3: scan backwards for the last assistant marker. The marker must sit at column 0:
	// claude's status footer carries its own `●` (`● high · /effort`) indented to the
	// right edge, and an indentation-tolerant scan returns that instead of the answer.
	start := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], string(assistantMarker)) {
			start = i
			break
		}
	}
	if start >= 0 {
		block := []string{strings.TrimSpace(trimMarker(lines[start]))}
		for i := start + 1; i < len(lines); i++ {
			l := lines[i]
			if strings.TrimSpace(l) == "" {
				break
			}
			r := firstRune(l)
			if r == userMarker || r == assistantMarker {
				break
			}
			block = append(block, strings.TrimSpace(l))
		}
		return strings.TrimSpace(strings.Join(block, "\n"))
	}

	// 4: no marker — the last non-empty, non-status line that is not the user's.
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if strings.TrimSpace(l) == "" || isStatusLine(l) || firstRune(l) == userMarker {
			continue
		}
		return strings.TrimSpace(l)
	}
	return ""
}

func firstRune(s string) rune {
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		return r
	}
	return 0
}

func trimMarker(s string) string {
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	s = strings.TrimPrefix(s, string(assistantMarker))
	return strings.TrimPrefix(s, " ")
}

func isStatusLine(s string) bool {
	return statusRunes[firstRune(s)]
}

// AgentName derives a Herdr agent alias from a job id. Herdr names must match
// [a-z][a-z0-9_-]{0,31}; job ids are up to 128 bytes and may contain '.', which that
// grammar forbids, so the derivation is lossy and deterministic
// (docs/spec/07-herdr-integration.md §3.1).
func AgentName(jobID, runID string) string {
	slug := slugify(jobID)
	if len(slug) <= 27 {
		return "cron-" + slug
	}
	return "cron-" + slug[:18] + "-" + hex8(jobID)
}

// AgentNameForRun is the collision form: deterministic per run rather than per job.
func AgentNameForRun(jobID, runID string) string {
	slug := slugify(jobID)
	if len(slug) > 18 {
		slug = slug[:18]
	}
	return "cron-" + slug + "-" + hex8(runID)
}

func slugify(v string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(v) {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			r = '-'
		}
		if r == '-' {
			if lastDash || b.Len() == 0 {
				continue
			}
			lastDash = true
		} else {
			lastDash = false
		}
		b.WriteRune(r)
	}
	return strings.Trim(b.String(), "-")
}

func hex8(v string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(v))
	const digits = "0123456789abcdef"
	n := uint32(h.Sum64())
	out := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		out[i] = digits[n&0xf]
		n >>= 4
	}
	return string(out)
}

// isBlank treats the no-break space claude pads its input box with as whitespace.
func isBlank(s string) bool {
	return strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == '\u00a0'
	}) == ""
}

func trimUserMarker(s string) string {
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	return strings.TrimPrefix(s, string(userMarker))
}
