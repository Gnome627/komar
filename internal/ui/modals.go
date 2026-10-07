package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/gnome627/komar/internal/i18n"
	"github.com/gnome627/komar/internal/kube"
)

type modal interface {
	update(m *Model, k tea.KeyPressMsg) (closed bool, cmd tea.Cmd)
	view(m *Model) string
}

// modalBox frames modal content. The box grows to its widest line but
// never past the window: what doesn't fit is cut, not drawn outside.
func (m *Model) modalBox(title string, lines []string, width int, danger bool) string {
	s := m.st
	w := width
	for _, l := range lines {
		if lw := ansi.StringWidth(l) + 6; lw > w {
			w = lw
		}
	}
	w = max(min(w, m.w-2), 8)
	if room := max(m.h-4, 1); len(lines) > room {
		lines = lines[:room]
	}
	if tw := w - 5; ansi.StringWidth(title) > tw {
		title = ansi.Truncate(title, tw, "…")
	}
	bs := s.ModalBorder
	ts := s.AccentBold
	if danger {
		bs = s.Red
		ts = s.Danger
	}
	inner := w - 2
	var b strings.Builder
	t := " " + title + " "
	b.WriteString(bs.Render("╭─") + ts.Render(t) + bs.Render(strings.Repeat("─", max(inner-1-ansi.StringWidth(t), 0))+"╮") + "\n")
	b.WriteString(bs.Render("│") + strings.Repeat(" ", inner) + bs.Render("│") + "\n")
	for _, l := range lines {
		b.WriteString(bs.Render("│") + "  " + fit(l, inner-4) + "  " + bs.Render("│") + "\n")
	}
	b.WriteString(bs.Render("│") + strings.Repeat(" ", inner) + bs.Render("│") + "\n")
	b.WriteString(bs.Render("╰" + strings.Repeat("─", inner) + "╯"))
	return b.String()
}

// --- confirm -------------------------------------------------------------

type confirmModal struct {
	title, question, body string
	danger                bool
	onYes                 func(m *Model) tea.Cmd
}

func (c *confirmModal) update(m *Model, k tea.KeyPressMsg) (bool, tea.Cmd) {
	// By key position like every other shortcut: with a Russian layout on,
	// the y key types н and still means yes. Reading д and н as да and нет
	// would clash with that (н is the y key), so they are not answers.
	switch hotkey(k) {
	case "y", "Y", "enter":
		return true, c.onYes(m)
	case "n", "N", "esc", "q":
		return true, nil
	}
	return false, nil
}

func (c *confirmModal) view(m *Model) string {
	s := m.st
	textW := m.modalTextW(70)
	var lines []string
	for _, l := range wrap(c.question, textW) {
		lines = append(lines, s.Bright.Render(l))
	}
	lines = append(lines, "")
	for _, l := range strings.Split(c.body, "\n") {
		for _, wl := range wrap(l, textW) {
			lines = append(lines, s.Dim.Render(wl))
		}
	}
	keys := []string{"", s.Key.Render("y") + s.KeyDesc.Render(" / enter  ") + s.Key.Render("n") + s.KeyDesc.Render(" / esc")}
	return m.modalBox(c.title, m.modalBody(lines, keys), 46, c.danger)
}

// modalTextW is how wide text inside a modal may be in this window.
func (m *Model) modalTextW(limit int) int { return max(min(m.w-8, limit), 4) }

// modalBody cuts long text so the keys under it stay on screen.
func (m *Model) modalBody(text, keys []string) []string {
	if room := max(m.h-4-len(keys), 1); len(text) > room {
		text = append(text[:room-1:room-1], m.st.Dim.Render("…"))
	}
	return append(text, keys...)
}

// --- info ----------------------------------------------------------------

type infoModal struct {
	title, body string
	danger      bool
	debugHint   bool // "b" starts a debug container (no-shell hint)
}

func (i *infoModal) update(m *Model, k tea.KeyPressMsg) (bool, tea.Cmd) {
	if i.debugHint && hotkey(k) == "b" {
		// From the "no shell" hint: jump straight to a debug container.
		return true, m.startDebug(m.target())
	}
	return true, nil
}

func (i *infoModal) view(m *Model) string {
	s := m.st
	var lines []string
	for _, l := range strings.Split(i.body, "\n") {
		for _, wl := range strings.Split(ansi.Wrap(clean(l), m.modalTextW(90), " /"), "\n") {
			lines = append(lines, s.Text.Render(wl))
		}
	}
	return m.modalBox(i.title, m.modalBody(lines, []string{"", s.KeyDesc.Render(i18n.T("misc.any_key"))}), 40, i.danger)
}

// --- scale ---------------------------------------------------------------

type scaleModal struct {
	t      target
	orig   int
	value  int
	ready  int
	typing string
}

func (sm *scaleModal) update(m *Model, k tea.KeyPressMsg) (bool, tea.Cmd) {
	switch key := hotkey(k); key {
	case "esc", "q":
		return true, nil
	case "up", "k", "+", "=", "right", "l":
		sm.value++
		sm.typing = ""
	case "down", "j", "-", "_", "left", "h":
		if sm.value > 0 {
			sm.value--
		}
		sm.typing = ""
	case "backspace":
		if len(sm.typing) > 0 {
			sm.typing = sm.typing[:len(sm.typing)-1]
			sm.value, _ = strconv.Atoi("0" + sm.typing)
		}
	case "enter":
		if sm.value == sm.orig {
			return true, nil
		}
		return true, m.scaleTo(sm.t, sm.value)
	default:
		if len(key) == 1 && key[0] >= '0' && key[0] <= '9' && len(sm.typing) < 4 {
			sm.typing += key
			sm.value, _ = strconv.Atoi(sm.typing)
		}
	}
	return false, nil
}

func (sm *scaleModal) view(m *Model) string {
	s := m.st
	arrow := s.Dim.Render(fmt.Sprintf("%d", sm.orig)) + s.Muted.Render("  →  ") + s.Bright.Render(fmt.Sprintf("%d", sm.value))
	var viz string
	if sm.value <= 24 {
		var b strings.Builder
		for i := 0; i < max(sm.value, sm.orig); i++ {
			switch {
			case i < sm.value && i < sm.orig:
				b.WriteString(s.Green.Render("● "))
			case i < sm.value:
				b.WriteString(s.Accent.Render("◉ "))
			default:
				b.WriteString(s.Red.Render("○ "))
			}
		}
		viz = b.String()
	}
	lines := []string{
		s.Dim.Render(strings.ToLower(sm.t.kind.KindName) + "/" + sm.t.name),
		"",
		arrow,
		"",
		viz,
		"",
		s.KeyDesc.Render(i18n.T("scale.hint")),
	}
	return m.modalBox(i18n.T("scale.title", ""), lines, 50, false)
}

// --- picker --------------------------------------------------------------

type pickerModal struct {
	id       string
	title    string
	items    []string
	labels   map[string]string
	filtered []string
	filter   lineEdit
	cursor   int
	offset   int
	onPick   func(m *Model, v string) tea.Cmd
}

func (p *pickerModal) setItems(items []string) {
	p.items = items
	p.refilter()
}

func (p *pickerModal) refilter() {
	f := strings.ToLower(p.filter.String())
	// Prefix matches first, then substring (name or label), then fuzzy.
	var pre, sub, fz []string
	for _, it := range p.items {
		name := strings.ToLower(it)
		l := name + " " + strings.ToLower(p.labels[it])
		switch {
		case f == "" || strings.HasPrefix(name, f):
			pre = append(pre, it)
		case strings.Contains(l, f):
			sub = append(sub, it)
		case fuzzy(l, f):
			fz = append(fz, it)
		}
	}
	p.filtered = append(append(pre, sub...), fz...)
	if p.cursor >= len(p.filtered) {
		p.cursor = max(len(p.filtered)-1, 0)
	}
}

// fuzzy matches when all runes of pat appear in order in s.
func fuzzy(s, pat string) bool {
	i := 0
	pr := []rune(pat)
	for _, r := range s {
		if i < len(pr) && r == pr[i] {
			i++
		}
	}
	return i == len(pr)
}

func (p *pickerModal) update(m *Model, k tea.KeyPressMsg) (bool, tea.Cmd) {
	switch hotkey(k) {
	case "esc":
		return true, nil
	case "enter":
		if p.cursor < len(p.filtered) {
			return true, p.onPick(m, p.filtered[p.cursor])
		}
		return true, nil
	case "up", "ctrl+p", "ctrl+k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "ctrl+n", "ctrl+j", "tab":
		if p.cursor < len(p.filtered)-1 {
			p.cursor++
		}
	default:
		if p.filter.Handle(k) {
			p.cursor = 0
			p.refilter()
		}
	}
	return false, nil
}

// rows is how many items fit in this window.
func (p *pickerModal) rows(m *Model) int { return min(max(m.h-7, 1), 14) }

func (p *pickerModal) view(m *Model) string {
	s := m.st
	rows := p.rows(m)
	width := min(max(m.w/2, 40), 70, m.w-2)
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
	lines := []string{s.Accent.Render("› ") + p.filter.View(s, width-10), ""}
	if len(p.filtered) == 0 {
		lines = append(lines, s.Dim.Render(i18n.T("status.empty")))
	}
	for i := p.offset; i < len(p.filtered) && i < p.offset+rows; i++ {
		it := p.filtered[i]
		label := it
		if l, ok := p.labels[it]; ok && l != "" {
			label = it + "  " + s.Dim.Render(l)
		}
		if i == p.cursor {
			lines = append(lines, s.Selected.Render(fit(" "+strip(label), width-6)))
		} else {
			lines = append(lines, " "+s.Text.Render(label))
		}
	}
	for len(lines) < rows+2 {
		lines = append(lines, "")
	}
	lines = append(lines, s.KeyDesc.Render(fmt.Sprintf("%d/%d · ↑↓ enter esc", min(p.cursor+1, len(p.filtered)), len(p.filtered))))
	return m.modalBox(p.title, lines, width, false)
}

func strip(s string) string { return ansi.Strip(s) }

func (m *Model) contextPicker() modal {
	labels := map[string]string{}
	for _, c := range m.contexts {
		cluster, user, _ := m.kcfg.ContextInfo(c)
		labels[c] = cluster + " · " + user
	}
	p := &pickerModal{id: "contexts", title: "⎈ " + i18n.T("panel.context"), items: m.contexts, labels: labels,
		onPick: func(m *Model, v string) tea.Cmd { return m.switchContext(v) }}
	p.refilter()
	return p
}

func (m *Model) namespacePicker() modal {
	items := make([]string, 0, len(m.nsItems))
	labels := map[string]string{}
	for _, n := range m.nsItems {
		if n == "" {
			items = append(items, "*")
			labels["*"] = i18n.T("ns.all")
			continue
		}
		items = append(items, n)
	}
	p := &pickerModal{id: "namespaces", title: i18n.T("panel.namespace"), items: items, labels: labels,
		onPick: func(m *Model, v string) tea.Cmd {
			if v == "*" {
				v = ""
			}
			return m.switchNamespace(v)
		}}
	p.refilter()
	return p
}

func (m *Model) kindItems() []string {
	var items []string
	seen := map[string]bool{}
	for _, k := range kube.Builtin {
		items = append(items, k.Ref())
		seen[k.Ref()] = true
	}
	for _, k := range m.allKinds {
		if !seen[k.Ref()] {
			items = append(items, k.Ref())
		}
	}
	return items
}

func (m *Model) openKindPicker() tea.Cmd {
	labels := map[string]string{}
	all := append(append([]kube.Kind{}, kube.Builtin...), m.allKinds...)
	for _, k := range all {
		l := k.KindName
		if k.Short != "" {
			l += " (" + k.Short + ")"
		}
		labels[k.Ref()] = l
	}
	p := &pickerModal{id: "kinds", title: i18n.T("kinds.title"), labels: labels,
		onPick: func(m *Model, v string) tea.Cmd {
			k, ok := m.findKind(v)
			if !ok {
				return nil
			}
			m.setKind(k)
			m.focus = fRes
			return tea.Batch(m.loadResources(), m.onSelectionChanged())
		}}
	p.setItems(m.kindItems())
	m.modal = p
	if m.allKinds == nil {
		return m.loadKinds()
	}
	return nil
}

// --- help ----------------------------------------------------------------

type helpModal struct{ top int }

func newHelpModal(m *Model) *helpModal { return &helpModal{} }

func (h *helpModal) update(m *Model, k tea.KeyPressMsg) (bool, tea.Cmd) {
	switch hotkey(k) {
	case "j", "down":
		h.top++
	case "k", "up":
		if h.top > 0 {
			h.top--
		}
	default:
		return true, nil
	}
	return false, nil
}

func (h *helpModal) view(m *Model) string {
	s := m.st
	sections := helpSections()
	var lines []string
	textW := m.modalTextW(90)
	if banner := strings.Split(bannerSmall(m), "\n"); lipWidth(banner[0]) <= textW {
		lines = append(lines, banner...)
		lines = append(lines, "")
	}
	// Descriptions wrap under themselves in a narrow window.
	const keyW = 16
	for _, sec := range sections {
		lines = append(lines, s.AccentBold.Render(sec.title))
		for _, kv := range sec.keys {
			for j, d := range wrap(kv[1], textW-2-keyW) {
				key := ""
				if j == 0 {
					key = kv[0]
				}
				lines = append(lines, "  "+s.Key.Render(fit(key, keyW))+s.Text.Render(d))
			}
		}
		lines = append(lines, "")
	}
	rows := max(m.h-4, 1)
	if h.top > max(len(lines)-rows, 0) {
		h.top = max(len(lines)-rows, 0)
	}
	end := min(h.top+rows, len(lines))
	return m.modalBox(i18n.T("help.title"), lines[h.top:end], 60, false)
}
