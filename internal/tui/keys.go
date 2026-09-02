package tui

import "charm.land/bubbles/v2/key"

// KeyMap is the single binding table. Every mouse affordance has an entry here, because
// mouse reporting can be off, absent, or swallowed by a multiplexer
// (docs/spec/06-tui.md §1.3, §5).
type KeyMap struct {
	Up            key.Binding
	Down          key.Binding
	PageUp        key.Binding
	PageDown      key.Binding
	Top           key.Binding
	Bottom        key.Binding
	Open          key.Binding
	Back          key.Binding
	NextPane      key.Binding
	ToggleEnabled key.Binding
	RunNow        key.Binding
	Cancel        key.Binding
	Delete        key.Binding
	Copy          key.Binding
	Follow        key.Binding
	Reload        key.Binding
	MouseMode     key.Binding
	Help          key.Binding
	Confirm       key.Binding
	Quit          key.Binding
}

// DefaultKeyMap returns the bindings of docs/spec/06-tui.md §5, one per row of that
// table. Its parity rule is the reason the table is exhaustive: a clickable region added
// without a binding here is unreachable whenever mouse reporting is off (§1.3).
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:            key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/↓", "move")),
		Down:          key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓", "down")),
		PageUp:        key.NewBinding(key.WithKeys("pgup", "b"), key.WithHelp("pgup", "page up")),
		PageDown:      key.NewBinding(key.WithKeys("pgdown", "f"), key.WithHelp("pgdn", "page down")),
		Top:           key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("g", "top")),
		Bottom:        key.NewBinding(key.WithKeys("end", "G"), key.WithHelp("G", "bottom")),
		Open:          key.NewBinding(key.WithKeys("enter", "l", "right"), key.WithHelp("enter", "open")),
		Back:          key.NewBinding(key.WithKeys("esc", "h", "left"), key.WithHelp("esc", "back")),
		NextPane:      key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "pane")),
		ToggleEnabled: key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "enable/disable")),
		RunNow:        key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "run now")),
		Cancel:        key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "cancel run")),
		Delete:        key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		Copy:          key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy")),
		Follow:        key.NewBinding(key.WithKeys("F"), key.WithHelp("F", "follow")),
		Reload:        key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "reload")),
		MouseMode:     key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "mouse")),
		Help:          key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Confirm:       key.NewBinding(key.WithKeys("enter", "y"), key.WithHelp("enter", "confirm")),
		// ctrl+c must be bound explicitly: in raw mode ^C arrives as a key event, not a
		// signal. No suspend binding is advertised — it is a no-op on Windows.
		Quit: key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// ShortHelp implements help.KeyMap for the one-line footer. Quit, Help and MouseMode are
// always among them, since they are the only way out of a frame where the mouse is dead
// (docs/spec/06-tui.md §5).
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Open, k.Back, k.ToggleEnabled, k.RunNow, k.MouseMode, k.Help, k.Quit}
}

// FullHelp implements help.KeyMap for the `?` expansion, one column group per concern:
// movement, navigation, job actions, session (docs/spec/06-tui.md §5).
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom},
		{k.Open, k.Back, k.NextPane, k.Reload},
		{k.ToggleEnabled, k.RunNow, k.Cancel, k.Delete},
		{k.Copy, k.Follow, k.MouseMode, k.Help, k.Quit},
	}
}
