// Package tui is the mouse-driven terminal UI of docs/spec/06-tui.md.
//
// It owns no scheduler: quitting it, by any means, has no effect on the schedule
// (docs/spec/06-tui.md §1.1). Reads are plain file reads; writes go through the same
// packages the cobra commands call.
package tui

import (
	"fmt"
	"image"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/huketo/herdr-cron/internal/daemon"
	"github.com/huketo/herdr-cron/internal/model"
	"github.com/huketo/herdr-cron/internal/paths"
	"github.com/huketo/herdr-cron/internal/store"
)

type screen int

const (
	screenJobs screen = iota
	screenDetail
	screenRuns
)

const (
	refreshInterval   = 2 * time.Second
	clockInterval     = time.Second
	doubleClickWindow = 400 * time.Millisecond
)

// ---------------------------------------------------------------- messages

// LayerHitMsg is the single mouse entry point: every mouse consequence is funnelled
// through it so the raw mouse cases in Update stay empty (docs/spec/06-tui.md §3).
type LayerHitMsg struct {
	ID    string
	Mouse tea.MouseMsg
}

type snapshotMsg Snapshot
type clockMsg time.Time
type runsLoadedMsg struct {
	jobID string
	runs  []*model.Run
}
type actionDoneMsg struct {
	what string
	err  error
}

// ---------------------------------------------------------------- the model

// Model is the whole UI. Exactly one screen is active; the modal composes over it.
type Model struct {
	roots paths.Roots
	keys  KeyMap
	help  help.Model

	width, height int
	screen        screen
	mouseOn       bool
	showFullHelp  bool

	snap     Snapshot
	jobs     table.Model
	detail   viewport.Model
	runs     table.Model
	output   viewport.Model
	runList  []*model.Run
	selected string // job id
	runIndex int

	focus    string // pane id holding keyboard focus
	modal    *modal
	status   string
	statusAt time.Time

	lastClickID string
	lastClickAt time.Time

	hits []hitRect
}

// hitRect is one clickable rectangle in screen coordinates.
type hitRect struct {
	ID   string
	Rect image.Rectangle
	Z    int
}

type modal struct {
	title   string
	body    string
	confirm string
	danger  bool
	action  func(*Model) tea.Cmd
}

// New builds the model. It reads the store once so the first frame is already populated.
func New(roots paths.Roots) *Model {
	m := &Model{
		roots:   roots,
		keys:    DefaultKeyMap(),
		help:    help.New(),
		mouseOn: true,
		focus:   "pane.jobs",
	}
	m.jobs = table.New(table.WithFocused(true))
	m.runs = table.New()
	m.detail = viewport.New()
	m.output = viewport.New()
	// Columns must exist before rows: table.SetRows renders immediately and indexes the
	// column slice.
	m.width, m.height = 80, 24
	m.layout()
	m.snap = Load(roots)
	m.syncJobs()
	return m
}

// Run starts the program. The alt screen is a declarative field on the view in v2, so
// there is no option to pass here.
func Run(roots paths.Roots) error {
	p := tea.NewProgram(New(roots))
	_, err := p.Run()
	return err
}

// Init starts the only two sources of change the TUI has: one store read and the clock.
// There is nothing to subscribe to — D6 leaves no socket, so the daemon's progress is
// visible only through files (docs/spec/06-tui.md §4.1).
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.refresh(), tick())
}

func tick() tea.Cmd {
	return tea.Every(clockInterval, func(t time.Time) tea.Msg { return clockMsg(t) })
}

func (m *Model) refresh() tea.Cmd {
	roots := m.roots
	return func() tea.Msg { return snapshotMsg(Load(roots)) }
}

func (m *Model) loadRuns(jobID string) tea.Cmd {
	roots := m.roots
	return func() tea.Msg {
		return runsLoadedMsg{jobID: jobID, runs: Runs(roots, jobID)}
	}
}

// ---------------------------------------------------------------- update

// Update is the single reducer for the whole app; every screen is a case, never a nested
// tea.Model with its own loop. It returns tea.Model to satisfy the interface, but the
// value is always this same *Model — the state is mutated in place.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Delivered on all three platforms; never special-cased for Windows.
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case clockMsg:
		// The countdown column ticks every second; the store is re-read less often.
		var cmds []tea.Cmd
		cmds = append(cmds, tick())
		if time.Since(m.snap.At) >= refreshInterval {
			cmds = append(cmds, m.refresh())
		}
		m.syncJobs()
		return m, tea.Batch(cmds...)

	case snapshotMsg:
		m.snap = Snapshot(msg)
		m.syncJobs()
		if m.selected != "" {
			return m, m.loadRuns(m.selected)
		}
		return m, nil

	case runsLoadedMsg:
		if msg.jobID == m.selected {
			m.runList = msg.runs
			m.syncRuns()
		}
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			m.setStatus("✗ " + msg.what + ": " + msg.err.Error())
		} else {
			m.setStatus("✓ " + msg.what)
		}
		return m, m.refresh()

	case LayerHitMsg:
		return m, m.handleHit(msg)

	case tea.KeyPressMsg:
		return m, m.handleKey(msg)

	case tea.MouseMsg:
		// Deliberately empty: mouse consequences arrive as LayerHitMsg, dispatched from
		// View.OnMouse, and the two are not guaranteed to arrive adjacently because
		// OnMouse's command is dispatched asynchronously (docs/spec/06-tui.md §3).
		return m, nil
	}
	return m, nil
}

func (m *Model) setStatus(s string) {
	m.status = s
	m.statusAt = time.Now()
}

// ---------------------------------------------------------------- keys

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.modal != nil {
		switch {
		case key.Matches(msg, m.keys.Confirm):
			action := m.modal.action
			m.modal = nil
			if action != nil {
				return action(m)
			}
			return nil
		case key.Matches(msg, m.keys.Back), key.Matches(msg, m.keys.Quit):
			m.modal = nil
			return nil
		}
		return nil
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		return tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.showFullHelp = !m.showFullHelp
		m.layout()
		return nil
	case key.Matches(msg, m.keys.MouseMode):
		m.mouseOn = !m.mouseOn
		m.setStatus(fmt.Sprintf("mouse %s — native selection %s",
			onOff(m.mouseOn), map[bool]string{true: "disabled", false: "available"}[m.mouseOn]))
		return nil
	case key.Matches(msg, m.keys.Reload):
		return m.refresh()
	}

	switch m.screen {
	case screenJobs:
		return m.keyJobs(msg)
	case screenDetail:
		return m.keyDetail(msg)
	default:
		return m.keyRuns(msg)
	}
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (m *Model) keyJobs(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, m.keys.Open):
		if id := m.cursorJob(); id != "" {
			return m.openJob(id)
		}
		return nil
	case key.Matches(msg, m.keys.ToggleEnabled):
		return m.toggleEnabled(m.cursorJob())
	case key.Matches(msg, m.keys.RunNow):
		return m.runNow(m.cursorJob())
	case key.Matches(msg, m.keys.Cancel):
		return m.cancelRun(m.cursorJob())
	}
	var cmd tea.Cmd
	m.jobs, cmd = m.jobs.Update(msg)
	m.syncDetail()
	return cmd
}

func (m *Model) keyDetail(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, m.keys.Back):
		m.screen = screenJobs
		m.focus = "pane.jobs"
		return nil
	case key.Matches(msg, m.keys.Open):
		if len(m.runList) > 0 {
			m.screen = screenRuns
			m.focus = "pane.runs"
			m.syncOutput()
		}
		return nil
	case key.Matches(msg, m.keys.ToggleEnabled):
		return m.toggleEnabled(m.selected)
	case key.Matches(msg, m.keys.RunNow):
		return m.runNow(m.selected)
	case key.Matches(msg, m.keys.Cancel):
		return m.cancelRun(m.selected)
	case key.Matches(msg, m.keys.Delete):
		m.openDeleteModal(m.selected)
		return nil
	}
	var cmd tea.Cmd
	if m.focus == "pane.runs" {
		m.runs, cmd = m.runs.Update(msg)
		m.runIndex = m.runs.Cursor()
	} else {
		m.detail, cmd = m.detail.Update(msg)
	}
	return cmd
}

func (m *Model) keyRuns(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, m.keys.Back):
		m.screen = screenDetail
		m.focus = "pane.runs"
		return nil
	case key.Matches(msg, m.keys.RunNow):
		return m.runNow(m.selected)
	case key.Matches(msg, m.keys.Copy):
		return m.copyOutput()
	}
	var cmd tea.Cmd
	if m.focus == "pane.output" {
		m.output, cmd = m.output.Update(msg)
		return cmd
	}
	m.runs, cmd = m.runs.Update(msg)
	m.runIndex = m.runs.Cursor()
	m.syncOutput()
	return cmd
}

// ---------------------------------------------------------------- actions

func (m *Model) cursorJob() string {
	i := m.jobs.Cursor()
	if i < 0 || i >= len(m.snap.Jobs) {
		return ""
	}
	return m.snap.Jobs[i].Job.ID
}

func (m *Model) openJob(id string) tea.Cmd {
	m.selected = id
	m.screen = screenDetail
	m.focus = "pane.definition"
	m.runIndex = 0
	m.syncDetail()
	return m.loadRuns(id)
}

// toggleEnabled writes overrides.json under its lock, never jobs.yaml
// (docs/spec/03-job-model.md §5).
func (m *Model) toggleEnabled(id string) tea.Cmd {
	if id == "" {
		return nil
	}
	v, ok := m.snap.Job(id)
	if !ok {
		return nil
	}
	roots, want, declared := m.roots, !v.Enabled, v.Job.Enabled
	verb := map[bool]string{true: "resumed", false: "paused"}[want]
	return func() tea.Msg {
		err := store.New(roots).SetEnabled(id, want, declared, "manual")
		return actionDoneMsg{what: id + " " + verb, err: err}
	}
}

// runNow writes a trigger file; with no daemon the CLI's daemon_unreachable surfaces here
// as a status line (docs/spec/04-storage.md §8).
func (m *Model) runNow(id string) tea.Cmd {
	if id == "" {
		return nil
	}
	if v, ok := m.snap.Job(id); ok && v.Running {
		m.setStatus("✗ " + id + " is already running")
		return nil
	}
	return m.trigger(id, "run", "run requested")
}

func (m *Model) cancelRun(id string) tea.Cmd {
	if id == "" {
		return nil
	}
	return m.trigger(id, "cancel", "cancel requested")
}

func (m *Model) trigger(id, action, label string) tea.Cmd {
	roots := m.roots
	return func() tea.Msg {
		tr := daemon.Trigger{ID: daemon.NewTriggerID(), CreatedAt: time.Now(),
			Action: action, JobID: id, RequestedBy: "tui"}
		path, err := daemon.WriteTrigger(roots, tr)
		if err != nil {
			return actionDoneMsg{what: label, err: err}
		}
		if _, err := daemon.AwaitTrigger(roots, tr, path, false); err != nil {
			return actionDoneMsg{what: label,
				err: fmt.Errorf("daemon_unreachable: %w", err)}
		}
		return actionDoneMsg{what: id + ": " + label}
	}
}

func (m *Model) copyOutput() tea.Cmd {
	text := m.output.GetContent()
	m.setStatus("copied the run output to the clipboard")
	// The designed answer to mouse mode killing native selection: an explicit copy,
	// not "turn the mouse off and drag" (docs/spec/06-tui.md §6.3).
	return tea.SetClipboard(text)
}

func (m *Model) openDeleteModal(id string) {
	if id == "" {
		return
	}
	m.modal = &modal{
		title:   "Delete job",
		body:    "Remove " + id + " from jobs.yaml?\nIts history and logs are kept.",
		confirm: "delete",
		danger:  true,
		action:  func(mm *Model) tea.Cmd { return mm.deleteJob(id) },
	}
}
