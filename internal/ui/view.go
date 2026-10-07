package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/gnome627/komar/internal/i18n"
	"github.com/gnome627/komar/internal/kube"
)

func lipWidth(s string) int { return ansi.StringWidth(s) }

type geom struct {
	leftW, rightW, bodyH  int
	ctxH, nsH, resH, relH int // 0 hides the panel
}

// chromeH is the lines around the panels: the top bar, an empty line that
// keeps the panels from sticking to it, and the bottom bar.
const chromeH = 3

// Below these sizes there is no room for everything at once.
const (
	minW, minH = 20, 6
	twoColumnW = 80 // narrower windows show one column, the focused one
)

// single reports whether the window is too narrow for two columns: the
// focused side then takes the whole width.
func (m *Model) single() bool { return m.w < twoColumnW }

func (m *Model) layout() geom {
	g := geom{bodyH: max(m.h-chromeH, 1)}
	switch {
	case !m.single():
		g.leftW = min(max(m.w*36/100, 34), 64)
		g.rightW = m.w - g.leftW
	case m.focus == fMain:
		g.rightW = m.w
	default:
		g.leftW = m.w
	}

	n := max(len(m.contexts), 1)
	if m.focus == fCtx {
		g.ctxH = min(n+2, max(g.bodyH/3, 3))
	} else {
		g.ctxH = min(n+2, 5)
	}
	n = max(len(m.nsItems), 1)
	if m.focus == fNs {
		g.nsH = min(n+2, max(g.bodyH/3, 3))
	} else {
		g.nsH = min(n+2, 6)
	}
	// What the resources and their related list need to stay readable.
	const minRes = 5
	minRel := 3
	if m.relMode != relNone {
		minRel = 5
	}
	// In a short window the context and namespace panels give way first:
	// both are in the top bar anyway, and 1 and 2 bring them back.
	if g.bodyH-g.ctxH-g.nsH < minRes+minRel {
		if m.focus != fCtx {
			g.ctxH = 0
		}
		if m.focus != fNs {
			g.nsH = 0
		}
	}
	rest := g.bodyH - g.ctxH - g.nsH
	switch {
	case rest < minRes && g.ctxH > 0:
		g.ctxH, rest = g.bodyH, 0
	case rest < minRes && g.nsH > 0:
		g.nsH, rest = g.bodyH, 0
	}
	switch {
	case rest == 0:
	case rest < minRes+minRel && m.focus == fRel:
		g.relH = rest
	case rest < minRes+minRel:
		g.resH = rest
	default:
		g.relH = minRel
		if m.relMode != relNone {
			g.relH = max(rest*2/5, minRel)
			if m.focus == fRel {
				g.relH = max(rest/2, minRel)
			}
		}
		g.relH = min(g.relH, rest-minRes)
		g.resH = rest - g.relH
	}
	return g
}

func (m *Model) resListHeight() int { return max(m.layout().resH-3, 1) }
func (m *Model) relListHeight() int { return max(m.layout().relH-3, 1) }

// View renders the whole screen.
func (m *Model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "komar — " + m.ctxName
	if m.ns != "" {
		v.WindowTitle += "/" + m.ns
	}
	if !m.ready || m.w < minW || m.h < minH {
		v.SetContent("komar…")
		return v
	}
	if m.splash != nil {
		if m.splash.W != m.w || m.splash.H != m.h {
			m.splash = nil
		} else {
			v.SetContent(m.splash.Render())
			return v
		}
	}
	g := m.layout()
	m.hits = m.hits[:0]
	var left []string
	if g.leftW > 0 {
		for _, p := range []struct {
			h      int
			render func(w, h int) string
		}{
			{g.ctxH, m.renderCtxPanel},
			{g.nsH, m.renderNsPanel},
			{g.resH, func(w, h int) string { return m.panelOrAnim(fRes, w, h, m.renderResPanel) }},
			{g.relH, func(w, h int) string { return m.panelOrAnim(fRel, w, h, m.renderRelPanel) }},
		} {
			if p.h > 0 {
				left = append(left, p.render(g.leftW, p.h))
			}
		}
	}
	right := ""
	if g.rightW > 0 {
		right = m.renderMain(g.rightW, g.bodyH)
	}
	body := joinColumns(strings.Join(left, "\n"), right, g.leftW, g.rightW, g.bodyH)
	screen := m.renderTopBar() + "\n" + strings.Repeat(" ", m.w) + "\n" + body + "\n" + m.renderBottomBar()
	if g.leftW > 0 && g.resH > 0 {
		// ‹ Pods › in the title of the resources panel, after "╭─ [3] ".
		y, name := chromeH-1+g.ctxH+g.nsH, lipWidth(m.kind().Name)
		key := func(r rune) func(m *Model) tea.Cmd {
			return func(m *Model) tea.Cmd {
				return tea.Batch(m.setFocus(fRes), m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)}))
			}
		}
		m.addHit(7, y, 2, key('['))
		m.addHit(9, y, name, func(m *Model) tea.Cmd { return m.openKindPicker() })
		m.addHit(9+name, y, 2, key(']'))
	}

	if m.cmd != nil {
		popup, _ := m.cmd.view(m, m.w)
		if len(popup) > 0 {
			screen = overlayAt(screen, strings.Join(popup, "\n"), min(11, max(m.w-lipWidth(popup[0]), 0)), m.h-1-len(popup))
		}
	}
	if m.modal != nil {
		screen = overlay(screen, m.modal.view(m), m.w, m.h)
	}
	v.SetContent(screen)
	return v
}

// joinColumns puts two blocks side by side line by line.
func joinColumns(left, right string, lw, rw, h int) string {
	ll := strings.Split(left, "\n")
	rl := strings.Split(right, "\n")
	var b strings.Builder
	for i := 0; i < h; i++ {
		l, r := "", ""
		if i < len(ll) {
			l = ll[i]
		}
		if i < len(rl) {
			r = rl[i]
		}
		b.WriteString(fit(l, lw))
		b.WriteString(fit(r, rw))
		if i < h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (m *Model) panelOrAnim(id, w, h int, render func(w, h int) string) string {
	return m.overlayDying(id, w, h, render(w, h))
}

// --- top bar -------------------------------------------------------------

func (m *Model) renderTopBar() string {
	// Each level gives up something less important than the context and
	// the namespace; the last one shortens those two as well.
	const levels = 6
	sep := m.st.Muted.Render("  ")
	for level := 0; level < levels; level++ {
		left, r := m.topBar(level)
		l := strings.Join(left, sep)
		if gap := m.w - lipWidth(l) - lipWidth(r); gap >= 1 || level == levels-1 {
			// The context and the namespace open their pickers on a click.
			x := lipWidth(left[0]) + 2
			m.addHit(x, 0, lipWidth(left[1]), func(m *Model) tea.Cmd { m.modal = m.contextPicker(); return nil })
			x += lipWidth(left[1]) + 2
			m.addHit(x, 0, lipWidth(left[2]), func(m *Model) tea.Cmd { m.modal = m.namespacePicker(); return nil })
			if gap < 1 {
				return fit(l, m.w)
			}
			return l + strings.Repeat(" ", gap) + r
		}
	}
	return ""
}

// topBar builds the two halves of the top bar. Higher levels fit narrower
// windows: 1 drops the user, 2 the server version, 3 the usage meters,
// 4 shortens the names to what is left, 5 drops the clock for them.
func (m *Model) topBar(level int) ([]string, string) {
	s := m.st
	sep := s.Muted.Render("  ")

	var right []string
	if m.usage != nil && level < 3 {
		right = append(right, s.Dim.Render(i18n.T("top.cpu")+" ")+s.meter(m.usage.CPUPercent, 5)+s.Text.Render(fmt.Sprintf(" %2.0f%%", m.usage.CPUPercent)))
		right = append(right, s.Dim.Render(i18n.T("top.mem")+" ")+s.meter(m.usage.MemPercent, 5)+s.Text.Render(fmt.Sprintf(" %2.0f%%", m.usage.MemPercent)))
	}
	if m.warnings > 0 {
		right = append(right, s.Red.Render(fmt.Sprintf("⚠ %d", m.warnings)))
	} else if m.events.loaded {
		right = append(right, s.Green.Render("✓"))
	}
	if level < 5 {
		right = append(right, s.Text.Render(time.Now().Format("15:04")))
	}
	r := strings.Join(right, sep) + " "

	ctxName, ns := m.ctxName, m.ns
	if ns == "" {
		ns = i18n.T("ns.all")
	}
	connecting := ""
	if m.connecting != "" && level < 4 {
		connecting = i18n.T("status.connecting", m.connecting)
	}
	if level >= 4 {
		// " ▍komar  ⎈ ctx  ◆ ns" plus the gap before the right half.
		room := m.w - lipWidth(r) - 18
		nsW := min(lipWidth(ns), max(room/2, 6))
		ns = shorten(ns, nsW)
		ctxName = shorten(ctxName, max(room-nsW, 6))
	}
	icon := s.Green.Render("⎈")
	switch {
	case m.connecting != "":
		icon = s.Yellow.Render("◌")
	case m.connErr != "":
		icon = s.Red.Render("✗")
	}
	ctx := icon + " " + s.Bright.Render(ctxName)
	if m.version != "" && level < 2 {
		ctx += s.Dim.Render(" " + m.version)
	}
	left := []string{s.AccentBold.Render(" ▍komar"), ctx, s.Accent.Render("◆ ") + s.Text.Render(ns)}
	if m.user != "" && level < 1 {
		left = append(left, s.Dim.Render("@"+m.user))
	}
	if connecting != "" {
		left = append(left, s.Yellow.Render(connecting))
	}
	return left, r
}

// --- bottom bar ----------------------------------------------------------

func (m *Model) renderBottomBar() string {
	s := m.st
	if m.cmd != nil {
		_, line := m.cmd.view(m, m.w)
		return fit(line, m.w)
	}
	if m.prompt != nil {
		label := s.AccentBold.Render(" " + m.prompt.label + " ")
		return fit(label+m.prompt.edit.View(s, m.w-lipWidth(label)-1), m.w)
	}
	if m.status != "" && time.Since(m.statusAt) < 6*time.Second {
		st := s.Green
		if m.statusErr {
			st = s.Red
		}
		return fit(" "+st.Render(m.status), m.w)
	}
	return fit(" "+m.hints(), m.w)
}

func (m *Model) hints() string {
	s := m.st
	k := func(key, desc string) string { return s.Key.Render(key) + " " + s.KeyDesc.Render(desc) }
	ru := i18n.Current() == i18n.RU
	tr := func(en, rus string) string {
		if ru {
			return rus
		}
		return en
	}
	var parts []string
	back := k("esc", tr("back", "назад"))
	if m.single() {
		// A narrow window shows one column: say how to get to the other.
		if m.focus == fMain {
			parts = append(parts, back)
		} else {
			parts = append(parts, k("5", i18n.T(tabKeys[m.tab])))
		}
	}
	switch m.focus {
	case fCtx, fNs:
		parts = append(parts, k("enter", tr("switch", "переключить")), k("/", tr("filter", "фильтр")), k("C N", tr("pickers", "выбор")))
	case fRes, fRel:
		t := m.target()
		if t.kind.Resource == "pods" {
			parts = append(parts, k("enter", tr("shell", "шелл")), k("b", "debug"))
		} else if m.relMode != relNone && m.focus == fRes {
			parts = append(parts, k("enter", tr("open", "открыть")))
		}
		parts = append(parts, k("d", tr("delete", "удалить")))
		if t.kind.Scalable() {
			parts = append(parts, k("s +-", tr("scale", "реплики")))
		}
		if t.kind.Rollable() {
			parts = append(parts, k("r", "restart"), k("u", tr("history", "история")))
		}
		parts = append(parts, k("l", tr("logs", "логи")), k("e", tr("events", "события")))
		if m.focus == fRes {
			parts = append(parts, k("[ ]", tr("kind", "тип")))
		}
	case fMain:
		switch m.tab {
		case tabLogs:
			parts = []string{k("/", tr("search", "поиск")), k("n N", tr("match", "совпад.")), k("m", tr("mode", "режим")), k("f", tr("follow", "следить")), k("p", "previous"), k("t", tr("since", "период")), k("L", "selector")}
		case tabEvents:
			parts = []string{k("w", "warning"), k("a", tr("all ns", "все ns")), k("s", tr("sort", "сорт.")), k("o", tr("group", "группа")), k("enter", tr("jump", "перейти"))}
		case tabRollout:
			parts = []string{k("j k", tr("revision", "ревизия")), k("enter", tr("undo", "откатить")), k("r", "restart")}
		default:
			parts = []string{k("/", tr("search", "поиск")), k("n N", tr("match", "совпад.")), k("g G", tr("top/end", "начало/конец"))}
		}
		parts = append(parts, k("[ ]", tr("tab", "вкладка")))
		if !m.single() {
			parts = append(parts, back)
		}
	}
	// The way to everything else stays in sight however narrow the window:
	// hints that don't fit go from the end, help never does.
	sep := s.Muted.Render(" · ")
	help := k("?", tr("help", "помощь"))
	parts = append(parts, k(":", "kubectl"))
	room := m.w - 2 - lipWidth(help)
	var shown []string
	for _, p := range parts {
		if room -= lipWidth(p) + lipWidth(sep); room < 0 {
			break
		}
		shown = append(shown, p)
	}
	return strings.Join(append(shown, help), sep)
}

// --- left panels ---------------------------------------------------------

func (m *Model) listLines(n int, l *listState, h int, focused bool, item func(i int, selected bool) string) []string {
	l.clamp(n, h)
	var lines []string
	for i := l.offset; i < n && len(lines) < h; i++ {
		lines = append(lines, item(i, i == l.cursor))
	}
	return lines
}

func (m *Model) selectLine(line string, w int, selected, focused bool) string {
	if !selected {
		return line
	}
	if focused {
		return m.st.Selected.Render(fit(strip(line), w))
	}
	return m.st.SelectedDim.Render(fit(strip(line), w))
}

func (m *Model) renderCtxPanel(w, h int) string {
	s := m.st
	focused := m.focus == fCtx
	inner := w - 2
	lines := m.listLines(len(m.contexts), &m.ctxList, h-2, focused, func(i int, sel bool) string {
		c := m.contexts[i]
		mark := "  "
		if c == m.ctxName {
			switch {
			case m.connecting == c:
				mark = s.Yellow.Render("◌ ")
			case m.connErr != "":
				mark = s.Red.Render("✗ ")
			default:
				mark = s.Green.Render("● ")
			}
		}
		line := " " + mark + s.Text.Render(c)
		return m.selectLine(line, inner, sel && (focused || h-2 < len(m.contexts)), focused)
	})
	right := ""
	if len(m.contexts) > h-2 {
		right = s.Dim.Render(fmt.Sprintf("%d/%d", m.ctxList.cursor+1, len(m.contexts)))
	}
	return s.box(w, h, m.panelTitle(1, i18n.T("panel.context"), focused), right, "", focused, lines)
}

func (m *Model) panelTitle(n int, title string, focused bool) string {
	s := m.st
	num := s.Dim.Render(fmt.Sprintf("[%d]", n))
	if focused {
		num = s.AccentBold.Render(fmt.Sprintf("[%d]", n))
		return num + " " + s.TitleFocused.Render(title)
	}
	return num + " " + s.TitleBlur.Render(title)
}

func (m *Model) renderNsPanel(w, h int) string {
	s := m.st
	focused := m.focus == fNs
	inner := w - 2
	lines := m.listLines(len(m.nsItems), &m.nsList, h-2, focused, func(i int, sel bool) string {
		n := m.nsItems[i]
		mark := "  "
		if n == m.ns {
			mark = s.Accent.Render("◆ ")
		}
		label := n
		if n == "" {
			label = s.Dim.Render("✱ " + i18n.T("ns.all"))
		}
		return m.selectLine(" "+mark+s.Text.Render(label), inner, sel && focused, focused)
	})
	if len(m.nsItems) == 0 {
		lines = []string{s.Dim.Render(" " + i18n.T("status.loading"))}
	}
	right := ""
	if len(m.nsItems) > 1 {
		right = s.Dim.Render(fmt.Sprint(len(m.nsItems) - 1))
	}
	footer := ""
	if focused && m.nsList.filter != "" {
		footer = s.Yellow.Render("/" + m.nsList.filter)
	}
	return s.box(w, h, m.panelTitle(2, i18n.T("panel.namespace"), focused), right, footer, focused, lines)
}

func (m *Model) renderResPanel(w, h int) string {
	s := m.st
	focused := m.focus == fRes
	k := m.kind()
	title := m.panelTitle(3, "", focused) + s.Muted.Render("‹ ") + s.TitleFocused.Render(k.Name) + s.Muted.Render(" ›")
	if !focused {
		title = m.panelTitle(3, "", focused) + s.Muted.Render("‹ ") + s.Text.Render(k.Name) + s.Muted.Render(" ›")
	}
	rows := m.resRows()
	var lines []string
	switch {
	case m.cl == nil || (m.resTable == nil && m.resErr == ""):
		lines = []string{s.Dim.Render(" " + i18n.T("status.loading"))}
	case m.resErr != "":
		lines = errLines(s, m.resErr, w-2)
	default:
		lines = m.tableLines(m.resTable, rows, &m.resList, w-2, h-2, focused, m.ns == "" && k.Namespaced)
	}
	right := ""
	if m.resTable != nil {
		right = s.Dim.Render(fmt.Sprintf("%d", len(rows)))
		if m.resList.filter != "" {
			right = s.Yellow.Render("/"+m.resList.filter) + " " + right
		}
	}
	footer := s.Dim.Render(fmt.Sprintf("%d/%d", m.kindIdx+1, len(m.kinds))) + s.Muted.Render(" [ ] K")
	return s.box(w, h, title, right, footer, focused, lines)
}

func errLines(s Styles, msg string, w int) []string {
	var out []string
	icon := "✗ "
	if kube.IsForbidden(fmt.Errorf("%s", msg)) {
		icon = "⛔ "
	}
	for _, l := range strings.Split(ansi.Wordwrap(icon+msg, max(w-2, 10), " "), "\n") {
		out = append(out, " "+s.Red.Render(l))
	}
	return out
}

func (m *Model) renderRelPanel(w, h int) string {
	s := m.st
	focused := m.focus == fRel
	var name string
	var lines []string
	switch m.relMode {
	case relNone:
		name = i18n.T("panel.related")
		lines = []string{s.Dim.Render(" —")}
	case relContainers:
		name = i18n.T("panel.containers")
		lines = m.containerLines(w-2, h-2, focused)
	case relJobs:
		name = i18n.T("panel.jobs")
		lines = m.relTableLines(w, h, focused)
	default:
		name = i18n.T("panel.pods")
		lines = m.relTableLines(w, h, focused)
	}
	right := ""
	if n := m.relLen(); n > 0 {
		right = s.Dim.Render(fmt.Sprint(n))
	}
	if m.podUsageOf != "" && m.relMode == relContainers {
		right = s.Dim.Render(fmt.Sprintf("cpu %dm · mem %s", m.podCPU, kube.HumanBytes(m.podMem)))
	}
	if m.relList.filter != "" {
		right = s.Yellow.Render("/"+m.relList.filter) + " " + right
	}
	return s.box(w, h, m.panelTitle(4, name, focused), right, "", focused, lines)
}

func (m *Model) relTableLines(w, h int, focused bool) []string {
	s := m.st
	switch {
	case m.relErr != "":
		return errLines(s, m.relErr, w-2)
	case m.relTable == nil:
		return []string{s.Dim.Render(" " + i18n.T("status.loading"))}
	}
	return m.tableLines(m.relTable, m.relRows(), &m.relList, w-2, h-2, focused, false)
}

func (m *Model) containerLines(w, h int, focused bool) []string {
	s := m.st
	if m.relErr != "" {
		return errLines(s, m.relErr, w)
	}
	if m.relPod == nil {
		return []string{s.Dim.Render(" " + i18n.T("status.loading"))}
	}
	header := s.Header.Render(fit("   CONTAINER", w*2/5) + fit("STATE", 18) + fitLeft("RST", 4))
	lines := []string{header}
	lines = append(lines, m.listLines(len(m.relContainers), &m.relList, h-1, focused, func(i int, sel bool) string {
		c := m.relContainers[i]
		st, _ := s.statusStyle(c.state)
		dot := st.Render("●")
		if !c.ready && c.state == "Running" {
			dot = s.Yellow.Render("●")
		}
		name := c.name
		if c.init {
			name = "↳ " + name
		}
		state := c.state
		rs := fmt.Sprint(c.restarts)
		rsSt := s.Dim
		if c.restarts > 0 {
			rsSt = s.Yellow
		}
		img := c.image
		if i := strings.LastIndex(img, "/"); i >= 0 {
			img = img[i+1:]
		}
		line := " " + dot + " " + s.Text.Render(fit(name, w*2/5-3)) + st.Render(fit(state, 18)) + rsSt.Render(fitLeft(rs, 4)) + "  " + s.Dim.Render(img)
		return m.selectLine(line, w, sel && focused, focused)
	})...)
	return lines
}

// tableLines renders a server-side table into a narrow panel: the name
// gets most of the room, then the columns that matter most.
func (m *Model) tableLines(t *kube.Table, rows []kube.Row, l *listState, w, h int, focused, showNS bool) []string {
	s := m.st
	if t == nil {
		return nil
	}
	nameCol := t.Col("Name")
	type col struct {
		idx   int
		name  string
		width int
		dots  bool
	}
	var cols []col
	for i, c := range t.Columns {
		if i == nameCol || c.Priority != 0 {
			continue
		}
		cw := len(c.Name)
		isReady := strings.EqualFold(c.Name, "Ready")
		dots := isReady
		for _, r := range rows {
			if i >= len(r.Cells) {
				continue
			}
			v := r.Cells[i]
			if dots {
				if a, b, ok := parseRatio(v); ok && b <= 8 && a <= 8 {
					if n := max(a, b, 1); n > cw {
						cw = n
					}
					continue
				}
				dots = false
			}
			if n := ansi.StringWidth(v); n > cw {
				cw = n
			}
		}
		if dots {
			// recompute width as max(dots, header)
			cw = len(c.Name)
			for _, r := range rows {
				if i < len(r.Cells) {
					if a, b, ok := parseRatio(r.Cells[i]); ok {
						cw = max(cw, a, b)
					}
				}
			}
		}
		cols = append(cols, col{idx: i, name: c.Name, width: min(cw, 22), dots: dots})
	}
	keep := func(n string) bool {
		switch strings.ToLower(n) {
		case "ready", "status", "age":
			return true
		}
		return false
	}
	nameW := 4
	for _, r := range rows {
		n := len(r.Name)
		if showNS {
			n += len(r.Namespace) + 1
		}
		nameW = max(nameW, n)
	}
	minName := min(nameW, 18)
	used := func() int {
		u := 3
		for _, c := range cols {
			u += c.width + 1
		}
		return u
	}
	for len(cols) > 0 && used()+minName > w {
		drop := -1
		for i := len(cols) - 1; i >= 0; i-- {
			if !keep(cols[i].name) {
				drop = i
				break
			}
		}
		if drop < 0 {
			// Only the columns that matter are left: the name, which keeps
			// both of its ends when cut, gives up some more room first.
			if used()+min(nameW, 12) <= w {
				break
			}
			drop = len(cols) - 1
		}
		cols = append(cols[:drop], cols[drop+1:]...)
	}
	nameW = max(w-used(), 4)

	header := "   " + fit("NAME", nameW)
	for _, c := range cols {
		header += " " + fitLeftOrRight(c.name, c.width, c.dots)
	}
	lines := []string{s.Header.Render(fit(header, w))}
	if len(rows) == 0 {
		return append(lines, s.Dim.Render("   "+i18n.T("status.empty")))
	}
	statusCol := t.Col("Status")
	lines = append(lines, m.listLines(len(rows), l, h-1, focused, func(i int, sel bool) string {
		r := rows[i]
		dot := " "
		if statusCol >= 0 && statusCol < len(r.Cells) {
			st, known := s.statusStyle(r.Cells[statusCol])
			if known {
				dot = st.Render("●")
			}
		}
		if r.Deleting {
			dot = s.Magenta.Render("✝")
		}
		nameSt := s.Text
		if r.Deleting {
			nameSt = s.Muted.Strikethrough(true)
		}
		var name string
		if showNS {
			full := midTrunc(r.Namespace+"/"+r.Name, nameW)
			if i := strings.Index(full, "/"); i >= 0 && strings.HasPrefix(r.Namespace+"/", full[:i+1]) {
				name = s.Dim.Render(full[:i+1]) + nameSt.Render(full[i+1:])
			} else {
				name = nameSt.Render(full)
			}
		} else {
			name = nameSt.Render(midTrunc(r.Name, nameW))
		}
		line := " " + dot + " " + name
		for _, c := range cols {
			v := ""
			if c.idx < len(r.Cells) {
				v = r.Cells[c.idx]
			}
			line += " " + m.renderCell(c.name, v, c.width, c.dots)
		}
		return m.selectLine(line, w, sel, focused)
	})...)
	return lines
}

func fitLeftOrRight(s string, w int, left bool) string {
	if left {
		return fit(s, w)
	}
	return fitLeft(s, w)
}

func parseRatio(v string) (int, int, bool) {
	var a, b int
	if _, err := fmt.Sscanf(v, "%d/%d", &a, &b); err != nil {
		return 0, 0, false
	}
	return a, b, true
}

func (m *Model) renderCell(col, v string, w int, dots bool) string {
	s := m.st
	if dots {
		if a, b, ok := parseRatio(v); ok {
			return fit(s.dots(a, b), w)
		}
	}
	switch strings.ToLower(col) {
	case "status", "phase":
		st, _ := s.statusStyle(v)
		return st.Render(fitLeft(v, w))
	case "restarts":
		if v != "0" && v != "" {
			return s.Yellow.Render(fitLeft(v, w))
		}
		return s.Dim.Render(fitLeft(v, w))
	case "age", "last schedule", "duration":
		return s.Dim.Render(fitLeft(v, w))
	case "ready":
		if a, b, ok := parseRatio(v); ok && a < b {
			return s.Yellow.Render(fitLeft(v, w))
		}
		return s.Green.Render(fitLeft(v, w))
	}
	return s.Text.Render(fitLeft(clean(v), w))
}

// --- main panel ----------------------------------------------------------

func (m *Model) renderMain(w, h int) string {
	s := m.st
	focused := m.focus == fMain
	// The title starts after "╭─ [5] "; tabs in it switch on a click.
	x0, y0 := m.layout().leftW+7, chromeH-1
	tab := func(i int) func(m *Model) tea.Cmd {
		return func(m *Model) tea.Cmd { m.tab = i; return tea.Batch(m.setFocus(fMain), m.loadTab(false)) }
	}
	var tabs []string
	var spots []hit
	x := x0
	for i, k := range tabKeys {
		label := i18n.T(k)
		if i == m.tab {
			tabs = append(tabs, s.TabActive.Render(label))
		} else {
			tabs = append(tabs, s.TabIdle.Render(label))
		}
		spots = append(spots, hit{x - 1, y0, lipWidth(label) + 2, tab(i)})
		x += lipWidth(label) + 3
	}
	num := s.Dim.Render("[5]")
	if focused {
		num = s.AccentBold.Render("[5]")
	}
	title := num + " " + strings.Join(tabs, s.Muted.Render(" │ "))
	if lipWidth(title) > w-8 {
		// No room for every tab: show the current one and where it is.
		label := i18n.T(tabKeys[m.tab])
		title = num + s.Muted.Render(" ‹ ") + s.TabActive.Render(label) + s.Muted.Render(" › ") +
			s.Dim.Render(fmt.Sprintf("%d/%d", m.tab+1, tabCount))
		spots = []hit{
			{x0 - 1, y0, 3, tab((m.tab + tabCount - 1) % tabCount)},
			{x0 + 2 + lipWidth(label), y0, 3, tab((m.tab + 1) % tabCount)},
		}
	}
	m.hits = append(m.hits, spots...)
	innerW := w - 2
	ch := h - 2
	var lines []string
	right := ""
	switch m.tab {
	case tabLogs:
		lines = m.logs.render(m, innerW, ch)
		right = m.logs.info(s)
		if m.logs.title != "" {
			right = s.Cyan.Render(m.logs.title) + s.Muted.Render(" · ") + right
		}
	case tabDescribe:
		lines = m.describe.render(s, innerW, ch, s.colorDescribe)
		right = s.Cyan.Render(m.describe.title)
	case tabYAML:
		lines = m.yaml.render(s, innerW, ch, s.colorYAML)
		right = s.Cyan.Render(m.yaml.title)
	case tabEvents:
		lines = m.events.render(m, innerW, ch)
		right = m.events.info(s)
	case tabRollout:
		lines = m.rollout.render(m, innerW, ch)
		if m.rollout.name != "" {
			right = s.Cyan.Render(strings.ToLower(m.rollout.kind.KindName) + "/" + m.rollout.name)
		}
	case tabOutput:
		if m.output.title == "" {
			lines = []string{"", s.Dim.Render("  " + i18n.T("cmd.hint")), "", s.Dim.Render("  : → kubectl get pods")}
		} else {
			lines = m.output.render(s, innerW, ch, s.colorOutput)
			right = s.Cyan.Render("$ " + m.output.title)
		}
	}
	if v := m.currentText(); v != nil && v.re != nil {
		right = s.Yellow.Render("/"+v.query+"/") + s.Dim.Render(fmt.Sprintf(" %d", len(v.matches))) + " " + right
	}
	footer := ""
	if v := m.currentText(); v != nil && len(v.lines) > ch {
		footer = s.Dim.Render(fmt.Sprintf("%d%%", min(100, (v.top+ch)*100/max(len(v.lines), 1))))
	}
	return s.box(w, h, title, right, footer, focused, lines)
}

// colorOutput colors kubectl output: header row and status words.
func (s Styles) colorOutput(l string) string {
	t := strings.TrimSpace(l)
	if t != "" && t == strings.ToUpper(t) && strings.ContainsAny(t, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") && !strings.ContainsAny(t, ":/") {
		return s.Header.Render(l)
	}
	if strings.HasPrefix(t, "Error") || strings.HasPrefix(t, "error:") {
		return s.Red.Render(l)
	}
	var b strings.Builder
	words := strings.SplitAfter(l, " ")
	for _, w := range words {
		trim := strings.TrimSpace(w)
		if st, ok := s.statusStyle(trim); ok && trim != "" {
			b.WriteString(st.Render(w))
		} else {
			b.WriteString(s.Text.Render(w))
		}
	}
	return b.String()
}

// midTrunc shortens s to w cells keeping both ends: generated names like
// api-gateway-65b59d8475-8bhbp differ only at the end.
func midTrunc(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s + strings.Repeat(" ", w-len(r))
	}
	if w < 5 {
		return string(r[:w])
	}
	head := (w - 1) * 2 / 5
	tail := w - 1 - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}
