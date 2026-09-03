package tui

import (
	"fmt"
	"image"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/huketo/herdr-cron/internal/config"
	"github.com/huketo/herdr-cron/internal/model"
	"github.com/huketo/herdr-cron/internal/store"
)

const (
	headerRows = 1
	minWidth   = 40
	// borderSize is the rounded border of a pane: one cell on each side. In lipgloss v2
	// Style.Width and Style.Height are the whole block, border included
	// (`[LG set.go:283-297]`), so a widget must be sized to its pane minus this or the
	// border is what gets clipped.
	borderSize = 2
	// hintRows is the scroll indicator every scrollable pane carries as its last row.
	hintRows  = 1
	wheelStep = 3
)

// ---------------------------------------------------------------- geometry

// paneLayout is the geometry of the active screen: the rows its breadcrumb bar occupies
// and the two framed panes below it, as body-relative outer rectangles.
//
// One function owns these numbers and three consumers read them: layout sizes the
// widgets, the render functions draw the boxes, and the same rectangles become the hit
// table. Computed three times independently, they disagreed: the definition pane
// inherited the job list's viewport, so a full-width pane wrapped its text at a third of
// its width, and the viewport stayed twice as tall as the box — it reported every line as
// visible, so no key and no wheel event scrolled, while the box clipped the rows below
// the fold away. The pane looked complete and was unreachable.
type paneLayout struct {
	bar           int
	first, second image.Rectangle
}

// panes computes the active screen's geometry from the terminal size alone.
func (m *Model) panes() paneLayout {
	bodyH := m.bodyHeight()
	switch m.screen {
	case screenJobs:
		listW := m.listWidth()
		return paneLayout{
			first:  image.Rect(0, 0, listW, bodyH),
			second: image.Rect(listW, 0, m.width, bodyH),
		}
	case screenDetail:
		// The breadcrumb and the action buttons own the first body row.
		defH := clampInt((bodyH-1)*55/100, 3, maxInt(3, bodyH-1-3))
		return paneLayout{
			bar:    1,
			first:  image.Rect(0, 1, m.width, 1+defH),
			second: image.Rect(0, 1+defH, m.width, bodyH),
		}
	default:
		half := m.width / 2
		return paneLayout{
			bar:    1,
			first:  image.Rect(0, 1, half, bodyH),
			second: image.Rect(half, 1, m.width, bodyH),
		}
	}
}

// innerSize is the space inside a pane's border.
func innerSize(r image.Rectangle) (int, int) {
	return maxInt(1, r.Dx()-borderSize), maxInt(1, r.Dy()-borderSize)
}

// layout re-sizes the widgets of the active screen from the current terminal size. All
// measurement goes through lipgloss (docs/spec/06-tui.md §7.1).
func (m *Model) layout() {
	if m.width < minWidth {
		m.width = minWidth
	}
	p := m.panes()
	switch m.screen {
	case screenJobs:
		sizeList(&m.jobs, p.first, jobColumns)
		sizeViewport(&m.detail, p.second)
	case screenDetail:
		sizeViewport(&m.detail, p.first)
		sizeList(&m.runs, p.second, runColumns)
	default:
		sizeList(&m.runs, p.first, runColumns)
		sizeViewport(&m.output, p.second)
	}
	m.help.SetWidth(m.width)

	// Rows are rendered at the column widths just computed, so the content is rebuilt
	// here rather than left one layout behind.
	m.syncJobs()
	if m.screen != screenJobs {
		m.syncRuns()
	}
}

// sizeList gives a list the pane's inner size less its scroll-hint row.
func sizeList(l *rowList, r image.Rectangle, fit func(int) []column) {
	w, h := innerSize(r)
	l.SetColumns(fit(w))
	l.SetSize(w, maxInt(1, h-hintRows))
}

// sizeViewport gives a viewport the pane's inner size less its scroll-hint row. A
// viewport sized taller than the box that draws it believes its content is fully visible,
// and then neither a key nor the wheel moves it.
func sizeViewport(v *viewport.Model, r image.Rectangle) {
	w, h := innerSize(r)
	v.SetWidth(w)
	v.SetHeight(maxInt(1, h-hintRows))
}

// jobColumns fits the list columns into exactly w cells. Each cell is rendered at its
// column width, padding included (renderCell), so the widths must sum to w or the header
// wraps and the layout collapses. The name column takes what is left; when that is too
// little to read, the optional columns are shed one at a time.
func jobColumns(w int) []column {
	const glyph, run = 4, 4
	sched, next, last := 18, 13, 10
	name := func() int { return w - glyph - run - sched - next - last }
	for _, optional := range []*int{&sched, &last, &next} {
		if name() >= 12 {
			break
		}
		*optional = 0
	}
	return []column{
		{Width: glyph},
		{Title: "job", Width: maxInt(1, name())},
		{Title: "schedule", Width: sched},
		{Title: "next run", Width: next},
		{Title: "last", Width: last},
		{Width: run},
	}
}

// runColumns keeps the run columns at their natural widths and puts the slack in a
// trailing filler column, so the timestamps stay next to their durations instead of a
// 60-cell gap opening between them. Narrow panes shed the rightmost columns first.
func runColumns(w int) []column {
	cols := []column{
		{Title: "started", Width: 20},
		{Title: "dur", Width: 9},
		{Title: "status", Width: 11},
		{Title: "trigger", Width: 11},
		{Title: "exit", Width: 6},
	}
	for _, optional := range []int{3, 4, 1} {
		if columnsWidth(cols) <= w {
			break
		}
		cols[optional].Width = 0
	}
	// Still over budget on a very narrow pane: the timestamp column gives way.
	if over := columnsWidth(cols) - w; over > 0 {
		cols[0].Width = maxInt(1, cols[0].Width-over)
	}
	// A filler column absorbs the slack, because a row must fill the pane or the cursor
	// band stops short of its right edge.
	if slack := w - columnsWidth(cols); slack > 0 {
		cols = append(cols, column{Width: slack})
	}
	return cols
}

func columnsWidth(cols []column) int {
	total := 0
	for _, c := range cols {
		total += c.Width
	}
	return total
}

func (m *Model) syncJobs() {
	rows := make([][]cellText, 0, len(m.snap.Jobs))
	for _, v := range m.snap.Jobs {
		run := cellText{"▶", styleDim}
		if v.Running {
			run = cellText{"…", styleInfo}
		}
		next := countdownCell(v.NextRunAt)
		if v.Completed {
			next = cellText{"completed", styleOK}
		}
		rows = append(rows, []cellText{
			enabledCell(v),
			text(orID(v.Job.Name, v.Job.ID)),
			text(v.ScheduleText()),
			next,
			statusCell(v.Last),
			run,
		})
	}
	m.jobs.SetRows(rows)
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
	m.detail.SetContent(wrapText(detailText(v), m.detail.Width()))
}

// wrapText word-wraps prose to the pane width before it reaches the viewport. The
// viewport's own soft wrap breaks at the cell — mid-word and, for a Korean prompt,
// mid-sentence — and a job's prompt is prose. Wrapping here also makes the viewport's
// line count the real line count, which is what its scroll arithmetic works from.
func wrapText(s string, w int) string {
	if w < 1 {
		return s
	}
	return lipgloss.NewStyle().Width(w).Render(s)
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
	switch {
	case v.Completed:
		line("next", styleOK.Render("completed"))
	case len(v.NextRuns) > 0:
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
	rows := make([][]cellText, 0, len(m.runList))
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
		rows = append(rows, []cellText{
			text(started), text(durationText(r.DurationSec)),
			statusCell(r), text(string(r.Trigger)), text(exit),
		})
	}
	m.runs.SetRows(rows)
	m.syncOutput()
}

// runAt maps a list row back to a run: the list shows newest first, the slice is
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
	m.output.SetContent(wrapText(head+"\n"+LogText(m.roots, r), m.output.Width()))
	if m.follow {
		// Following means the tail, and a live run's log grows under the reader.
		m.output.GotoBottom()
	}
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

// renderScreen composes the frame and the hit table from one set of rectangles, so the
// two can never disagree (docs/spec/06-tui.md §2).
func (m *Model) renderScreen() string {
	p := m.panes()
	var content string
	var hits []hitRect

	switch m.screen {
	case screenJobs:
		content, hits = m.renderJobs(p)
	case screenDetail:
		content, hits = m.renderDetail(p)
	default:
		content, hits = m.renderRuns(p)
	}

	screen := lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(), content, m.renderFooter())

	rects := []hitRect{
		// The header owns the daemon badge; the mouse badge sits at a higher z so a
		// click on it wins over the header.
		hit("pane.header", 0, 0, m.width, 1, 1),
		hit("hdr.mouse", maxInt(0, m.width-14), 0, 14, 1, 3),
		hit("pane.help", 0, headerRows+m.bodyHeight(), m.width, footerHeight(m), 1),
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

// rectHit registers a pane's own rectangle, which the layout already computed.
func rectHit(id string, r image.Rectangle, z int) hitRect {
	return hitRect{ID: id, Rect: r, Z: z}
}

func (m *Model) bodyHeight() int {
	h := m.height - headerRows - footerHeight(m)
	if h < 6 {
		h = 6
	}
	return h
}

func footerHeight(m *Model) int {
	if m.showFullHelp {
		return 5
	}
	return 1
}

// renderJobs is screen 1: the job list beside the detail pane.
func (m *Model) renderJobs(p paneLayout) (string, []hitRect) {
	left := m.box("pane.jobs", p.first, listPane(&m.jobs, p.first))
	right := m.box("pane.detail", p.second, viewportPane(&m.detail, p.second))
	content := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	hits := []hitRect{
		rectHit("pane.jobs", p.first, 1),
		rectHit("pane.detail", p.second, 1),
	}

	w, _ := innerSize(p.first)
	first, last := visibleRange(&m.jobs, len(m.snap.Jobs))
	for i := first; i < last; i++ {
		id := m.snap.Jobs[i].Job.ID
		y := rowY(p.first, i-first)
		runW := m.jobs.ColumnWidth(-1)
		hits = append(hits,
			hit("row."+id, p.first.Min.X+1, y, w, 1, 2),
			hit("row."+id+".toggle", p.first.Min.X+1, y, m.jobs.ColumnWidth(0), 1, 4),
			hit("row."+id+".run", p.first.Max.X-1-runW, y, runW, 1, 4),
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

// renderDetail is screen 2: the definition above the recent-runs list, with the action
// buttons on the first line.
func (m *Model) renderDetail(p paneLayout) (string, []hitRect) {
	v, _ := m.snap.Job(m.selected)

	runBtn := styleButton.Render("run ▶")
	pauseBtn := styleButton.Render(pauseLabel(v))
	delBtn := styleDanger.Render("delete")
	buttons := lipgloss.JoinHorizontal(lipgloss.Top, runBtn, " ", pauseBtn, " ", delBtn)

	crumb := styleDim.Render(" ‹ jobs / ") + styleHeader.Render(m.selected)
	bar := lipgloss.NewStyle().MaxWidth(m.width).Render(
		crumb + strings.Repeat(" ",
			maxInt(1, m.width-lipgloss.Width(crumb)-lipgloss.Width(buttons)-1)) + buttons)

	defPane := m.box("pane.definition", p.first, viewportPane(&m.detail, p.first))
	runsPane := m.box("pane.runs", p.second, listPane(&m.runs, p.second))
	content := lipgloss.JoinVertical(lipgloss.Left, bar, defPane, runsPane)

	bx := maxInt(0, m.width-lipgloss.Width(buttons)-1)
	hits := []hitRect{
		rectHit("pane.definition", p.first, 1),
		rectHit("pane.runs", p.second, 1),
		hit("nav.back", 0, 0, 10, 1, 3),
		hit("btn.job.run", bx, 0, lipgloss.Width(runBtn), 1, 4),
		hit("btn.job.pause", bx+lipgloss.Width(runBtn)+1, 0, lipgloss.Width(pauseBtn), 1, 4),
		hit("btn.job.delete", bx+lipgloss.Width(runBtn)+lipgloss.Width(pauseBtn)+2, 0,
			lipgloss.Width(delBtn), 1, 4),
	}
	return content, append(hits, m.runRowHits(p.second)...)
}

// renderRuns is screen 3: history beside the selected run's output.
func (m *Model) renderRuns(p paneLayout) (string, []hitRect) {
	crumb := styleDim.Render(" ‹ jobs / ") + styleHeader.Render(m.selected) +
		styleDim.Render(" / runs")
	copyBtn := styleButton.Render("copy")
	bar := lipgloss.NewStyle().MaxWidth(m.width).Render(
		crumb + strings.Repeat(" ",
			maxInt(1, m.width-lipgloss.Width(crumb)-lipgloss.Width(copyBtn)-1)) + copyBtn)

	left := m.box("pane.runs", p.first, listPane(&m.runs, p.first))
	right := m.box("pane.output", p.second, viewportPane(&m.output, p.second))
	content := lipgloss.JoinVertical(lipgloss.Left, bar,
		lipgloss.JoinHorizontal(lipgloss.Top, left, right))

	hits := []hitRect{
		rectHit("pane.runs", p.first, 1),
		rectHit("pane.output", p.second, 1),
		hit("nav.back", 0, 0, 10, 1, 3),
		hit("btn.copy", maxInt(0, m.width-lipgloss.Width(copyBtn)-1), 0,
			lipgloss.Width(copyBtn), 1, 4),
	}
	return content, append(hits, m.runRowHits(p.first)...)
}

// runRowHits is one rectangle per visible run row, shared by both screens that list runs.
func (m *Model) runRowHits(r image.Rectangle) []hitRect {
	w, _ := innerSize(r)
	first, last := visibleRange(&m.runs, m.runs.Len())
	hits := make([]hitRect, 0, last-first)
	for row := first; row < last; row++ {
		run := m.runAt(row)
		if run == nil {
			continue
		}
		hits = append(hits, hit("row.run."+run.RunID, r.Min.X+1, rowY(r, row-first), w, 1, 2))
	}
	return hits
}

// rowY is the screen row of the nth visible row of a pane: past the top border and past
// the list's header line.
func rowY(r image.Rectangle, n int) int { return r.Min.Y + 2 + n }

// visibleRange is the half-open range of row indices a list currently shows. It comes
// from the list's own window, which is why herdr-cron owns that window (see rowList).
func visibleRange(l *rowList, n int) (int, int) {
	first := minInt(l.Top(), n)
	return first, minInt(first+l.Visible(), n)
}

// box frames a widget at an exact size. Width and Height are the whole block in lipgloss
// v2, border included, and the widget inside was sized to match, so nothing is clipped.
func (m *Model) box(id string, r image.Rectangle, content string) string {
	style := styleBorder
	if m.focus == id {
		style = styleFocused
	}
	return style.
		Width(r.Dx()).Height(r.Dy()).
		MaxWidth(r.Dx()).MaxHeight(r.Dy()).
		Render(content)
}

// listPane is a list plus the scroll hint that occupies the pane's last row.
func listPane(l *rowList, r image.Rectangle) string {
	w, _ := innerSize(r)
	return l.View() + "\n" + listHint(l, w)
}

// viewportPane is a viewport plus the scroll hint that occupies the pane's last row.
func viewportPane(v *viewport.Model, r image.Rectangle) string {
	w, _ := innerSize(r)
	return v.View() + "\n" + viewportHint(v, w)
}

// viewportHint is the last row of a scrollable pane.
//
// A pane holding more text than it shows looks exactly like a pane with nothing more to
// show. That is what makes a reader conclude the scroll keys are dead, so the hint states
// which of the two it is, and in which direction there is more.
func viewportHint(v *viewport.Model, w int) string {
	if v.AtTop() && v.AtBottom() {
		return ""
	}
	return styleDim.Render(rightAlign(
		fmt.Sprintf("%s %d%% %s", arrow("▲", !v.AtTop()),
			int(v.ScrollPercent()*100), arrow("▼", !v.AtBottom())), w))
}

func listHint(l *rowList, w int) string {
	if l.AtTop() && l.AtBottom() {
		return ""
	}
	first, last := visibleRange(l, l.Len())
	return styleDim.Render(rightAlign(
		fmt.Sprintf("%s %d-%d/%d %s", arrow("▲", !l.AtTop()),
			first+1, last, l.Len(), arrow("▼", !l.AtBottom())), w))
}

func arrow(glyph string, on bool) string {
	if on {
		return glyph
	}
	return " "
}

func rightAlign(s string, w int) string {
	return strings.Repeat(" ", maxInt(0, w-lipgloss.Width(s))) + s
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
			m.gotoScreen(screenDetail, "pane.definition")
		} else {
			m.gotoScreen(screenJobs, "pane.jobs")
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
	case msg.ID == "pane.detail", msg.ID == "pane.definition", msg.ID == "pane.output",
		msg.ID == "pane.jobs", msg.ID == "pane.runs":
		// Clicking a pane moves keyboard focus to it, so the keys and the highlighted
		// border never disagree (docs/spec/06-tui.md §1.3).
		m.setFocus(msg.ID)
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
		m.setFocus("pane.jobs")
		m.syncJobs()
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
	for row := range m.runs.Len() {
		if r := m.runAt(row); r != nil && r.RunID == runID {
			m.runs.SetCursor(row)
			m.runIndex = row
			break
		}
	}
	if m.screen == screenDetail {
		m.gotoScreen(screenRuns, "pane.runs")
		return nil
	}
	m.setFocus("pane.runs")
	m.syncRuns()
	return nil
}

// handleWheel forwards a wheel event to whichever pane it landed on. The lists scroll
// here because their window is herdr-cron's; the viewports scroll natively
// (docs/spec/06-tui.md §1.5).
func (m *Model) handleWheel(msg LayerHitMsg) tea.Cmd {
	wheel, _ := msg.Mouse.(tea.MouseWheelMsg)
	step := wheelStep
	if wheel.Button == tea.MouseWheelUp {
		step = -wheelStep
	}

	target := msg.ID
	if strings.HasPrefix(target, "row.run.") {
		target = "pane.runs"
	} else if strings.HasPrefix(target, "row.") {
		target = "pane.jobs"
	}

	switch target {
	case "pane.jobs":
		m.jobs.Move(step)
		m.syncJobs()
	case "pane.runs":
		m.runs.Move(step)
		m.runIndex = m.runs.Cursor()
		m.syncRuns()
	case "pane.detail", "pane.definition":
		scrollViewport(&m.detail, step)
	case "pane.output":
		scrollViewport(&m.output, step)
	}
	return nil
}

func scrollViewport(v *viewport.Model, step int) {
	if step < 0 {
		v.ScrollUp(-step)
		return
	}
	v.ScrollDown(step)
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

func clampInt(v, low, high int) int {
	if high < low {
		low, high = high, low
	}
	return minInt(high, maxInt(low, v))
}
