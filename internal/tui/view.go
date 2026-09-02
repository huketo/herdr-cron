package tui

import (
	"fmt"
	"strings"
	"time"

	"image"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/huketo/herdr-cron/internal/config"
	"github.com/huketo/herdr-cron/internal/model"
	"github.com/huketo/herdr-cron/internal/store"
)

const (
	headerRows = 1
	footerRows = 1
	minWidth   = 40
)

// layout re-sizes every widget from the current terminal size. All measurement goes
// through lipgloss (docs/spec/06-tui.md §7.1).
func (m *Model) layout() {
	if m.width < minWidth {
		m.width = minWidth
	}
	helpRows := 1
	if m.showFullHelp {
		helpRows = 5
	}
	body := m.height - headerRows - footerRows - helpRows
	if body < 5 {
		body = 5
	}

	listWidth := m.width * 62 / 100
	detailWidth := m.width - listWidth - 1
	if detailWidth < 16 {
		detailWidth = 16
		listWidth = m.width - detailWidth - 1
	}

	// table.SetHeight takes the total, header line included. SetWidth is mandatory: the
	// widget's internal viewport defaults to zero width and then renders the header and
	// nothing else — three rows present, none visible.
	m.jobs.SetHeight(body - 2)
	m.jobs.SetWidth(listWidth - 2)
	m.jobs.SetColumns(jobColumns(listWidth - 2))

	// Every pane's inner size is its box minus the border, so JoinHorizontal does not
	// pad one column taller than its neighbour.
	m.detail.SetWidth(detailWidth - 2)
	m.detail.SetHeight(body - 2)

	m.runs.SetHeight(body/2 - 2)
	m.runs.SetWidth(m.width - 2)
	m.runs.SetColumns(runColumns(m.width - 2))

	m.output.SetWidth(m.width/2 - 2)
	m.output.SetHeight(body - 3)

	m.help.SetWidth(m.width)
}

// jobColumns fits the columns into w cells. The table's cell style pads one cell either
// side, so every column costs Width+2 and the budget must account for it or the header
// wraps and the layout collapses.
func jobColumns(w int) []table.Column {
	const columnCount = 6
	name := w - (2 + 16 + 11 + 8 + 2) - columnCount*2
	if name < 8 {
		name = 8
	}
	cols := []table.Column{
		{Title: "", Width: 2},
		{Title: "job", Width: name},
		{Title: "schedule", Width: 16},
		{Title: "next run", Width: 11},
		{Title: "last", Width: 8},
		{Title: "", Width: 2},
	}
	if w < 60 {
		cols[2].Width = 0
		cols[4].Width = 0
	}
	return cols
}

func runColumns(w int) []table.Column {
	first := w - (7 + 9 + 9 + 4) - 5*2
	if first < 16 {
		first = 16
	}
	return []table.Column{
		{Title: "started", Width: first},
		{Title: "dur", Width: 7},
		{Title: "status", Width: 9},
		{Title: "trigger", Width: 9},
		{Title: "exit", Width: 4},
	}
}

func (m *Model) syncJobs() {
	rows := make([]table.Row, 0, len(m.snap.Jobs))
	for _, v := range m.snap.Jobs {
		name := v.Job.Name
		if name == "" {
			name = v.Job.ID
		}
		run := "▶"
		if v.Running {
			run = "…"
		}
		rows = append(rows, table.Row{
			enabledGlyph(v), name, v.ScheduleText(),
			countdown(v.NextRunAt), statusText(v.Last), run,
		})
	}
	m.jobs.SetRows(rows)
	if m.jobs.Cursor() >= len(rows) {
		m.jobs.SetCursor(maxInt(0, len(rows)-1))
	}
	m.syncDetail()
}

func (m *Model) syncDetail() {
	id := m.selected
	if m.screen == screenJobs {
		id = m.cursorJob()
	}
	v, ok := m.snap.Job(id)
	if !ok {
		m.detail.SetContent(styleDim.Render("no job selected"))
		return
	}
	m.detail.SetContent(detailText(v))
}

func detailText(v JobView) string {
	j := v.Job
	var b strings.Builder
	line := func(k, val string) {
		fmt.Fprintf(&b, "%s %s\n", styleDim.Render(pad(k, 12)), val)
	}
	b.WriteString(styleHeader.Render(orID(j.Name, j.ID)) + "\n")
	if j.Description != "" {
		b.WriteString(styleDim.Render(j.Description) + "\n")
	}
	b.WriteString("\n")

	kind := string(j.Kind)
	if p, ok := j.Payload.(model.AgentPayload); ok {
		kind = fmt.Sprintf("agent (%s) · session %s", p.AgentKind, p.Session)
	}
	line("kind", kind)
	line("schedule", v.ScheduleText()+"  "+j.Schedule.Timezone)
	line("jitter", fmt.Sprintf("%ds", j.Schedule.JitterSec))
	line("catchup", string(j.Schedule.Catchup))
	line("enabled", fmt.Sprintf("%v (source: %s)", v.Enabled, v.EnabledSource))
	if len(v.NextRuns) > 0 {
		var next []string
		for _, t := range v.NextRuns {
			next = append(next, t.Format("01-02 15:04:05"))
		}
		line("next", strings.Join(next, " · "))
	}
	line("failures", fmt.Sprintf("%d / %d", v.ConsecutiveFailures, j.Limits.MaxConsecutiveFailures))
	line("runs today", fmt.Sprintf("%d / %s", v.RunsToday, limitText(j.Limits.MaxRunsPerDay)))
	line("cwd", j.Cwd)
	line("timeout", fmt.Sprintf("%ds", j.TimeoutSec))
	line("concurrency", string(j.Concurrency))
	line("retry", fmt.Sprintf("%d attempt(s), %s", j.Retry.MaxAttempts, j.Retry.Backoff))
	line("notify", strings.Join(j.Notify.On, ", "))
	if p, ok := j.Payload.(model.ShellPayload); ok {
		b.WriteString("\n" + styleDim.Render("command") + "\n" + p.Command + "\n")
	}
	if p, ok := j.Payload.(model.AgentPayload); ok {
		b.WriteString("\n" + styleDim.Render("prompt") + "\n" + p.Prompt + "\n")
		if p.NoOpMarker != "" {
			line("no-op marker", p.NoOpMarker)
		}
	}
	return b.String()
}

func limitText(n int) string {
	if n == 0 {
		return "∞"
	}
	return fmt.Sprint(n)
}

func (m *Model) syncRuns() {
	rows := make([]table.Row, 0, len(m.runList))
	for i := len(m.runList) - 1; i >= 0; i-- {
		r := m.runList[i]
		started := "—"
		if r.StartedAt != nil {
			started = r.StartedAt.Format("2006-01-02 15:04")
		}
		exit := "–"
		if r.ExitCode != nil {
			exit = fmt.Sprint(*r.ExitCode)
		}
		rows = append(rows, table.Row{
			started, durationText(r.DurationSec), statusText(r), string(r.Trigger), exit,
		})
	}
	m.runs.SetRows(rows)
	if m.runs.Cursor() >= len(rows) {
		m.runs.SetCursor(maxInt(0, len(rows)-1))
	}
	m.syncOutput()
}

// runAt maps a table row back to a run: the table shows newest first, the slice is
// newest last.
func (m *Model) runAt(row int) *model.Run {
	i := len(m.runList) - 1 - row
	if i < 0 || i >= len(m.runList) {
		return nil
	}
	return m.runList[i]
}

func (m *Model) syncOutput() {
	r := m.runAt(m.runs.Cursor())
	if r == nil {
		m.output.SetContent(styleDim.Render("no run selected"))
		return
	}
	head := fmt.Sprintf("%s  %s  %s\n", r.RunID, statusText(r), reasonText(r))
	m.output.SetContent(head + "\n" + LogText(m.roots, r))
}

func reasonText(r *model.Run) string {
	if r.Reason == nil || *r.Reason == "" {
		return ""
	}
	return styleWarn.Render(*r.Reason)
}

// ---------------------------------------------------------------- rendering

// View renders one frame and declares the terminal modes that frame wants. In v2 the alt
// screen and mouse mode are fields of the view rather than program options, so every
// frame re-asserts them and mouse mode can be toggled mid-session
// (docs/spec/06-tui.md §1.4, §6).
func (m *Model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	// Terminal modes are declarative fields in v2. Mouse mode is never left disabled
	// without the `m` binding advertised in the footer (docs/spec/06-tui.md §6.2).
	if m.mouseOn {
		v.MouseMode = tea.MouseModeCellMotion
	} else {
		v.MouseMode = tea.MouseModeNone
	}

	v.Content = m.renderScreen()

	// One mouse entry point: the hit test turns a click into a LayerHitMsg naming a
	// layer, so no screen does coordinate arithmetic of its own.
	v.OnMouse = func(msg tea.MouseMsg) tea.Cmd {
		mouse := msg.Mouse()
		id := m.hitTest(mouse.X, mouse.Y)
		if id == "" {
			return nil
		}
		return func() tea.Msg { return LayerHitMsg{ID: id, Mouse: msg} }
	}
	return v
}

// buildLayers composes one full-screen string and then overlays *transparent* hit
// layers on top of it. Rendering and hit testing therefore share one geometry: the boxes
// have fixed sizes, so every overlay offset is arithmetic on those sizes rather than a
// guess (docs/spec/06-tui.md §2).
func (m *Model) renderScreen() string {
	bodyH := m.bodyHeight()
	var content string
	var hits []hitRect

	switch m.screen {
	case screenJobs:
		content, hits = m.renderJobs(bodyH)
	case screenDetail:
		content, hits = m.renderDetail(bodyH)
	default:
		content, hits = m.renderRuns(bodyH)
	}

	screen := lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(), content, m.renderFooter())

	rects := []hitRect{
		// The header owns the daemon badge; the mouse badge sits at a higher z so a
		// click on it wins over the header.
		hit("pane.header", 0, 0, m.width, 1, 1),
		hit("hdr.mouse", maxInt(0, m.width-14), 0, 14, 1, 3),
		hit("pane.help", 0, headerRows+bodyH, m.width, footerHeight(m), 1),
	}
	// Body rectangles are positioned relative to the body, which starts under the header.
	for _, r := range hits {
		r.Rect = r.Rect.Add(image.Pt(0, headerRows))
		rects = append(rects, r)
	}
	if m.modal != nil {
		screen, rects = m.renderModal(screen, rects)
	}
	m.hits = rects
	return screen
}

// hitTest returns the id under a cell: highest z first, then the smallest rectangle, so a
// child affordance always wins over the pane containing it.
func (m *Model) hitTest(x, y int) string {
	best := ""
	bestZ, bestArea := -1, 1<<30
	for _, r := range m.hits {
		if !image.Pt(x, y).In(r.Rect) {
			continue
		}
		area := r.Rect.Dx() * r.Rect.Dy()
		if r.Z > bestZ || (r.Z == bestZ && area < bestArea) {
			best, bestZ, bestArea = r.ID, r.Z, area
		}
	}
	return best
}

// hit registers a clickable rectangle.
//
// Hit testing does NOT use lipgloss.Compositor, and the reason is empirical: a layer
// painted over another is opaque even when its content is only spaces — a two-space
// overlay at (1,1) over "FGHIJ" renders "F  IJ". "Invisible hit layers" therefore do not
// exist, so herdr-cron keeps its own rectangle table, built from the same numbers the
// layout uses (see docs/spec/06-tui.md §2 and the correction recorded there).
func hit(id string, x, y, w, h, z int) hitRect {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return hitRect{ID: id, Rect: image.Rect(x, y, x+w, y+h), Z: z}
}

func (m *Model) bodyHeight() int {
	h := m.height - headerRows - footerHeight(m)
	if h < 4 {
		h = 4
	}
	return h
}

func footerHeight(m *Model) int {
	if m.showFullHelp {
		return 5
	}
	return 1
}

// renderJobs is screen 1: the job table beside the detail pane.
func (m *Model) renderJobs(bodyH int) (string, []hitRect) {
	listW := m.listWidth()
	detailW := m.width - listW

	left := box(m, "pane.jobs", listW, bodyH, m.jobs.View())
	right := box(m, "pane.detail", detailW, bodyH, m.detail.View())
	content := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	hits := []hitRect{
		hit("pane.jobs", 0, 0, listW, bodyH, 1),
		hit("pane.detail", listW, 0, detailW, bodyH, 1),
	}

	// One row layer per visible row. The box border is one line and the table renders a
	// header line above the rows, so the first row sits at +2.
	const rowOrigin = 2
	visible := bodyH - 3
	for i, v := range m.snap.Jobs {
		if i >= visible {
			break
		}
		id := v.Job.ID
		y := rowOrigin + i
		hits = append(hits,
			hit("row."+id, 1, y, listW-2, 1, 2),
			hit("row."+id+".toggle", 1, y, 3, 1, 4),
			hit("row."+id+".run", maxInt(2, listW-3), y, 2, 1, 4),
		)
	}
	return content, hits
}

func (m *Model) listWidth() int {
	w := m.width * 62 / 100
	if w < 30 {
		w = minInt(m.width, 30)
	}
	return w
}

// renderDetail is screen 2: the definition above the recent-runs table, with the action
// buttons on the first line.
func (m *Model) renderDetail(bodyH int) (string, []hitRect) {
	v, _ := m.snap.Job(m.selected)
	defH := bodyH * 55 / 100
	runsH := bodyH - defH

	runBtn := styleButton.Render("run ▶")
	pauseBtn := styleButton.Render(pauseLabel(v))
	delBtn := styleDanger.Render("delete")
	buttons := lipgloss.JoinHorizontal(lipgloss.Top, runBtn, " ", pauseBtn, " ", delBtn)

	crumb := styleDim.Render(" ‹ jobs / ") + styleHeader.Render(m.selected)
	bar := lipgloss.NewStyle().Width(m.width).Render(
		crumb + strings.Repeat(" ", maxInt(1, m.width-lipgloss.Width(crumb)-lipgloss.Width(buttons)-1)) + buttons)

	defPane := box(m, "pane.definition", m.width, defH-1, m.detail.View())
	runsPane := box(m, "pane.runs", m.width, runsH, m.runs.View())
	content := lipgloss.JoinVertical(lipgloss.Left, bar, defPane, runsPane)

	bx := maxInt(0, m.width-lipgloss.Width(buttons)-1)
	hits := []hitRect{
		hit("pane.definition", 0, 1, m.width, defH, 1),
		hit("pane.runs", 0, defH, m.width, runsH, 1),
		hit("nav.back", 0, 0, 10, 1, 3),
		hit("btn.job.run", bx, 0, lipgloss.Width(runBtn), 1, 4),
		hit("btn.job.pause", bx+lipgloss.Width(runBtn)+1, 0, lipgloss.Width(pauseBtn), 1, 4),
		hit("btn.job.delete", bx+lipgloss.Width(runBtn)+lipgloss.Width(pauseBtn)+2, 0,
			lipgloss.Width(delBtn), 1, 4),
	}
	// Rows of the recent-runs table, for click-to-open.
	const rowOrigin = 2
	for row := range m.runList {
		r := m.runAt(row)
		if r == nil || row >= runsH-3 {
			break
		}
		hits = append(hits, hit("row.run."+r.RunID, 1, defH+rowOrigin+row, m.width-2, 1, 2))
	}
	return content, hits
}

// renderRuns is screen 3: history beside the selected run's output.
func (m *Model) renderRuns(bodyH int) (string, []hitRect) {
	half := m.width / 2
	crumb := styleDim.Render(" ‹ jobs / ") + styleHeader.Render(m.selected) + styleDim.Render(" / runs")
	copyBtn := styleButton.Render("copy")
	bar := crumb + strings.Repeat(" ", maxInt(1, m.width-lipgloss.Width(crumb)-lipgloss.Width(copyBtn)-1)) + copyBtn

	left := box(m, "pane.runs", half, bodyH-1, m.runs.View())
	right := box(m, "pane.output", m.width-half, bodyH-1, m.output.View())
	content := lipgloss.JoinVertical(lipgloss.Left, bar,
		lipgloss.JoinHorizontal(lipgloss.Top, left, right))

	hits := []hitRect{
		hit("pane.runs", 0, 1, half, bodyH-1, 1),
		hit("pane.output", half, 1, m.width-half, bodyH-1, 1),
		hit("nav.back", 0, 0, 10, 1, 3),
		hit("btn.copy", maxInt(0, m.width-lipgloss.Width(copyBtn)-1), 0,
			lipgloss.Width(copyBtn), 1, 4),
	}
	const rowOrigin = 3
	for row := range m.runList {
		r := m.runAt(row)
		if r == nil || row >= bodyH-4 {
			break
		}
		hits = append(hits, hit("row.run."+r.RunID, 1, rowOrigin+row, half-2, 1, 2))
	}
	return content, hits
}

// box frames a widget at an exact size, so the overlay arithmetic above is exact too.
func box(m *Model, id string, w, h int, content string) string {
	style := styleBorder
	if m.focus == id {
		style = styleFocused
	}
	inner := w - 2
	if inner < 1 {
		inner = 1
	}
	innerH := h - 2
	if innerH < 1 {
		innerH = 1
	}
	return style.Width(inner).Height(innerH).MaxWidth(w).MaxHeight(h).Render(content)
}

func (m *Model) renderModal(screen string, rects []hitRect) (string, []hitRect) {
	md := m.modal
	w := minInt(60, maxInt(20, m.width-4))
	confirm := styleButton.Render(md.confirm)
	if md.danger {
		confirm = styleDanger.Render(md.confirm)
	}
	body := lipgloss.JoinVertical(lipgloss.Left,
		styleHeader.Render(md.title), "", md.body, "",
		lipgloss.JoinHorizontal(lipgloss.Top, confirm, " ", styleButton.Render("cancel")))
	rendered := styleBorder.Width(w).Render(body)

	x := maxInt(0, (m.width-lipgloss.Width(rendered))/2)
	y := maxInt(0, (m.height-lipgloss.Height(rendered))/2)

	// The box is composited over the screen; the scrim is a hit rectangle only, so the
	// screen behind stays readable while a click outside the box still dismisses.
	composed := lipgloss.NewCompositor(
		lipgloss.NewLayer(screen),
		lipgloss.NewLayer(rendered).X(x).Y(y).Z(100),
	).Render()

	rects = append(rects,
		hit("modal.scrim", 0, 0, m.width, m.height, 50),
		hit("modal.box", x, y, lipgloss.Width(rendered), lipgloss.Height(rendered), 100),
		hit("modal.confirm", x+1, y+lipgloss.Height(rendered)-2, lipgloss.Width(confirm), 1, 101),
	)
	return composed, rects
}

func (m *Model) renderHeader() string {
	left := styleHeader.Render(" herdr-cron ") + " " + daemonBadge(m.snap.Daemon)
	if m.snap.ConfigError != "" {
		// A broken jobs.yaml must be visible in the header, never silent.
		left = styleHeader.Render(" herdr-cron ") + " " +
			styleBad.Render("jobs.yaml invalid: "+m.snap.ConfigError)
	}
	left = truncate(left, maxInt(1, m.width-15))
	badge := mouseBadge(m.mouseOn)
	gap := maxInt(1, m.width-lipgloss.Width(left)-lipgloss.Width(badge))
	return left + strings.Repeat(" ", gap) + badge
}

func mouseBadge(on bool) string {
	if on {
		return styleDim.Render("mouse: on (m)")
	}
	// Once mouse mode is off the badge is unclickable, which is exactly why the key
	// binding and its help text are mandatory (docs/spec/06-tui.md §6.2).
	return styleWarn.Render("mouse: off(m)")
}

func (m *Model) renderFooter() string {
	if m.status != "" && time.Since(m.statusAt) < 6*time.Second {
		return truncate(styleInfo.Render(m.status), m.width)
	}
	m.help.ShowAll = m.showFullHelp
	return m.help.View(m.keys)
}

func pauseLabel(v JobView) string {
	if v.Enabled {
		return "pause"
	}
	return "resume"
}

// ---------------------------------------------------------------- hit dispatch

func (m *Model) handleHit(msg LayerHitMsg) tea.Cmd {
	if m.modal != nil {
		switch msg.ID {
		case "modal.confirm":
			action := m.modal.action
			m.modal = nil
			if action != nil {
				return action(m)
			}
			return nil
		case "modal.scrim":
			m.modal = nil
			return nil
		}
		return nil
	}

	if _, isWheel := msg.Mouse.(tea.MouseWheelMsg); isWheel {
		return m.handleWheel(msg)
	}
	if _, isClick := msg.Mouse.(tea.MouseClickMsg); !isClick {
		return nil
	}

	switch {
	case msg.ID == "hdr.mouse":
		m.mouseOn = !m.mouseOn
		m.setStatus("mouse " + onOff(m.mouseOn) + " — press m to toggle")
		return nil
	case msg.ID == "hdr.daemon", msg.ID == "pane.header":
		if m.snap.Daemon.Status != "running" {
			m.modal = &modal{title: "Scheduler is not running",
				body:    "No process holds daemon.lock, so nothing is being scheduled.\n\nStart it with:\n  herdr-cron daemon --detach",
				confirm: "ok"}
		}
		return nil
	case msg.ID == "pane.help":
		m.showFullHelp = !m.showFullHelp
		m.layout()
		return nil
	case msg.ID == "nav.back":
		if m.screen == screenRuns {
			m.screen = screenDetail
		} else {
			m.screen = screenJobs
		}
		return nil
	case msg.ID == "btn.copy":
		return m.copyOutput()
	case msg.ID == "btn.job.run":
		return m.runNow(m.selected)
	case msg.ID == "btn.job.pause":
		return m.toggleEnabled(m.selected)
	case msg.ID == "btn.job.delete":
		m.openDeleteModal(m.selected)
		return nil
	case strings.HasPrefix(msg.ID, "row.run."):
		return m.selectRun(strings.TrimPrefix(msg.ID, "row.run."))
	case strings.HasSuffix(msg.ID, ".toggle"):
		return m.toggleEnabled(strings.TrimSuffix(strings.TrimPrefix(msg.ID, "row."), ".toggle"))
	case strings.HasSuffix(msg.ID, ".run"):
		return m.runNow(strings.TrimSuffix(strings.TrimPrefix(msg.ID, "row."), ".run"))
	case strings.HasPrefix(msg.ID, "row."):
		return m.selectRow(strings.TrimPrefix(msg.ID, "row."))
	case msg.ID == "pane.detail", msg.ID == "pane.definition", msg.ID == "pane.output":
		m.jobs.Blur()
		m.focus = msg.ID
		return nil
	case msg.ID == "pane.jobs", msg.ID == "pane.runs":
		m.focus = msg.ID
		m.jobs.Focus()
		return nil
	}
	return nil
}

// selectRow implements click-to-select plus herdr-cron's own double-click rule: v2 has no
// double-click message and no configurable threshold (docs/spec/06-tui.md §2.1).
func (m *Model) selectRow(jobID string) tea.Cmd {
	for i, v := range m.snap.Jobs {
		if v.Job.ID != jobID {
			continue
		}
		m.jobs.SetCursor(i)
		m.jobs.Focus()
		m.focus = "pane.jobs"
		m.syncDetail()
		break
	}
	double := m.lastClickID == jobID && time.Since(m.lastClickAt) < doubleClickWindow
	m.lastClickID, m.lastClickAt = jobID, time.Now()
	if double {
		return m.openJob(jobID)
	}
	return nil
}

func (m *Model) selectRun(runID string) tea.Cmd {
	for row := range m.runList {
		if r := m.runAt(row); r != nil && r.RunID == runID {
			m.runs.SetCursor(row)
			m.runIndex = row
			m.focus = "pane.runs"
			m.syncOutput()
			break
		}
	}
	if m.screen == screenDetail {
		m.screen = screenRuns
	}
	return nil
}

// handleWheel forwards a wheel event to whichever pane it landed on. bubbles/table has no
// wheel handling at all, so the list scroll is implemented here; viewport handles its own
// (docs/spec/06-tui.md §1.5).
func (m *Model) handleWheel(msg LayerHitMsg) tea.Cmd {
	wheel, _ := msg.Mouse.(tea.MouseWheelMsg)
	up := wheel.Button == tea.MouseWheelUp
	target := msg.ID
	if i := strings.Index(target, "row."); i == 0 {
		if strings.HasPrefix(target, "row.run.") {
			target = "pane.runs"
		} else {
			target = "pane.jobs"
		}
	}

	switch target {
	case "pane.jobs":
		if up {
			m.jobs.MoveUp(3)
		} else {
			m.jobs.MoveDown(3)
		}
		m.syncDetail()
	case "pane.runs":
		if up {
			m.runs.MoveUp(3)
		} else {
			m.runs.MoveDown(3)
		}
		m.syncOutput()
	case "pane.detail", "pane.definition":
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg.Mouse)
		return cmd
	case "pane.output":
		var cmd tea.Cmd
		m.output, cmd = m.output.Update(msg.Mouse)
		return cmd
	}
	return nil
}

// ---------------------------------------------------------------- deletion

// deleteJob removes a job through the same package the cobra command calls, so there is
// no second YAML writer (docs/spec/06-tui.md §1.2).
func (m *Model) deleteJob(id string) tea.Cmd {
	roots := m.roots
	return func() tea.Msg {
		_, issues, err := config.Apply(roots.JobsFile(), config.RemoveJob(id))
		switch {
		case err != nil:
			return actionDoneMsg{what: "delete " + id, err: err}
		case len(issues) > 0:
			return actionDoneMsg{what: "delete " + id,
				err: fmt.Errorf("the result would be invalid: %s", issues[0].String())}
		}
		if err := store.New(roots).ForgetJob(id, false); err != nil {
			return actionDoneMsg{what: "delete " + id, err: err}
		}
		return actionDoneMsg{what: id + " deleted from jobs.yaml"}
	}
}

// ---------------------------------------------------------------- helpers

func pad(s string, w int) string {
	for lipgloss.Width(s) < w {
		s += " "
	}
	return s
}

func truncate(s string, w int) string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return s
	}
	out := []rune(s)
	for len(out) > 0 && lipgloss.Width(string(out)) > w {
		out = out[:len(out)-1]
	}
	return string(out)
}

func orID(name, id string) string {
	if name != "" {
		return name
	}
	return id
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
