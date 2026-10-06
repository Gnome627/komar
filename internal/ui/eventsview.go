package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/gnome627/komar/internal/i18n"
	"github.com/gnome627/komar/internal/kube"
)

// eventsView lists cluster events folded into groups, each with a count, a
// per-minute sparkline of the last half hour and its rate.
type eventsView struct {
	tracker  *kube.EventTracker
	raw      []corev1.Event
	groups   []*kube.EventGroup
	err      string
	loaded   bool
	warnOnly bool
	allNS    bool
	sort     kube.SortBy
	group    kube.GroupBy
	cursor   int
	offset   int
}

func newEventsView() eventsView {
	return eventsView{tracker: kube.NewEventTracker()}
}

func (e *eventsView) reset() {
	e.tracker.Reset()
	e.raw, e.groups, e.loaded, e.err = nil, nil, false, ""
	e.cursor, e.offset = 0, 0
}

func (e *eventsView) update(evs []corev1.Event, err error) {
	if err != nil {
		e.err = kube.ErrString(err)
		e.loaded = true
		return
	}
	e.err = ""
	e.raw = evs
	e.loaded = true
	e.regroup()
}

func (e *eventsView) regroup() {
	e.groups = e.tracker.Aggregate(e.raw, e.group, e.sort, e.warnOnly, time.Now())
	if e.cursor >= len(e.groups) {
		e.cursor = max(len(e.groups)-1, 0)
	}
}

// recentWarnings counts warning occurrences in the last 10 minutes for the
// top bar.
func (e *eventsView) recentWarnings() int {
	n := 0.0
	for _, g := range e.tracker.Aggregate(e.raw, kube.GroupByReason, kube.SortByLast, true, time.Now()) {
		for _, v := range g.Bins[kube.EventWindow-10:] {
			n += v
		}
	}
	return int(n + 0.5)
}

func (e *eventsView) move(d int) {
	e.cursor += d
	if e.cursor >= len(e.groups) {
		e.cursor = len(e.groups) - 1
	}
	if e.cursor < 0 {
		e.cursor = 0
	}
}

func (e *eventsView) handleKey(m *Model, k tea.KeyPressMsg) (bool, tea.Cmd) {
	switch k.String() {
	case "g", "home":
		e.cursor = 0
	case "G", "end":
		e.cursor = max(len(e.groups)-1, 0)
	case "w":
		e.warnOnly = !e.warnOnly
		e.regroup()
	case "a":
		e.allNS = !e.allNS
		e.reset()
		return true, m.loadEvents()
	case "s":
		e.sort = (e.sort + 1) % 3
		e.regroup()
	case "o":
		e.group = (e.group + 1) % 2
		e.regroup()
	case "enter":
		// Jump to the object the event is about.
		if e.cursor < len(e.groups) {
			g := e.groups[e.cursor]
			return true, m.jumpTo(g.Kind, g.Namespace, g.Object)
		}
	default:
		return false, nil
	}
	return true, nil
}

// jumpTo selects an object in the resources panel.
func (m *Model) jumpTo(kind, ns, name string) tea.Cmd {
	k, ok := m.findKind(kind)
	if !ok {
		return nil
	}
	var cmds []tea.Cmd
	if k.Namespaced && m.ns != "" && ns != m.ns {
		cmds = append(cmds, m.switchNamespace(ns))
	}
	if k.Ref() != m.kind().Ref() {
		m.setKind(k)
	}
	m.pendingSelect = name
	if k.Namespaced && ns != "" {
		m.pendingSelect = ns + "/" + name
	}
	m.focus = fRes
	cmds = append(cmds, m.loadResources())
	return tea.Batch(cmds...)
}

func (e *eventsView) info(s Styles) string {
	sortName := []string{i18n.T("events.sort_last"), i18n.T("events.sort_count"), i18n.T("events.sort_rate")}[e.sort]
	groupName := []string{i18n.T("events.group_obj"), i18n.T("events.group_reason")}[e.group]
	scope := i18n.T("events.all")
	if e.warnOnly {
		scope = i18n.T("events.warn_only")
	}
	ns := ""
	if e.allNS {
		ns = " · " + i18n.T("ns.all")
	}
	return s.Dim.Render(fmt.Sprintf("%s · ↓%s · %s%s · %d", scope, sortName, groupName, ns, len(e.groups)))
}

func (e *eventsView) render(m *Model, w, h int) []string {
	s := m.st
	if !e.loaded {
		return []string{s.Dim.Render(" " + i18n.T("status.loading"))}
	}
	var out []string
	if e.err != "" {
		out = append(out, s.Red.Render(" "+e.err))
	}
	if len(e.groups) == 0 {
		return append(out, "", s.Dim.Render("  "+i18n.T("events.none")))
	}
	const sparkW = 15
	reasonW, objW := 18, 30
	if w < 110 {
		objW = 22
	}
	if w < 90 {
		reasonW, objW = 14, 16
	}
	header := " " + fit("", 2) + fit("REASON", reasonW) + " " + fit("OBJECT", objW) + " " + fitLeft("COUNT", 6) + "  " +
		fit("30m", sparkW) + " " + fitLeft("RATE", 8) + " " + fitLeft("LAST", 5) + "  MESSAGE"
	out = append(out, s.Header.Render(fit(header, w-2)))
	rows := h - len(out)
	if e.cursor < e.offset {
		e.offset = e.cursor
	}
	if e.cursor >= e.offset+rows {
		e.offset = e.cursor - rows + 1
	}
	// One scale for all rows so heights compare across groups.
	var peak float64
	for _, g := range e.groups {
		bins := make([]float64, sparkW)
		for j, v := range g.Bins {
			bins[j*sparkW/kube.EventWindow] += v
		}
		for _, v := range bins {
			peak = max(peak, v)
		}
	}
	for i := e.offset; i < len(e.groups) && len(out) < h; i++ {
		g := e.groups[i]
		icon := s.Dim.Render("· ")
		reasonSt := s.Text
		if g.Type == corev1.EventTypeWarning {
			icon = s.Red.Render("⚠ ")
			reasonSt = s.Orange
		}
		obj := strings.ToLower(g.Kind) + "/" + g.Object
		if e.group == kube.GroupByReason && g.Objects > 1 {
			obj = fmt.Sprintf("%s ×%d", strings.ToLower(g.Kind), g.Objects)
		}
		if g.Namespace != "" && (e.allNS || m.ns == "") {
			obj = g.Namespace + "/" + obj
		}
		bins := make([]float64, sparkW)
		for j, v := range g.Bins {
			bins[j*sparkW/kube.EventWindow] += v
		}
		rate := g.RatePerMin()
		rateSt := s.Dim
		if rate >= 1 {
			rateSt = s.Yellow
		}
		if rate >= 10 {
			rateSt = s.Red
		}
		sparkSt := s.Accent
		if g.Type == corev1.EventTypeWarning {
			sparkSt = s.Orange
		}
		countSt := s.Text
		if g.Count >= 100 {
			countSt = s.Bright
		}
		line := " " + icon + reasonSt.Render(fit(g.Reason, reasonW)) + " " + s.Cyan.Render(fit(obj, objW)) + " " +
			countSt.Render(fitLeft("×"+fmt.Sprint(g.Count), 6)) + "  " + sparkSt.Render(spark(bins, peak)) + " " +
			rateSt.Render(fitLeft(i18n.T("events.rate", rate), 8)) + " " + s.Dim.Render(fitLeft(kube.Age(g.Last), 5)) + "  " +
			s.Text.Render(strings.ReplaceAll(g.Message, "\n", " "))
		if i == e.cursor && m.focus == fMain {
			line = s.Selected.Render(fit(strip(line), w-2))
		}
		out = append(out, line)
	}
	return out
}
