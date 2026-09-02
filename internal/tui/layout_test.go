package tui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/huketo/herdr-cron/internal/paths"
)

// screens is every screen a frame can be rendered from, with the pane that holds
// keyboard focus when it opens.
var screens = []struct {
	name  string
	s     screen
	focus string
}{
	{"jobs", screenJobs, "pane.jobs"},
	{"detail", screenDetail, "pane.definition"},
	{"runs", screenRuns, "pane.runs"},
}

// onScreen puts the model on one screen the way the transitions do, so the widgets are
// sized for the screen that is about to be rendered.
func onScreen(m *Model, s screen, focus string) {
	m.selected = "nightly-deps"
	m.gotoScreen(s, focus)
}

// A frame must fill its terminal exactly and close every box it opens. lipgloss v2 counts
// the border inside Width and Height, so a pane sized as though the border sat outside is
// two cells short — and a widget taller than its pane pushes the bottom border out of the
// frame, which is how screen 2 came to render two panes with no bottom edge.
func TestEveryScreenFillsItsFrame(t *testing.T) {
	const w, h = 110, 32
	m := newSized(t, w, h)

	for _, sc := range screens {
		onScreen(m, sc.s, sc.focus)
		content := m.View().Content
		lines := strings.Split(content, "\n")
		if len(lines) != h {
			t.Errorf("%s: frame is %d rows, want %d", sc.name, len(lines), h)
		}
		for i, line := range lines {
			if got := lipgloss.Width(line); got != w {
				t.Errorf("%s: line %d is %d cells, want %d: %q", sc.name, i, got, w, line)
			}
		}
		if got := strings.Count(content, "╰"); got != 2 {
			t.Errorf("%s: %d panes are closed at the bottom, want 2:\n%s", sc.name, got, content)
		}
	}
}

// Every widget must be exactly as large as the pane that draws it, less the scroll-hint
// row. A viewport sized larger reports its content as fully visible, so no key and no
// wheel event scrolls it, and the box clips the rows below the fold away.
func TestWidgetsMatchTheirPanes(t *testing.T) {
	m := newSized(t, 110, 32)

	check := func(name string, gotW, gotH int, r image.Rectangle) {
		w, h := innerSize(r)
		if gotW != w || gotH != h-hintRows {
			t.Errorf("%s is %dx%d, want %dx%d", name, gotW, gotH, w, h-hintRows)
		}
	}

	onScreen(m, screenJobs, "pane.jobs")
	p := m.panes()
	check("job list", m.jobs.width, m.jobs.height, p.first)
	check("detail viewport", m.detail.Width(), m.detail.Height(), p.second)

	onScreen(m, screenDetail, "pane.definition")
	p = m.panes()
	check("definition viewport", m.detail.Width(), m.detail.Height(), p.first)
	check("run list", m.runs.width, m.runs.height, p.second)

	onScreen(m, screenRuns, "pane.runs")
	p = m.panes()
	check("run list", m.runs.width, m.runs.height, p.first)
	check("output viewport", m.output.Width(), m.output.Height(), p.second)
}

// The pane that holds focus is the pane the movement keys move, on every screen, and
// `tab` is what moves focus. Without it the job list consumed every key press and the
// detail pane beside it could not be scrolled at all.
func TestTabCyclesFocusAndMovesTheFocusedPane(t *testing.T) {
	m := newSized(t, 110, 14) // short enough that the definition overflows its pane

	press := func(code rune, text string) {
		updated, _ := m.Update(tea.KeyPressMsg{Code: code, Text: text})
		m = asModel(t, updated)
	}
	tab := func() {
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		m = asModel(t, updated)
	}

	if m.focus != "pane.jobs" {
		t.Fatalf("the job list does not start focused: %q", m.focus)
	}
	press('j', "j")
	if m.jobs.Cursor() != 1 {
		t.Fatalf("j left the job cursor at %d", m.jobs.Cursor())
	}

	tab()
	if m.focus != "pane.detail" {
		t.Fatalf("tab moved focus to %q, want pane.detail", m.focus)
	}
	press('j', "j")
	if m.jobs.Cursor() != 1 {
		t.Error("j moved the job cursor while the detail pane held focus")
	}
	if m.detail.YOffset() == 0 {
		t.Error("j did not scroll the focused detail pane")
	}

	tab()
	if m.focus != "pane.jobs" {
		t.Fatalf("tab did not return focus to the job list: %q", m.focus)
	}
}

// The definition pane must scroll from the keyboard and from the wheel, and reach its
// last line: it is where a job's prompt is read.
func TestDefinitionPaneScrolls(t *testing.T) {
	m := newSized(t, 110, 14)
	m.selected = "nightly-deps"
	m.gotoScreen(screenDetail, "pane.definition")

	if m.detail.AtBottom() {
		t.Fatal("the definition already fits its pane; the test cannot show scrolling")
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	m = asModel(t, updated)
	if m.detail.YOffset() == 0 {
		t.Error("pgdown did not scroll the definition pane")
	}

	before := m.detail.YOffset()
	updated, _ = m.Update(LayerHitMsg{ID: "pane.definition",
		Mouse: tea.MouseWheelMsg{Button: tea.MouseWheelDown}})
	m = asModel(t, updated)
	if m.detail.YOffset() <= before {
		t.Errorf("the wheel left the definition pane at %d", m.detail.YOffset())
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m = asModel(t, updated)
	if !m.detail.AtBottom() {
		t.Error("end did not reach the bottom of the definition")
	}
	// The last line of a shell job's definition is its command, and it must be readable.
	if !strings.Contains(m.View().Content, "echo audited") {
		t.Errorf("the bottom of the definition does not show the command:\n%s", m.View().Content)
	}
}

// A scrollable pane must say so. A pane holding more than it shows looks exactly like a
// pane with nothing more to show, which is what makes a reader conclude the keys are dead.
func TestScrollHintAppearsOnlyWhenThereIsMore(t *testing.T) {
	m := newSized(t, 110, 14)
	m.selected = "nightly-deps"
	m.gotoScreen(screenDetail, "pane.definition")

	if !strings.Contains(m.View().Content, "▼") {
		t.Errorf("no scroll hint on an overflowing definition pane:\n%s", m.View().Content)
	}

	tall := newSized(t, 110, 60)
	tall.selected = "nightly-deps"
	tall.gotoScreen(screenDetail, "pane.definition")
	if strings.Contains(tall.View().Content, "▼") {
		t.Errorf("a definition that fits its pane still shows a scroll hint:\n%s", tall.View().Content)
	}
}

// The cursor row must be visibly the cursor row. bubbles/table styled it by wrapping the
// whole row in one style, which the ANSI reset inside any coloured cell terminated: the
// row was highlighted for its first cell and plain for the rest, so pressing ↓ looked
// like nothing had happened.
func TestCursorRowIsHighlightedAcrossTheWholeRow(t *testing.T) {
	m := newSized(t, 110, 32)
	m.jobs.SetCursor(1)
	m.syncJobs()

	cursor := m.jobs.rowLine(1)
	band := strings.Count(cursor, "48;5;24")
	if band == 0 {
		t.Fatalf("the cursor row carries no cursor band: %q", cursor)
	}
	if band != len(m.jobs.cols) {
		t.Errorf("the cursor band covers %d of %d cells: %q", band, len(m.jobs.cols), cursor)
	}
	if other := m.jobs.rowLine(0); strings.Contains(other, "48;5;24") {
		t.Errorf("a row that is not the cursor carries the band: %q", other)
	}
}

// A click must land on the row the reader sees, including after the list has scrolled.
// The window is herdr-cron's own precisely so this mapping exists.
func TestRowHitsFollowTheScrolledWindow(t *testing.T) {
	m := newSizedWithJobs(t, 110, 16, 20)
	visible := m.jobs.Visible()
	if visible >= 20 {
		t.Fatalf("the list shows %d rows; the test needs it to scroll", visible)
	}

	m.jobs.GotoBottom()
	m.syncJobs()
	if m.jobs.Top() == 0 {
		t.Fatal("the list did not scroll to its last row")
	}

	first := m.jobs.Top()
	wantFirst := "row." + m.snap.Jobs[first].Job.ID
	if got := hitAt(t, m, 20, rowY(m.panes().first, 0)+headerRows); got != wantFirst {
		t.Errorf("the top visible row answers %q, want %q", got, wantFirst)
	}
	wantLast := "row." + m.snap.Jobs[first+visible-1].Job.ID
	if got := hitAt(t, m, 20, rowY(m.panes().first, visible-1)+headerRows); got != wantLast {
		t.Errorf("the last visible row answers %q, want %q", got, wantLast)
	}
}

// `F` is advertised in the full help, so it has to do something. Following pins the
// output pane to the tail of a log that a live run is still writing.
func TestFollowTogglesAndClearsOnLeavingTheRunScreen(t *testing.T) {
	m := newSized(t, 110, 32)
	m.selected = "nightly-deps"
	m.gotoScreen(screenRuns, "pane.runs")

	press := func(code rune, text string) {
		updated, _ := m.Update(tea.KeyPressMsg{Code: code, Text: text})
		m = asModel(t, updated)
	}

	press('F', "F")
	if !m.follow {
		t.Fatal("F did not turn following on")
	}
	if !strings.Contains(m.status, "follow on") {
		t.Errorf("status = %q, want it to report following", m.status)
	}
	press('F', "F")
	if m.follow {
		t.Fatal("F did not turn following off")
	}

	press('F', "F")
	m.gotoScreen(screenDetail, "pane.definition")
	if m.follow {
		t.Error("following survived leaving the run screen, where there is nothing to follow")
	}
}

// Every mouse affordance needs a keyboard equivalent in the help bar, and the pane
// switch is the one a reader cannot guess (docs/spec/06-tui.md §1.3).
func TestShortHelpAdvertisesThePaneSwitch(t *testing.T) {
	m := newSized(t, 110, 32)

	advertised := false
	for _, b := range m.keys.ShortHelp() {
		if b.Help().Key == "tab" {
			advertised = true
		}
	}
	if !advertised {
		t.Error("the one-line help does not list the pane switch")
	}
	// The rendered footer styles the key and its description separately, so the two are
	// asserted apart.
	if content := m.View().Content; !strings.Contains(content, "pane") {
		t.Errorf("the footer does not name the pane switch:\n%s", content)
	}
}

// newSizedWithJobs builds a model whose store holds n jobs, for the cases that need the
// list to be longer than the pane.
func newSizedWithJobs(t *testing.T, w, h, n int) *Model {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HERDR_CRON_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	var b strings.Builder
	b.WriteString("version: 1\ndefaults:\n  timezone: Asia/Seoul\njobs:\n")
	for i := range n {
		fmt.Fprintf(&b, `  - id: job-%02d
    schedule: { cron: "%d 3 * * *" }
    kind: shell
    shell: { command: "echo %d" }
    cwd: %s
`, i, i%60, i, filepath.ToSlash(cwd))
	}
	if err := os.WriteFile(filepath.Join(home, "config", "jobs.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	roots, err := paths.Resolve(paths.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	model := New(roots)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return asModel(t, updated)
}
