package ui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/gnome627/komar/internal/i18n"
	"github.com/gnome627/komar/internal/kube"
	"github.com/gnome627/komar/internal/shell"
)

// textView is a scrollable block of text with search.
type textView struct {
	key     string
	title   string
	lines   []string
	top     int
	err     string
	loading bool
	re      *regexp.Regexp
	query   string
	matches []int
	cur     int
}

func (v *textView) set(content string) {
	v.lines = strings.Split(strings.TrimRight(content, "\r\n"), "\n")
	for i, l := range v.lines {
		v.lines[i] = clean(l)
	}
	v.err = ""
	v.loading = false
	v.reindex()
	if v.top > len(v.lines)-1 {
		v.top = max(len(v.lines)-1, 0)
	}
}

func (v *textView) reindex() {
	v.matches = v.matches[:0]
	if v.re == nil {
		return
	}
	for i, l := range v.lines {
		if v.re.MatchString(l) {
			v.matches = append(v.matches, i)
		}
	}
}

func (v *textView) scroll(d, height int) {
	v.top += d
	if v.top > len(v.lines)-height {
		v.top = len(v.lines) - height
	}
	if v.top < 0 {
		v.top = 0
	}
}

func (v *textView) jump(dir, height int) {
	if len(v.matches) == 0 {
		return
	}
	// next match after the current view position
	if dir > 0 {
		for _, i := range v.matches {
			if i > v.top {
				v.top = i
				v.scroll(-height/3, height)
				return
			}
		}
		v.top = v.matches[0]
	} else {
		for j := len(v.matches) - 1; j >= 0; j-- {
			if v.matches[j] < v.top+height/3 {
				v.top = v.matches[j]
				v.scroll(-height/3, height)
				return
			}
		}
		v.top = v.matches[len(v.matches)-1]
	}
	v.scroll(-height/3, height)
}

func (v *textView) render(s Styles, w, h int, colorize func(string) string) []string {
	if v.loading && len(v.lines) == 0 {
		return []string{s.Dim.Render(" " + i18n.T("status.loading"))}
	}
	var out []string
	if v.err != "" {
		out = errLines(s, v.err, w)
	}
	for i := v.top; i < len(v.lines) && len(out) < h; i++ {
		l := v.lines[i]
		if v.re != nil && v.re.MatchString(l) {
			out = append(out, " "+highlight(s, l, v.re))
		} else if colorize != nil {
			out = append(out, " "+colorize(l))
		} else {
			out = append(out, " "+s.Text.Render(l))
		}
	}
	return out
}

func highlight(s Styles, line string, re *regexp.Regexp) string {
	idx := re.FindAllStringIndex(line, -1)
	if len(idx) == 0 {
		return s.Text.Render(line)
	}
	var b strings.Builder
	last := 0
	for _, p := range idx {
		if p[0] == p[1] {
			continue
		}
		b.WriteString(s.Text.Render(line[last:p[0]]))
		b.WriteString(s.Match.Render(line[p[0]:p[1]]))
		last = p[1]
	}
	b.WriteString(s.Text.Render(line[last:]))
	return b.String()
}

var yamlKey = regexp.MustCompile(`^(\s*-?\s*)([A-Za-z0-9_.\-/]+):(\s|$)`)

func (s Styles) colorYAML(l string) string {
	t := strings.TrimSpace(l)
	if strings.HasPrefix(t, "#") {
		return s.Dim.Render(l)
	}
	if m := yamlKey.FindStringSubmatchIndex(l); m != nil {
		prefix := l[:m[4]]
		key := l[m[4]:m[5]]
		rest := l[m[5]:]
		return s.Muted.Render(prefix) + s.Accent.Render(key) + s.Text.Render(rest)
	}
	if strings.HasPrefix(t, "- ") {
		i := strings.Index(l, "-")
		return s.Text.Render(l[:i]) + s.Muted.Render("-") + s.Text.Render(l[i+1:])
	}
	return s.Text.Render(l)
}

var describeKey = regexp.MustCompile(`^([A-Z][A-Za-z ()/\-]*):(\s|$)`)

func (s Styles) colorDescribe(l string) string {
	if m := describeKey.FindStringSubmatchIndex(l); m != nil {
		return s.AccentBold.Render(l[:m[3]+1]) + s.Text.Render(l[m[3]+1:])
	}
	t := strings.TrimSpace(l)
	if strings.HasPrefix(t, "Warning ") {
		return s.Red.Render(l)
	}
	if m := describeKey.FindStringSubmatchIndex(t); m != nil {
		i := strings.Index(l, t)
		return s.Text.Render(l[:i]) + s.Cyan.Render(t[:m[3]+1]) + s.Text.Render(t[m[3]+1:])
	}
	return s.Text.Render(l)
}

// --- loading tabs --------------------------------------------------------

// loadTab refreshes the active tab for the current target. force reloads
// even when the target didn't change.
func (m *Model) loadTab(force bool) tea.Cmd {
	if m.cl == nil {
		return nil
	}
	t := m.target()
	switch m.tab {
	case tabLogs:
		return m.logs.ensure(m, t, force)
	case tabDescribe:
		return m.loadText(&m.describe, tabDescribe, t, force)
	case tabYAML:
		return m.loadText(&m.yaml, tabYAML, t, force)
	case tabEvents:
		if force {
			return m.loadEvents()
		}
	case tabRollout:
		return m.loadRollout(force)
	}
	return nil
}

func (m *Model) loadText(v *textView, tab int, t target, force bool) tea.Cmd {
	if !t.valid() {
		v.key, v.lines, v.err = "", nil, ""
		return nil
	}
	key := t.key()
	if key == v.key && !force {
		return nil
	}
	if key != v.key {
		v.top = 0
		v.lines = nil
	}
	v.key = key
	v.loading = true
	v.title = strings.ToLower(t.kind.KindName) + "/" + t.name
	cl, gen := m.cl, m.gen
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if tab == tabYAML {
			y, err := cl.YAML(ctx, t.kind, t.ns, t.name)
			return textMsg{gen: gen, tab: tab, key: key, content: y, err: err}
		}
		args := append(cl.KubectlArgs(), "describe", t.kind.Ref(), t.name)
		if t.kind.Namespaced {
			args = append(args, "-n", t.ns)
		}
		cmd, err := shell.Command(ctx, cl.KubectlEnv(), args...)
		if err != nil {
			return textMsg{gen: gen, tab: tab, key: key, err: err}
		}
		out, err := cmd.CombinedOutput()
		if err != nil && len(out) > 0 {
			err = fmt.Errorf("%s", strings.TrimSpace(string(out)))
			out = nil
		}
		return textMsg{gen: gen, tab: tab, key: key, content: string(out), err: err}
	}
}

func (m *Model) onText(msg textMsg) tea.Cmd {
	if msg.gen != m.gen {
		return nil
	}
	v := &m.describe
	if msg.tab == tabYAML {
		v = &m.yaml
	}
	if msg.key != v.key {
		return nil
	}
	v.loading = false
	if msg.err != nil {
		v.err = kube.ErrString(msg.err)
		v.lines = nil
		return nil
	}
	v.set(msg.content)
	return nil
}

func (m *Model) mainHeight() int { return max(m.h-chromeH-2, 1) }

func (m *Model) currentText() *textView {
	switch m.tab {
	case tabDescribe:
		return &m.describe
	case tabYAML:
		return &m.yaml
	case tabOutput:
		return &m.output
	}
	return nil
}

func (m *Model) scrollMain(d int) {
	h := m.mainHeight()
	switch m.tab {
	case tabLogs:
		m.logs.scroll(d, h)
	case tabEvents:
		m.events.move(d)
	case tabRollout:
		m.rollout.move(d)
	default:
		if v := m.currentText(); v != nil {
			v.scroll(d, h)
		}
	}
}

// handleMainKey handles keys while the main panel has focus.
func (m *Model) handleMainKey(k tea.KeyPressMsg) (bool, tea.Cmd) {
	h := m.mainHeight()
	key := hotkey(k)
	switch key {
	case "[", "h", "left", "shift+left":
		m.tab = (m.tab + tabCount - 1) % tabCount
		return true, m.loadTab(false)
	case "]", "l", "right", "shift+right":
		m.tab = (m.tab + 1) % tabCount
		return true, m.loadTab(false)
	case "j", "down":
		m.scrollMain(1)
		return true, nil
	case "k", "up":
		m.scrollMain(-1)
		return true, nil
	case "ctrl+d", "pgdown", "space":
		m.scrollMain(h / 2)
		return true, nil
	case "ctrl+u", "pgup":
		m.scrollMain(-h / 2)
		return true, nil
	}

	switch m.tab {
	case tabLogs:
		return m.logs.handleKey(m, k)
	case tabEvents:
		return m.events.handleKey(m, k)
	case tabRollout:
		return m.rollout.handleKey(m, k)
	}

	v := m.currentText()
	if v == nil {
		return false, nil
	}
	switch key {
	case "g", "home":
		v.top = 0
	case "G", "end":
		v.scroll(len(v.lines), h)
	case "/":
		m.searchText(v)
	case "n":
		v.jump(1, h)
	case "N":
		v.jump(-1, h)
	default:
		return false, nil
	}
	return true, nil
}

func (m *Model) searchText(v *textView) {
	orig, origRe := v.query, v.re
	m.prompt = &prompt{
		label: "/",
		edit:  newLineEdit(v.query),
		onChange: func(m *Model, s string) {
			v.query = s
			v.re = compileSearch(s)
			v.reindex()
			if len(v.matches) > 0 {
				v.top = 0
				v.jump(1, m.mainHeight())
			}
		},
		onCancel: func(m *Model) {
			v.query, v.re = orig, origRe
			v.reindex()
		},
	}
}

// compileSearch compiles a case-insensitive regex, falling back to a
// literal match when the pattern is invalid (e.g. while still typing).
func compileSearch(s string) *regexp.Regexp {
	if s == "" {
		return nil
	}
	re, err := regexp.Compile("(?i)" + s)
	if err != nil {
		re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(s))
	}
	return re
}

// --- rollout tab ---------------------------------------------------------

type rolloutView struct {
	key      string
	kind     kube.Kind
	name     string
	ns       string
	status   kube.RolloutStatus
	history  []kube.Revision
	err      string
	cursor   int
	loaded   bool
	history0 time.Time
}

type rolloutMsg struct {
	gen      int
	key      string
	status   kube.RolloutStatus
	history  []kube.Revision
	withHist bool
	err      error
}

func (m *Model) loadRollout(force bool) tea.Cmd {
	t := m.target()
	if m.cl == nil || !t.valid() || !t.kind.Rollable() {
		m.rollout = rolloutView{}
		return nil
	}
	key := t.key()
	withHist := force || key != m.rollout.key || time.Since(m.rollout.history0) > 10*time.Second
	if key != m.rollout.key {
		m.rollout = rolloutView{key: key, kind: t.kind, name: t.name, ns: t.ns}
	}
	cl, gen := m.cl, m.gen
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		st, err := cl.Rollout(ctx, t.kind, t.ns, t.name)
		msg := rolloutMsg{gen: gen, key: key, status: st, err: err, withHist: withHist}
		if withHist && err == nil {
			msg.history, msg.err = cl.RolloutHistory(ctx, t.kind, t.ns, t.name)
		}
		return msg
	}
}

func (r *rolloutView) onMsg(m *Model, msg rolloutMsg) {
	if msg.gen != m.gen || msg.key != r.key {
		return
	}
	r.loaded = true
	if msg.err != nil {
		r.err = kube.ErrString(msg.err)
		return
	}
	r.err = ""
	r.status = msg.status
	if msg.withHist {
		r.history = msg.history
		r.history0 = time.Now()
		if r.cursor >= len(r.history) {
			r.cursor = max(len(r.history)-1, 0)
		}
	}
}

func (r *rolloutView) move(d int) {
	r.cursor += d
	if r.cursor >= len(r.history) {
		r.cursor = len(r.history) - 1
	}
	if r.cursor < 0 {
		r.cursor = 0
	}
}

func (r *rolloutView) handleKey(m *Model, k tea.KeyPressMsg) (bool, tea.Cmd) {
	switch hotkey(k) {
	case "g":
		r.cursor = 0
	case "G":
		r.cursor = max(len(r.history)-1, 0)
	case "enter":
		if r.cursor < len(r.history) {
			rev := r.history[r.cursor]
			if rev.Current {
				return true, nil
			}
			return true, m.requestUndo(target{kind: r.kind, ns: r.ns, name: r.name}, rev.Number)
		}
	default:
		return false, nil
	}
	return true, nil
}

func (r *rolloutView) render(m *Model, w, h int) []string {
	s := m.st
	t := m.target()
	if !t.valid() || !t.kind.Rollable() {
		return []string{"", s.Dim.Render("  " + i18n.T("restart.unsupported", strings.ToLower(t.kind.KindName)))}
	}
	if !r.loaded {
		return []string{s.Dim.Render(" " + i18n.T("status.loading"))}
	}
	var out []string
	if r.err != "" {
		out = errLines(s, r.err, w)
	}
	st := r.status
	barW := max(min(w-24, 50), 10)
	bar := func(label string, n, total int64, style func(...string) string) string {
		filled := 0
		if total > 0 {
			filled = int(float64(barW) * float64(n) / float64(total))
		}
		if filled > barW {
			filled = barW
		}
		return fmt.Sprintf("  %s %s%s %d/%d", fit(s.Dim.Render(label), 9), style(strings.Repeat("█", filled)),
			s.Muted.Render(strings.Repeat("░", barW-filled)), n, total)
	}
	out = append(out, "", "  "+s.AccentBold.Render(i18n.T("rollout.progress"))+"  "+r.statusLabel(s))
	out = append(out, bar(i18n.T("rollout.new"), st.Updated, max64(st.Desired, 1), s.Accent.Render))
	out = append(out, bar("ready", st.Ready, max64(st.Desired, 1), s.Green.Render))
	if st.Old > 0 {
		out = append(out, bar(i18n.T("rollout.old"), st.Old, max64(st.Desired, st.Old), s.Yellow.Render))
	}
	if st.Message != "" {
		for _, l := range wrap(st.Message, w-4) {
			out = append(out, "  "+s.Red.Render(l))
		}
	}
	out = append(out, "", "  "+s.AccentBold.Render(i18n.T("rollout.history"))+"  "+s.Dim.Render(i18n.T("rollout.hint")))
	if len(r.history) == 0 {
		out = append(out, s.Dim.Render("  "+i18n.T("rollout.none")))
	}
	for i, rev := range r.history {
		if len(out) >= h {
			break
		}
		mark := "  "
		if rev.Current {
			mark = s.Green.Render("● ")
		}
		line := fmt.Sprintf("%s%s %-4d %-5s %s", mark, s.Dim.Render(i18n.T("rollout.revision")), rev.Number,
			kube.Age(rev.Created), strings.Join(rev.Images, ", "))
		if rev.Cause != "" {
			line += s.Dim.Render("  — " + clean(rev.Cause))
		}
		if i == r.cursor {
			line = s.Selected.Render(fit(" "+strip(line), w))
		} else {
			line = " " + line
		}
		out = append(out, line)
	}
	return out
}

func (r *rolloutView) statusLabel(s Styles) string {
	if r.status.Done {
		return s.Green.Render("✓ " + i18n.T("rollout.done"))
	}
	return s.Yellow.Render("◌ " + i18n.T("rollout.waiting"))
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
