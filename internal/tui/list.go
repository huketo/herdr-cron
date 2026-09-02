package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// column is one list column. Width is the whole cell, the one space of padding either
// side included, so the widths of a list sum to the pane's inner width exactly — which is
// what lets a click be mapped back to a column.
type column struct {
	Title string
	Width int
}

// cellText is one cell: plain text plus the colour it is rendered in.
//
// The text is kept plain and the style is applied at render time on purpose. A cell that
// arrives pre-rendered carries an ANSI reset, and any style wrapped around a whole row of
// such cells stops at the first one — which is exactly how the cursor row came to be
// invisible while the arrow keys were working perfectly.
type cellText struct {
	Text  string
	Style lipgloss.Style
}

func text(s string) cellText { return cellText{Text: s} }

// renderCell lays one cell out at exactly w cells, one space of padding either side. The
// row style is inherited into the cell style rather than wrapped around the finished
// cell, so the cursor band is continuous and a coloured cell keeps its colour.
func renderCell(c cellText, w int, row lipgloss.Style) string {
	if w <= 0 {
		return ""
	}
	return c.Style.Inherit(row).Render(pad(" "+truncate(c.Text, maxInt(0, w-2)), w))
}

// rowList is the job list and the run list: a header line, a window of rows, and a
// cursor.
//
// bubbles/table is deliberately not used. Its Update keeps a private start/end window
// plus an internal viewport offset, so the index of its first visible row cannot be read
// back, and without that index a click cannot be mapped to a row once the list has
// scrolled. It also renders a row by wrapping the joined cells in one selection style
// (`[BB table/table.go:431-449]`), which the ANSI reset inside any coloured cell
// terminates. Owning the window, the cursor and the cell styling removes both problems
// (docs/spec/06-tui.md §1.5).
type rowList struct {
	cols    []column
	rows    [][]cellText
	cursor  int
	top     int
	width   int
	height  int // total rows, the header line included
	focused bool
}

// ColumnWidth is the width of the nth column; a negative n counts from the end, so the
// trailing affordance column can be addressed without knowing the column count.
func (l *rowList) ColumnWidth(n int) int {
	if n < 0 {
		n += len(l.cols)
	}
	if n < 0 || n >= len(l.cols) {
		return 0
	}
	return l.cols[n].Width
}

func (l *rowList) SetColumns(cols []column) { l.cols = cols }

// SetSize takes the pane's inner size, the header line included in h.
func (l *rowList) SetSize(w, h int) {
	l.width, l.height = maxInt(1, w), maxInt(1, h)
	l.clamp()
}

// Visible is the number of data rows the window shows: the header line is not one.
func (l *rowList) Visible() int { return maxInt(0, l.height-1) }

func (l *rowList) SetRows(rows [][]cellText) {
	l.rows = rows
	l.clamp()
}

func (l *rowList) Len() int    { return len(l.rows) }
func (l *rowList) Cursor() int { return l.cursor }

// Top is the index of the first visible row, the origin every row hit rectangle is
// measured from.
func (l *rowList) Top() int { return l.top }

func (l *rowList) SetCursor(i int) {
	l.cursor = i
	l.clamp()
}

func (l *rowList) Move(n int) {
	l.cursor += n
	l.clamp()
}

func (l *rowList) GotoTop() {
	l.cursor = 0
	l.clamp()
}

func (l *rowList) GotoBottom() {
	l.cursor = len(l.rows) - 1
	l.clamp()
}

func (l *rowList) Focus(on bool) { l.focused = on }

// AtTop and AtBottom report whether the window can move, which is what the scroll hint
// renders.
func (l *rowList) AtTop() bool    { return l.top == 0 }
func (l *rowList) AtBottom() bool { return l.top+l.Visible() >= len(l.rows) }

// clamp keeps the cursor inside the rows and the window around the cursor.
func (l *rowList) clamp() {
	l.cursor = clampInt(l.cursor, 0, maxInt(0, len(l.rows)-1))
	vis := l.Visible()
	if vis == 0 {
		l.top = 0
		return
	}
	l.top = clampInt(l.top, 0, maxInt(0, len(l.rows)-vis))
	if l.cursor < l.top {
		l.top = l.cursor
	}
	if l.cursor >= l.top+vis {
		l.top = l.cursor - vis + 1
	}
}

// View renders the header line and the window: exactly height lines, each width cells.
func (l *rowList) View() string {
	lines := make([]string, 0, l.height)
	lines = append(lines, l.headerLine())
	for i := l.top; i < len(l.rows) && i < l.top+l.Visible(); i++ {
		lines = append(lines, l.rowLine(i))
	}
	for len(lines) < l.height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (l *rowList) headerLine() string {
	cells := make([]string, 0, len(l.cols))
	for _, c := range l.cols {
		cells = append(cells, renderCell(text(c.Title), c.Width, styleListHeader))
	}
	return strings.Join(cells, "")
}

func (l *rowList) rowLine(i int) string {
	row := lipgloss.NewStyle()
	if i == l.cursor {
		row = styleCursorBlur
		if l.focused {
			row = styleCursor
		}
	}
	cells := make([]string, 0, len(l.cols))
	for j, c := range l.cols {
		var cell cellText
		if j < len(l.rows[i]) {
			cell = l.rows[i][j]
		}
		cells = append(cells, renderCell(cell, c.Width, row))
	}
	return strings.Join(cells, "")
}
