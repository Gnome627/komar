package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// hit is a spot on the last drawn screen that does something when clicked:
// a tab, the arrows around the resource kind, the context in the top bar.
type hit struct {
	x, y, w int
	do      func(m *Model) tea.Cmd
}

func (m *Model) addHit(x, y, w int, do func(m *Model) tea.Cmd) {
	m.hits = append(m.hits, hit{x, y, w, do})
}

// doubleClick is how soon a second click on the same line counts as one.
const doubleClick = 400 * time.Millisecond

// panelAt finds the panel under a cell and the line the panel starts on.
func (m *Model) panelAt(x, y int) (panel, top int, ok bool) {
	g := m.layout()
	y -= chromeH - 1
	if y < 0 || y >= g.bodyH || x < 0 || x >= m.w {
		return 0, 0, false
	}
	if x >= g.leftW {
		return fMain, chromeH - 1, g.rightW > 0
	}
	top = chromeH - 1
	for _, p := range []struct{ id, h int }{{fCtx, g.ctxH}, {fNs, g.nsH}, {fRes, g.resH}, {fRel, g.relH}} {
		if y < p.h {
			return p.id, top, true
		}
		y -= p.h
		top += p.h
	}
	return 0, 0, false
}

// handleClick is the mouse doing what the keys do: a click focuses a panel
// and selects the row under it, a second one acts like enter.
func (m *Model) handleClick(msg tea.MouseClickMsg) tea.Cmd {
	if m.splash != nil {
		m.splash = nil
		return nil
	}
	if msg.Button != tea.MouseLeft {
		return nil
	}
	x, y := msg.X, msg.Y
	double := y == m.clickY && time.Since(m.clickAt) < doubleClick
	m.clickY, m.clickAt = y, time.Now()
	if double {
		m.clickAt = time.Time{} // a third click starts over
	}
	if m.modal != nil {
		return m.clickModal(x, y)
	}
	if m.cmd != nil || m.prompt != nil {
		return nil
	}
	for _, h := range m.hits {
		if y == h.y && x >= h.x && x < h.x+h.w {
			return h.do(m)
		}
	}
	panel, top, ok := m.panelAt(x, y)
	if !ok {
		return nil
	}
	enter := func() tea.Cmd { return m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) }
	// Lists start under the border; tables have a header line as well.
	pick := func(l *listState, n, first int) bool {
		i := l.offset + y - top - first
		if y < top+first || i < 0 || i >= n {
			return false
		}
		l.cursor = i
		return true
	}
	var cmds []tea.Cmd
	onRow := false
	switch panel {
	case fCtx:
		onRow = pick(&m.ctxList, len(m.contexts), 1)
	case fNs:
		onRow = pick(&m.nsList, len(m.nsItems), 1)
	case fRes:
		onRow = m.resTable != nil && m.resErr == "" && pick(&m.resList, len(m.resRows()), 2)
	case fRel:
		onRow = m.relMode != relNone && m.relErr == "" && pick(&m.relList, m.relLen(), 2)
	case fMain:
		if e := &m.events; m.tab == tabEvents && e.err == "" && y-top-2 < e.shown {
			if i := e.offset + y - top - 2; y >= top+2 && i < len(e.groups) {
				e.cursor, onRow = i, true
			}
		}
	}
	if panel != m.focus {
		cmds = append(cmds, m.setFocus(panel))
	} else if onRow && (panel == fRes || panel == fRel) {
		cmds = append(cmds, m.onSelectionChanged())
	}
	if onRow && double {
		cmds = append(cmds, enter())
	}
	return tea.Batch(cmds...)
}

// clickModal picks an item of a picker; a click outside a modal closes it,
// the same as esc.
func (m *Model) clickModal(x, y int) tea.Cmd {
	w, h := lipgloss.Size(m.modal.view(m))
	left, top := max((m.w-w)/2, 0), max((m.h-h)/2, 0)
	if x < left || x >= left+w || y < top || y >= top+h {
		m.modal = nil
		return nil
	}
	p, ok := m.modal.(*pickerModal)
	if !ok {
		return nil
	}
	// Border, blank line, filter, blank line, then the items.
	i := p.offset + y - top - 4
	if y < top+4 || i >= min(len(p.filtered), p.offset+p.rows(m)) {
		return nil
	}
	m.modal = nil
	return p.onPick(m, p.filtered[i])
}

// handleWheel moves lists one item per wheel step and scrolls text three
// lines.
func (m *Model) handleWheel(msg tea.MouseWheelMsg) tea.Cmd {
	d := 1
	if msg.Button == tea.MouseWheelUp {
		d = -1
	}
	switch p := m.modal.(type) {
	case *pickerModal:
		p.cursor = min(max(p.cursor+d, 0), max(len(p.filtered)-1, 0))
		return nil
	case *helpModal:
		p.top = max(p.top+3*d, 0)
		return nil
	}
	if m.modal != nil || m.splash != nil {
		return nil
	}
	// The wheel scrolls what is under the pointer, not what has the focus.
	panel, _, ok := m.panelAt(msg.X, msg.Y)
	if !ok {
		panel = m.focus
	}
	switch panel {
	case fMain:
		if m.tab == tabEvents || m.tab == tabRollout {
			m.scrollMain(d) // these tabs are lists
		} else {
			m.scrollMain(3 * d)
		}
	case fCtx:
		m.ctxList.move(d, len(m.contexts))
	case fNs:
		m.nsList.move(d, len(m.nsItems))
	case fRes:
		before := m.resList.cursor
		m.resList.move(d, len(m.resRows()))
		if m.resList.cursor != before {
			return m.onSelectionChanged()
		}
	case fRel:
		before := m.relList.cursor
		m.relList.move(d, m.relLen())
		if m.relList.cursor != before {
			return m.loadTab(false)
		}
	}
	return nil
}
