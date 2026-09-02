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

	styleListHeader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	// The cursor row is a background band, not a foreground colour: a cell keeps its own
	// colour, and the band still reads on a row where every cell is coloured. The dim
	// variant marks the cursor of a list that does not hold keyboard focus, so a reader
	// can always see both where the cursor is and which pane the keys reach.
	styleCursor     = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("24"))
	styleCursorBlur = lipgloss.NewStyle().Background(lipgloss.Color("236"))
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

// enabledCell is the leading column of the job list: enabled, disabled by override, or
// disabled by the circuit breaker.
func enabledCell(v JobView) cellText {
	switch {
	case v.AutoDisabled:
		return cellText{"⊘", styleBad}
	case !v.Enabled:
		return cellText{"○", styleDim}
	default:
		return cellText{"●", styleOK}
	}
}

func statusCell(r *model.Run) cellText {
	if r == nil {
		return cellText{"—", styleDim}
	}
	label := string(r.Status)
	if r.Status == model.StatusNoOp {
		label = "no-op"
	}
	return cellText{label, statusStyle(r.Status)}
}

// statusText is the same status as prose, for the run output header, which is a string
// rather than a cell.
func statusText(r *model.Run) string {
	c := statusCell(r)
	return c.Style.Render(c.Text)
}

// countdownCell renders a next-run instant the way a human reads a scheduler.
func countdownCell(at time.Time) cellText {
	if at.IsZero() {
		return cellText{"—", styleDim}
	}
	d := time.Until(at)
	if d < 0 {
		return cellText{"due", styleWarn}
	}
	switch {
	case d < time.Minute:
		return text(fmt.Sprintf("in %ds", int(d.Seconds())))
	case d < time.Hour:
		return text(fmt.Sprintf("in %02d:%02d", int(d.Minutes()), int(d.Seconds())%60))
	case d < 24*time.Hour:
		return text(fmt.Sprintf("in %dh%02dm", int(d.Hours()), int(d.Minutes())%60))
	default:
		return text(at.Format("01-02 15:04"))
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
