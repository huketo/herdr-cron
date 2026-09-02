package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/huketo/herdr-cron/internal/paths"
	"github.com/huketo/herdr-cron/internal/store"
)

const fixture = `version: 1
defaults:
  timezone: Asia/Seoul
jobs:
  - id: nightly-deps
    name: Nightly dependency audit
    schedule: { cron: "17 3 * * 1-5" }
    kind: shell
    shell: { command: "echo audited" }
    cwd: /tmp
  - id: build-smoke
    schedule: { every: 30m }
    kind: shell
    shell: { command: "echo smoke" }
    cwd: /tmp
  - id: daily-report
    name: 일일보고 스케줄
    schedule: { cron: "0 18 * * 1-5" }
    kind: shell
    shell: { command: "echo 보고" }
    cwd: /tmp
`

func testRoots(t *testing.T) paths.Roots {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HERDR_CRON_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "jobs.yaml"), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	roots, err := paths.Resolve(paths.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	return roots
}

// asModel narrows what Update returns back to *Model. Update always returns its receiver,
// so a failed assertion means the reducer started handing out some other model — which
// would silently make every assertion below test a stale copy.
func asModel(t *testing.T, m tea.Model) *Model {
	t.Helper()
	got, ok := m.(*Model)
	if !ok {
		t.Fatalf("Update returned %T, want *tui.Model", m)
	}
	return got
}

// newSized builds a model at a known size, which is what makes every hit-test
// coordinate in this file deterministic.
func newSized(t *testing.T, w, h int) *Model {
	t.Helper()
	m := New(testRoots(t))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return asModel(t, updated)
}

func TestFirstFrameRendersEveryJob(t *testing.T) {
	m := newSized(t, 110, 32)
	view := m.View()

	if !view.AltScreen {
		t.Error("the TUI must run on the alt screen")
	}
	if view.MouseMode != tea.MouseModeCellMotion {
		t.Errorf("MouseMode = %v, want cell motion", view.MouseMode)
	}

	content := view.Content
	for _, want := range []string{
		"herdr-cron",
		"daemon stopped", // liveness is always visible
		"mouse: on (m)",  // the toggle advertises its key
		"Nightly depend", // job names, truncated to the column
		"build-smoke",
		"일일보고",         // a Korean name must survive layout
		"17 3 * * 1-5", // the schedule column
	} {
		if !strings.Contains(content, want) {
			t.Errorf("the first frame does not contain %q:\n%s", want, content)
		}
	}
	if got := lipgloss.Height(content); got > 32 {
		t.Errorf("the frame is %d rows tall, taller than the 32-row terminal", got)
	}
	for i, line := range strings.Split(content, "\n") {
		if w := lipgloss.Width(line); w > 110 {
			t.Errorf("line %d is %d cells wide, wider than the 110-cell terminal", i, w)
		}
	}
}

// hitAt renders, then asks the compositor what is under a cell — the same path
// View.OnMouse takes.
func hitAt(t *testing.T, m *Model, x, y int) string {
	t.Helper()
	_ = m.View()
	if len(m.hits) == 0 {
		t.Fatal("the view registered no hit rectangles")
	}
	return m.hitTest(x, y)
}

// Every clickable region named by docs/spec/06-tui.md §2.1 must answer a hit test. Hit
// arithmetic is the highest-risk, least-visible code in the TUI (§8).
func TestHitRegionsOnTheJobList(t *testing.T) {
	m := newSized(t, 110, 32)
	listW := m.listWidth()

	cases := []struct {
		name string
		x, y int
		want string
	}{
		{"header", 4, 0, "pane.header"},
		{"mouse badge", 103, 0, "hdr.mouse"},
		// Rows are sorted by id, so build-smoke is first.
		{"first row", 20, 1 + 2, "row.build-smoke"},
		{"first row toggle glyph", 2, 1 + 2, "row.build-smoke.toggle"},
		{"first row run affordance", listW - 3, 1 + 2, "row.build-smoke.run"},
		{"second row", 20, 1 + 3, "row.daily-report"},
		{"detail pane", listW + 5, 6, "pane.detail"},
		{"help footer", 4, 31, "pane.help"},
	}
	for _, tc := range cases {
		if got := hitAt(t, m, tc.x, tc.y); got != tc.want {
			t.Errorf("%s: hit(%d,%d) = %q, want %q", tc.name, tc.x, tc.y, got, tc.want)
		}
	}
}

// A click selects; a second click on the same row within the window opens the job. v2 has
// no double-click message, so this rule is herdr-cron's own (§2.1).
func TestClickSelectsAndDoubleClickOpens(t *testing.T) {
	m := newSized(t, 110, 32)
	_ = m.View()

	click := func(id string) {
		msg := LayerHitMsg{ID: id, Mouse: tea.MouseClickMsg{X: 20, Y: 3, Button: tea.MouseLeft}}
		updated, _ := m.Update(msg)
		m = asModel(t, updated)
	}

	click("row.build-smoke")
	if m.screen != screenJobs {
		t.Fatalf("one click changed the screen to %v", m.screen)
	}
	if got := m.cursorJob(); got != "build-smoke" {
		t.Fatalf("cursor is on %q after clicking build-smoke", got)
	}
	click("row.build-smoke")
	if m.screen != screenDetail {
		t.Fatal("a second click on the same row did not open the detail screen")
	}
	if m.selected != "build-smoke" {
		t.Fatalf("the detail screen opened %q", m.selected)
	}
}

// The wheel must scroll the job table: bubbles/table has no wheel handling at all, so
// this is code herdr-cron owns (§1.5).
func TestWheelScrollsTheJobTable(t *testing.T) {
	m := newSized(t, 110, 12) // short enough that three jobs do not all fit comfortably
	_ = m.View()

	down := LayerHitMsg{ID: "pane.jobs", Mouse: tea.MouseWheelMsg{Button: tea.MouseWheelDown}}
	before := m.jobs.Cursor()
	updated, _ := m.Update(down)
	m = asModel(t, updated)
	if m.jobs.Cursor() <= before {
		t.Errorf("wheel down left the cursor at %d", m.jobs.Cursor())
	}

	up := LayerHitMsg{ID: "pane.jobs", Mouse: tea.MouseWheelMsg{Button: tea.MouseWheelUp}}
	updated, _ = m.Update(up)
	m = asModel(t, updated)
	if m.jobs.Cursor() != 0 {
		t.Errorf("wheel up left the cursor at %d, want the top", m.jobs.Cursor())
	}
}

// Clicking the glyph writes overrides.json and never touches jobs.yaml
// (docs/spec/03-job-model.md §5).
func TestToggleWritesOverridesNotYAML(t *testing.T) {
	m := newSized(t, 110, 32)
	before, err := os.ReadFile(m.roots.JobsFile())
	if err != nil {
		t.Fatal(err)
	}

	msg := LayerHitMsg{ID: "row.nightly-deps.toggle",
		Mouse: tea.MouseClickMsg{X: 2, Y: 3, Button: tea.MouseLeft}}
	_, cmd := m.Update(msg)
	if cmd == nil {
		t.Fatal("clicking the toggle produced no command")
	}
	if done, ok := cmd().(actionDoneMsg); !ok {
		t.Fatalf("the toggle produced %T, want actionDoneMsg", cmd())
	} else if done.err != nil {
		t.Fatalf("the toggle failed: %v", done.err)
	}

	after, err := os.ReadFile(m.roots.JobsFile())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("the toggle rewrote jobs.yaml; it must only write overrides.json")
	}
	ov, err := store.New(m.roots).LoadOverrides()
	if err != nil {
		t.Fatal(err)
	}
	o := ov.Overrides["nightly-deps"]
	if o == nil {
		t.Fatal("no override was recorded")
	}
	if o.Enabled || o.Reason != "manual" || !o.DeclaredEnabled {
		t.Errorf("override = %+v, want enabled=false declared=true reason=manual", *o)
	}
}

// With no daemon there is nobody to claim a trigger, and the TUI must say so rather than
// pretend the run started (docs/spec/06-tui.md §1.2).
func TestRunNowWithoutADaemonReportsUnreachable(t *testing.T) {
	m := newSized(t, 110, 32)
	cmd := m.runNow("nightly-deps")
	if cmd == nil {
		t.Fatal("run now produced no command")
	}
	done, ok := cmd().(actionDoneMsg)
	if !ok {
		t.Fatalf("run now produced %T", cmd())
	}
	if done.err == nil || !strings.Contains(done.err.Error(), "daemon_unreachable") {
		t.Fatalf("err = %v, want daemon_unreachable", done.err)
	}
}

// Mouse mode is toggleable from both the badge and the key, and the badge advertises the
// key because once mouse mode is off the badge itself is unclickable (§6.2).
func TestMouseModeToggle(t *testing.T) {
	m := newSized(t, 110, 32)
	if m.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("mouse should start enabled")
	}

	msg := LayerHitMsg{ID: "hdr.mouse", Mouse: tea.MouseClickMsg{X: 100, Y: 0, Button: tea.MouseLeft}}
	updated, _ := m.Update(msg)
	m = asModel(t, updated)
	if m.View().MouseMode != tea.MouseModeNone {
		t.Fatal("clicking the badge did not disable mouse reporting")
	}
	if !strings.Contains(m.View().Content, "mouse: off(m)") {
		t.Error("the footer must still advertise the m key while mouse mode is off")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	m = asModel(t, updated)
	if m.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("the m key did not re-enable mouse reporting")
	}
}

// A broken jobs.yaml must be visible in the header and must not take the TUI down (§7.4).
func TestInvalidConfigIsVisibleNotFatal(t *testing.T) {
	m := newSized(t, 110, 32)
	if err := os.WriteFile(m.roots.JobsFile(), []byte("version: 1\njobs:\n  - id: Bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, _ := m.Update(snapshotMsg(Load(m.roots)))
	m = asModel(t, updated)
	if !strings.Contains(m.View().Content, "jobs.yaml invalid") {
		t.Errorf("the header does not report the broken file:\n%s", m.View().Content)
	}
}

// A destructive action must never happen on the click that requests it (§2.2).
func TestDeleteAsksFirst(t *testing.T) {
	m := newSized(t, 110, 32)
	m.selected = "nightly-deps"
	m.screen = screenDetail
	_ = m.View()

	updated, _ := m.Update(LayerHitMsg{ID: "btn.job.delete",
		Mouse: tea.MouseClickMsg{Button: tea.MouseLeft}})
	m = asModel(t, updated)
	if m.modal == nil {
		t.Fatal("clicking delete did not open a confirmation")
	}
	body, err := os.ReadFile(m.roots.JobsFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "nightly-deps") {
		t.Fatal("the job was deleted by the click that only asked")
	}

	// The scrim dismisses without acting.
	updated, _ = m.Update(LayerHitMsg{ID: "modal.scrim",
		Mouse: tea.MouseClickMsg{Button: tea.MouseLeft}})
	m = asModel(t, updated)
	if m.modal != nil {
		t.Error("clicking the scrim did not dismiss the modal")
	}
}
