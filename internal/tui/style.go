package tui

import (
	"fmt"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/huketo/herdr-cron/internal/model"
)

// Every width and height is measured with lipgloss, never len(): a Hangul syllable is one
// rune, three bytes and two cells (docs/spec/06-tui.md §7.1).
var (
	styleHeader  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styleOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styleWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleBad     = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleInfo    = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	styleBorder  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240"))
	styleFocused = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("39"))
	styleButton  = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("238")).Padding(0, 1)
	styleDanger  = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("124")).Padding(0, 1)
)

// statusStyle maps every status of docs/spec/03-job-model.md §6 onto a colour.
func statusStyle(s model.Status) lipgloss.Style {
	switch s {
	case model.StatusSuccess:
		return styleOK
	case model.StatusNoOp:
		return styleInfo
	case model.StatusRunning:
		return styleInfo
	case model.StatusSkipped:
		return styleDim
	case model.StatusFailure, model.StatusTimeout:
		return styleBad
	case model.StatusBlocked:
		return styleWarn
	case model.StatusCancelled:
		return styleDim
	default:
		return styleDim
	}
}

// enabledGlyph is the leading column of the job list: enabled, disabled by override, or
// disabled by the circuit breaker.
func enabledGlyph(v JobView) string {
	switch {
	case v.AutoDisabled:
		return styleBad.Render("⊘")
	case !v.Enabled:
		return styleDim.Render("○")
	default:
		return styleOK.Render("●")
	}
}

func statusText(r *model.Run) string {
	if r == nil {
		return styleDim.Render("—")
	}
	label := string(r.Status)
	if r.Status == model.StatusNoOp {
		label = "no-op"
	}
	return statusStyle(r.Status).Render(label)
}

// countdown renders a next-run instant the way a human reads a scheduler.
func countdown(at time.Time) string {
	if at.IsZero() {
		return styleDim.Render("—")
	}
	d := time.Until(at)
	if d < 0 {
		return styleDim.Render("due")
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("in %ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("in %02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("in %dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return at.Format("01-02 15:04")
	}
}

func daemonBadge(d DaemonView) string {
	switch d.Status {
	case "running":
		return styleOK.Render(fmt.Sprintf("daemon running · pid %d", d.PID))
	case "stale":
		// A crash, and the header says so rather than pretending.
		return styleBad.Render(fmt.Sprintf("daemon stale · pid %d crashed", d.PID))
	default:
		return styleWarn.Render("daemon stopped · nothing is scheduled")
	}
}

func durationText(sec *float64) string {
	if sec == nil {
		return "—"
	}
	d := time.Duration(*sec * float64(time.Second))
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return d.Round(time.Second).String()
}
