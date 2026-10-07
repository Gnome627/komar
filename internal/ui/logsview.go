package ui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/gnome627/komar/internal/i18n"
	"github.com/gnome627/komar/internal/kube"
)

const maxLogLines = 50000

var sinceSteps = []time.Duration{0, 5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}

type logEntry struct {
	src  int
	text string
	err  bool
	t    time.Time
}

type logSource struct {
	pod, container string
}

// logView streams logs of the current target (or a label selector) and
// lets you search them: highlight mode marks matches and n/N jumps between
// them, filter mode shows only matching lines.
type logView struct {
	session   *kube.LogSession
	sessionID int
	key       string
	title     string
	tail      int64

	lines   []logEntry
	sources []logSource
	srcIdx  map[logSource]int

	follow   bool
	previous bool
	since    int
	wrap     bool

	query   string
	re      *regexp.Regexp
	filter  bool
	matches []int // indices into lines that match re
	top     int   // first visible index into visible()
	matchAt int

	selector   string // custom label selector mode
	selectorNS string
	noTarget   bool
}

type logBatchMsg struct {
	id     int
	lines  []kube.LogLine
	closed bool
}

func newLogView(tail int64) logView {
	return logView{follow: true, wrap: true, tail: tail, srcIdx: map[logSource]int{}}
}

func (l *logView) stop() {
	if l.session != nil {
		l.session.Stop()
		l.session = nil
	}
}

func (l *logView) reset() {
	l.lines = l.lines[:0]
	l.sources = nil
	l.srcIdx = map[logSource]int{}
	l.matches = l.matches[:0]
	l.top = 0
}

// ensure starts streaming for target t unless it already is.
func (l *logView) ensure(m *Model, t target, force bool) tea.Cmd {
	var key string
	var sel kube.ListOptions
	if l.selector != "" {
		key = "selector|" + l.selectorNS + "|" + l.selector
		sel = kube.ListOptions{Namespace: l.selectorNS, LabelSelector: l.selector}
		l.title = "-l " + l.selector
	} else {
		if !t.valid() || !(t.kind.HasPods() || t.kind.Resource == "nodes") {
			l.stop()
			l.key = ""
			l.title = ""
			l.noTarget = true
			l.reset()
			return nil
		}
		key = t.key() + "|" + t.container
		l.title = strings.ToLower(t.kind.KindName) + "/" + t.name
		if t.container != "" {
			l.title += " -c " + t.container
		}
	}
	key += fmt.Sprintf("|%v|%d", l.previous, l.since)
	if key == l.key && !force && l.session != nil {
		return nil
	}
	l.noTarget = false
	l.stop()
	l.reset()
	l.key = key
	l.follow = true
	l.sessionID++
	id := l.sessionID
	cl := m.cl
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	opts := kube.LogOptions{Previous: l.previous, Since: sinceSteps[l.since], Tail: l.tail, Follow: !l.previous}
	if l.selector == "" {
		opts.Container = t.container
	}
	if l.selector == "" {
		return func() tea.Msg {
			lctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			s, ok, err := cl.PodSelector(lctx, t.kind, t.ns, t.name)
			if err != nil || !ok {
				text := i18n.T("logs.no_target")
				if err != nil {
					text = kube.ErrString(err)
				}
				return logBatchMsg{id: id, lines: []kube.LogLine{{Text: text, Err: true}}, closed: true}
			}
			return startedLogs{id: id, session: cl.StreamLogs(ctx, s, opts)}
		}
	}
	return func() tea.Msg { return startedLogs{id: id, session: cl.StreamLogs(ctx, sel, opts)} }
}

type startedLogs struct {
	id      int
	session *kube.LogSession
}

func waitLogs(id int, s *kube.LogSession) tea.Cmd {
	return func() tea.Msg {
		first, ok := <-s.Lines
		if !ok {
			return logBatchMsg{id: id, closed: true}
		}
		batch := []kube.LogLine{first}
		// Drain what is already buffered so a burst renders once.
		timeout := time.After(30 * time.Millisecond)
		for len(batch) < 2000 {
			select {
			case l, ok := <-s.Lines:
				if !ok {
					return logBatchMsg{id: id, lines: batch, closed: true}
				}
				batch = append(batch, l)
			case <-timeout:
				return logBatchMsg{id: id, lines: batch}
			}
		}
		return logBatchMsg{id: id, lines: batch}
	}
}

func (l *logView) onBatch(m *Model, msg logBatchMsg) tea.Cmd {
	if msg.id != l.sessionID {
		return nil
	}
	reordered := false
	for _, ln := range msg.lines {
		src := logSource{ln.Pod, ln.Container}
		idx, ok := l.srcIdx[src]
		if !ok {
			idx = len(l.sources)
			l.sources = append(l.sources, src)
			l.srcIdx[src] = idx
		}
		e := logEntry{src: idx, text: clean(ansi.Strip(ln.Text)), err: ln.Err, t: ln.Time}
		// Streams from several pods arrive in chunks (each starts with its
		// own backlog); merge them by timestamp like stern does.
		pos := len(l.lines)
		if !e.t.IsZero() {
			for pos > 0 && !l.lines[pos-1].t.IsZero() && l.lines[pos-1].t.After(e.t) {
				pos--
			}
		}
		if pos == len(l.lines) {
			l.lines = append(l.lines, e)
			if l.re != nil && l.re.MatchString(e.text) {
				l.matches = append(l.matches, pos)
			}
			continue
		}
		l.lines = append(l.lines, logEntry{})
		copy(l.lines[pos+1:], l.lines[pos:])
		l.lines[pos] = e
		reordered = true
	}
	if reordered {
		l.rebuildMatches()
	}
	if len(l.lines) > maxLogLines {
		drop := len(l.lines) - maxLogLines*9/10
		l.lines = append([]logEntry{}, l.lines[drop:]...)
		l.rebuildMatches()
		l.top = max(l.top-drop, 0)
	}
	if msg.closed || l.session == nil {
		return nil
	}
	return waitLogs(l.sessionID, l.session)
}

// handled in Model.handleData via type switch below
func (m *Model) onStartedLogs(msg startedLogs) tea.Cmd {
	if msg.id != m.logs.sessionID {
		msg.session.Stop()
		return nil
	}
	m.logs.session = msg.session
	return waitLogs(msg.id, msg.session)
}

func (l *logView) rebuildMatches() {
	l.matches = l.matches[:0]
	if l.re == nil {
		return
	}
	for i, e := range l.lines {
		if l.re.MatchString(e.text) {
			l.matches = append(l.matches, i)
		}
	}
}

// visible returns indices of lines shown (all, or matches in filter mode).
func (l *logView) visibleLen() int {
	if l.filter && l.re != nil {
		return len(l.matches)
	}
	return len(l.lines)
}

func (l *logView) at(i int) int {
	if l.filter && l.re != nil {
		return l.matches[i]
	}
	return i
}

func (l *logView) scroll(d, h int) {
	n := l.visibleLen()
	if l.follow {
		l.top = max(n-h, 0)
	}
	l.top += d
	if l.top < 0 {
		l.top = 0
	}
	if d < 0 {
		l.follow = false
	}
	if l.top >= n-h {
		l.top = max(n-h, 0)
		if d > 0 {
			l.follow = true
		}
	}
}

func (l *logView) handleKey(m *Model, k tea.KeyPressMsg) (bool, tea.Cmd) {
	h := m.mainHeight()
	switch hotkey(k) {
	case "g", "home":
		l.follow = false
		l.top = 0
	case "G", "end":
		l.follow = true
	case "f":
		l.follow = !l.follow
		if !l.follow {
			l.top = max(l.visibleLen()-h, 0)
		}
	case "w":
		l.wrap = !l.wrap
	case "p":
		l.previous = !l.previous
		return true, l.ensure(m, m.target(), true)
	case "t":
		l.since = (l.since + 1) % len(sinceSteps)
		return true, l.ensure(m, m.target(), true)
	case "m":
		l.filter = !l.filter
		l.follow = true
	case "c":
		l.reset()
	case "/":
		m.searchLogs()
	case "n":
		l.jump(1, h)
	case "N":
		l.jump(-1, h)
	case "L":
		m.askSelector()
	case "esc":
		if l.selector != "" {
			l.selector = ""
			return true, l.ensure(m, m.target(), true)
		}
		if l.re != nil {
			l.query, l.re = "", nil
			l.rebuildMatches()
			return true, nil
		}
		return false, nil
	default:
		return false, nil
	}
	return true, nil
}

// jump moves to the next/previous match in highlight mode.
func (l *logView) jump(dir, h int) {
	if l.re == nil || len(l.matches) == 0 || l.filter {
		return
	}
	cur := l.top + h/2
	if l.follow {
		cur = len(l.lines)
	}
	target := -1
	if dir > 0 {
		for _, i := range l.matches {
			if i > cur {
				target = i
				break
			}
		}
		if target < 0 {
			target = l.matches[0]
		}
	} else {
		for j := len(l.matches) - 1; j >= 0; j-- {
			if l.matches[j] < cur {
				target = l.matches[j]
				break
			}
		}
		if target < 0 {
			target = l.matches[len(l.matches)-1]
		}
	}
	l.follow = false
	l.matchAt = target
	l.top = max(target-h/2, 0)
}

func (m *Model) searchLogs() {
	l := &m.logs
	orig, origRe := l.query, l.re
	m.prompt = &prompt{
		label: i18n.T("logs.search") + " /",
		edit:  newLineEdit(l.query),
		onChange: func(m *Model, v string) {
			l.query = v
			l.re = compileSearch(v)
			l.rebuildMatches()
			if !l.filter && len(l.matches) > 0 {
				l.jump(-1, m.mainHeight())
			}
		},
		onCancel: func(m *Model) {
			l.query, l.re = orig, origRe
			l.rebuildMatches()
		},
	}
}

// askSelector streams logs of every pod matching a label selector.
func (m *Model) askSelector() {
	l := &m.logs
	m.prompt = &prompt{
		label: i18n.T("logs.selector") + " -l",
		edit:  newLineEdit(l.selector),
		onSubmit: func(m *Model, v string) tea.Cmd {
			l.selector = strings.TrimSpace(v)
			l.selectorNS = m.ns
			m.tab = tabLogs
			return tea.Batch(l.ensure(m, m.target(), true), m.setFocus(fMain))
		},
	}
}

var srcColors = []func(Styles) func(...string) string{
	func(s Styles) func(...string) string { return s.Cyan.Render },
	func(s Styles) func(...string) string { return s.Magenta.Render },
	func(s Styles) func(...string) string { return s.Yellow.Render },
	func(s Styles) func(...string) string { return s.Green.Render },
	func(s Styles) func(...string) string { return s.Blue.Render },
	func(s Styles) func(...string) string { return s.Orange.Render },
}

func shortPod(p string) string {
	// deployment pods end in -<rs hash>-<pod hash>; the last part is enough
	// to tell them apart.
	if i := strings.LastIndex(p, "-"); i > 0 && len(p)-i <= 6 {
		return p[i+1:]
	}
	return p
}

func (l *logView) render(m *Model, w, h int) []string {
	s := m.st
	if l.noTarget && l.selector == "" {
		return []string{"", s.Dim.Render("  " + i18n.T("logs.no_target"))}
	}
	n := l.visibleLen()
	multi := len(l.sources) > 1
	containers := map[string]bool{}
	for _, src := range l.sources {
		containers[src.container] = true
	}
	// The pod column shrinks in a narrow panel to leave the text some room.
	nameW := 18
	if w < 60 {
		nameW = 8
	}
	prefixW := 0
	prefix := func(e logEntry) string {
		if !multi || e.src >= len(l.sources) {
			return ""
		}
		src := l.sources[e.src]
		name := shortPod(src.pod)
		if len(containers) > 1 {
			name += "/" + src.container
		}
		return srcColors[e.src%len(srcColors)](s)(fit(name, nameW)) + s.Muted.Render(" │ ")
	}
	if multi {
		prefixW = nameW + 3
	}
	textW := max(w-2-prefixW, 10)

	renderLine := func(i int) []string {
		e := l.lines[i]
		var body string
		switch {
		case e.err:
			body = s.Red.Render(e.text)
		case l.re != nil && !l.filter:
			body = highlight(s, e.text, l.re)
		case l.re != nil && l.filter:
			body = highlight(s, e.text, l.re)
		default:
			body = s.Text.Render(e.text)
		}
		mark := " "
		if i == l.matchAt && l.re != nil && !l.filter && !l.follow {
			mark = s.Yellow.Render("▌")
		}
		if !l.wrap || ansi.StringWidth(e.text) <= textW {
			return []string{mark + prefix(e) + ansi.Truncate(body, textW, "…")}
		}
		// lipgloss.Wrap closes the colors at the end of each row and opens
		// them again on the next one, so a highlight cut by the wrap doesn't
		// spill over the padding and the border.
		var rows []string
		for j, r := range strings.Split(lipgloss.Wrap(body, textW, ""), "\n") {
			p := prefix(e)
			if j > 0 && multi {
				p = strings.Repeat(" ", nameW) + s.Muted.Render(" ┆ ")
			}
			rows = append(rows, mark+p+r)
			mark = " "
		}
		return rows
	}

	if n == 0 {
		return []string{"", s.Dim.Render("  " + i18n.T("status.empty"))}
	}
	var out []string
	if l.follow {
		// fill from the bottom
		var rev [][]string
		total := 0
		for i := n - 1; i >= 0 && total < h; i-- {
			r := renderLine(l.at(i))
			rev = append(rev, r)
			total += len(r)
		}
		for i := len(rev) - 1; i >= 0; i-- {
			out = append(out, rev[i]...)
		}
		if len(out) > h {
			out = out[len(out)-h:]
		}
		l.top = max(n-h, 0)
		return out
	}
	if l.top > n-1 {
		l.top = max(n-1, 0)
	}
	for i := l.top; i < n && len(out) < h; i++ {
		out = append(out, renderLine(l.at(i))...)
	}
	if len(out) > h {
		out = out[:h]
	}
	return out
}

// info is shown in the panel's top-right corner.
func (l *logView) info(s Styles) string {
	var parts []string
	if l.follow {
		parts = append(parts, s.Green.Render("◉ "+i18n.T("logs.follow")))
	} else {
		parts = append(parts, s.Yellow.Render("⏸ "+i18n.T("logs.paused")))
	}
	if l.previous {
		parts = append(parts, s.Orange.Render(i18n.T("logs.previous")))
	}
	if d := sinceSteps[l.since]; d > 0 {
		parts = append(parts, s.Dim.Render(i18n.T("logs.since")+" "+shortDur(d)))
	}
	if l.re != nil {
		mode := i18n.T("logs.highlight_mode")
		if l.filter {
			mode = i18n.T("logs.filter_mode")
		}
		parts = append(parts, s.Yellow.Render("/"+l.query+"/")+" "+s.Dim.Render(i18n.T("logs.matches", len(l.matches))+" · "+mode))
	}
	if !l.wrap {
		parts = append(parts, s.Dim.Render("nowrap"))
	}
	return strings.Join(parts, s.Muted.Render(" · "))
}

func shortDur(d time.Duration) string {
	if d >= time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
