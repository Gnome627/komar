package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/gnome627/komar/internal/i18n"
	"github.com/gnome627/komar/internal/kube"
)

// Every async result carries the generation it was started in; results
// from a previous context are dropped.

type connectedMsg struct {
	gen        int
	cl         *kube.Cluster
	version    string
	namespaces []string
	user       string
	err        error
}

type nsListMsg struct {
	gen  int
	list []string
	err  error
}

type tableMsg struct {
	gen int
	key string
	t   *kube.Table
	err error
}

type relatedMsg struct {
	gen int
	key string
	t   *kube.Table
	pod *corev1.Pod
	err error
}

type textMsg struct {
	gen     int
	tab     int
	key     string
	content string
	err     error
}

type eventsMsg struct {
	gen    int
	events []corev1.Event
	err    error
}

type metricsMsg struct {
	gen   int
	usage *kube.Usage
	pod   string
	cpu   int64
	mem   int64
}

type kindsMsg struct {
	gen   int
	kinds []kube.Kind
	err   error
}

type namesMsg struct {
	key   string
	names []string
}

func (m *Model) timeout(d time.Duration) (context.Context, context.CancelFunc) {
	base := m.ctx
	if base == nil {
		base = context.Background()
	}
	return context.WithTimeout(base, d)
}

// connect switches to a context: builds clients, checks the server and
// loads namespaces in one go so the switch feels instant once it lands.
func (m *Model) connect(name string) tea.Cmd {
	m.gen++
	gen := m.gen
	m.connecting = name
	m.connErr = ""
	kcfg := m.kcfg
	return func() tea.Msg {
		cl, err := kcfg.Connect(name)
		if err != nil {
			return connectedMsg{gen: gen, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		v, err := cl.Ping(ctx)
		if err != nil {
			return connectedMsg{gen: gen, cl: cl, err: err}
		}
		nss, _ := cl.Namespaces(ctx)
		user := cl.WhoAmI(ctx)
		return connectedMsg{gen: gen, cl: cl, version: v, namespaces: nss, user: user}
	}
}

func (m *Model) switchContext(name string) tea.Cmd {
	if name == m.ctxName && m.cl != nil && m.connErr == "" {
		return nil
	}
	m.logs.stop()
	if m.cancel != nil {
		m.cancel()
	}
	m.ctxName = name
	for i, c := range m.contexts {
		if c == name {
			m.ctxList.cursor = i
		}
	}
	m.resTable, m.resErr = nil, ""
	m.clearRelated()
	m.events.reset()
	m.logs.reset()
	m.logs.key = ""
	m.logs.sessionID++
	m.describe, m.yaml, m.rollout = textView{}, textView{}, rolloutView{}
	m.usage = nil
	m.warnings = 0
	m.allKinds = nil
	m.nameCache = map[string][]string{}
	return m.connect(name)
}

func (m *Model) switchNamespace(ns string) tea.Cmd {
	m.ns = ns
	for i, n := range m.nsItems {
		if n == ns {
			m.nsList.cursor = i
		}
	}
	m.sess.Namespaces[m.ctxName] = ns
	m.sess.Save()
	m.resTable, m.resErr = nil, ""
	m.resList = listState{}
	m.clearRelated()
	m.events.reset()
	label := ns
	if ns == "" {
		label = i18n.T("ns.all")
	}
	m.setStatus(i18n.T("status.switched_ns", label))
	return tea.Batch(m.loadResources(), m.loadEvents(), m.onSelectionChanged())
}

func (m *Model) loadNamespaces() tea.Cmd {
	if m.cl == nil {
		return nil
	}
	cl, gen := m.cl, m.gen
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		l, err := cl.Namespaces(ctx)
		return nsListMsg{gen: gen, list: l, err: err}
	}
}

func (m *Model) resourceKey() string { return m.kind().Ref() + "|" + m.ns }

func (m *Model) loadResources() tea.Cmd {
	if m.cl == nil {
		return nil
	}
	cl, gen, k, ns := m.cl, m.gen, m.kind(), m.ns
	key := m.resourceKey()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		t, err := cl.ListTable(ctx, k, kube.ListOptions{Namespace: ns})
		return tableMsg{gen: gen, key: key, t: t, err: err}
	}
}

func (m *Model) loadRelated() tea.Cmd {
	if m.cl == nil || m.relMode == relNone {
		return nil
	}
	t := m.resTarget()
	if !t.valid() {
		return nil
	}
	cl, gen, key, mode := m.cl, m.gen, m.relKey, m.relMode
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		switch mode {
		case relContainers:
			p, err := cl.Pod(ctx, t.ns, t.name)
			return relatedMsg{gen: gen, key: key, pod: p, err: err}
		case relJobs:
			tb, err := cl.JobsOf(ctx, t.ns, t.name)
			return relatedMsg{gen: gen, key: key, t: tb, err: err}
		default:
			sel, ok, err := cl.PodSelector(ctx, t.kind, t.ns, t.name)
			if err != nil || !ok {
				return relatedMsg{gen: gen, key: key, err: err, t: &kube.Table{}}
			}
			tb, err := cl.ListTable(ctx, kube.Builtin[0], sel)
			return relatedMsg{gen: gen, key: key, t: tb, err: err}
		}
	}
}

func (m *Model) loadEvents() tea.Cmd {
	if m.cl == nil {
		return nil
	}
	cl, gen := m.cl, m.gen
	ns := m.ns
	if m.events.allNS {
		ns = ""
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ev, err := cl.Events(ctx, ns)
		return eventsMsg{gen: gen, events: ev, err: err}
	}
}

func (m *Model) loadMetrics() tea.Cmd {
	if m.cl == nil {
		return nil
	}
	cl, gen := m.cl, m.gen
	t := m.target()
	return func() tea.Msg {
		if !cl.HasMetrics() {
			return metricsMsg{gen: gen}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out := metricsMsg{gen: gen}
		if u, err := cl.ClusterUsage(ctx); err == nil {
			out.usage = &u
		}
		if t.kind.Resource == "pods" && t.valid() {
			if cpu, mem, err := cl.PodUsage(ctx, t.ns, t.name); err == nil {
				out.pod, out.cpu, out.mem = t.ns+"/"+t.name, cpu, mem
			}
		}
		return out
	}
}

func (m *Model) loadKinds() tea.Cmd {
	if m.cl == nil {
		return nil
	}
	cl, gen := m.cl, m.gen
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ks, err := cl.Resources(ctx)
		return kindsMsg{gen: gen, kinds: ks, err: err}
	}
}

// handleData routes async results.
func (m *Model) handleData(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case connectedMsg:
		if msg.gen != m.gen {
			return nil
		}
		m.connecting = ""
		if msg.err != nil {
			m.connErr = kube.ErrString(msg.err)
			m.cl = msg.cl
			m.resErr = i18n.T("status.unreachable", m.connErr)
			m.setError(i18n.T("status.unreachable", m.connErr))
			return nil
		}
		m.cl = msg.cl
		m.version = msg.version
		m.user = msg.user
		if m.cancel != nil {
			m.cancel()
		}
		m.ctx, m.cancel = context.WithCancel(context.Background())
		m.setNamespaces(msg.namespaces)
		// The view saved by the previous run applies to the first connect
		// only, and only when it is the same context and no -n was given.
		last := m.restore
		m.restore = nil
		if last != nil && (last.Context != m.ctxName || m.opts.Namespace != "") {
			last = nil
		}
		ns, ok := m.sess.Namespaces[m.ctxName]
		if last != nil {
			ns, ok = last.Namespace, true
		}
		if m.opts.Namespace != "" {
			ns, ok = m.opts.Namespace, true
			m.opts.Namespace = ""
		}
		if !ok {
			ns = m.cl.DefaultNS
		}
		m.ns = ns
		for i, n := range m.nsItems {
			if n == ns {
				m.nsList.cursor = i
			}
		}
		ref, ok := m.sess.Kinds[m.ctxName]
		if last != nil {
			ref, ok = last.Kind, true
		}
		if ok {
			if k, found := m.cl.KindFor(ref); found {
				m.setKindQuiet(k)
			}
		}
		if last != nil && last.Kind == m.kind().Ref() {
			m.applyView(*last)
		}
		m.sess.Context = m.ctxName
		m.sess.Save()
		m.setStatus(i18n.T("status.switched_ctx", m.ctxName))
		return tea.Batch(m.loadResources(), m.loadEvents(), m.loadMetrics(), m.loadTab(true))

	case nsListMsg:
		if msg.gen == m.gen && msg.err == nil {
			m.setNamespaces(msg.list)
		}

	case tableMsg:
		if msg.gen != m.gen || msg.key != m.resourceKey() {
			return nil
		}
		if msg.err != nil {
			m.resErr = kube.ErrString(msg.err)
			if m.resTable == nil {
				m.resTable = &kube.Table{Kind: m.kind()}
			}
			return nil
		}
		prev, hadPrev := m.selectedRes()
		m.resErr = ""
		m.keepDying(m.resTable, msg.t, fRes)
		m.resTable = msg.t
		rows := m.resRows()
		// Keep the cursor on the same object across refreshes.
		want := ""
		if hadPrev {
			want = prev.Key()
		}
		if m.pendingSelect != "" {
			want = m.pendingSelect
		}
		if want != "" {
			for i, r := range rows {
				if r.Key() == want || r.Name == want {
					m.resList.cursor = i
					break
				}
			}
			if m.pendingSelect != "" {
				m.pendingSelect = ""
			}
		}
		m.resList.clamp(len(rows), m.resListHeight())
		return m.onSelectionChanged()

	case relatedMsg:
		if msg.gen != m.gen || msg.key != m.relKey {
			return nil
		}
		m.relErr = ""
		if msg.err != nil {
			m.relErr = kube.ErrString(msg.err)
		}
		before := m.target().key()
		if msg.pod != nil {
			m.relPod = msg.pod
			m.relContainers = containerItems(msg.pod)
		}
		if msg.t != nil {
			var prev string
			if r, ok := m.selectedRel(); ok {
				prev = r.Key()
			}
			m.keepDying(m.relTable, msg.t, fRel)
			m.relTable = msg.t
			if prev != "" {
				for i, r := range m.relRows() {
					if r.Key() == prev {
						m.relList.cursor = i
					}
				}
			}
		}
		restored := false
		if want := m.pendingRel; want != "" {
			m.pendingRel = ""
			for i, r := range m.relRows() {
				if r.Key() == want {
					m.relList.cursor, restored = i, true
				}
			}
			for i, c := range m.relContainers {
				if m.relMode == relContainers && c.name == want {
					m.relList.cursor, restored = i, true
				}
			}
		}
		m.relList.clamp(m.relLen(), m.relListHeight())
		if restored {
			return m.loadTab(false)
		}
		if m.target().key() != before && (m.focus == fRel || m.lastFocus == fRel) {
			return m.loadTab(false)
		}

	case textMsg:
		return m.onText(msg)

	case eventsMsg:
		if msg.gen != m.gen {
			return nil
		}
		m.events.update(msg.events, msg.err)
		m.warnings = m.events.recentWarnings()

	case metricsMsg:
		if msg.gen != m.gen {
			return nil
		}
		m.usage = msg.usage
		m.podUsageOf, m.podCPU, m.podMem = msg.pod, msg.cpu, msg.mem

	case kindsMsg:
		if msg.gen != m.gen {
			return nil
		}
		if msg.err != nil && len(msg.kinds) == 0 {
			m.setError(kube.ErrString(msg.err))
			return nil
		}
		m.allKinds = msg.kinds
		if p, ok := m.modal.(*pickerModal); ok && p.id == "kinds" {
			p.setItems(m.kindItems())
		}

	case namesMsg:
		m.nameCache[msg.key] = msg.names
		if m.cmd != nil {
			m.cmd.refresh(m)
			return m.flushPending()
		}

	case startedLogs:
		return m.onStartedLogs(msg)

	case logBatchMsg:
		return m.logs.onBatch(m, msg)

	case rolloutMsg:
		m.rollout.onMsg(m, msg)

	case actionMsg:
		return m.onAction(msg)

	case execCheckMsg:
		return m.onExecCheck(msg)

	case execDoneMsg:
		return m.onExecDone(msg)

	case cmdOutputMsg:
		return m.onCmdOutput(msg)

	case deleteCheckMsg:
		return m.onDeleteCheck(msg)

	case scaleInfoMsg:
		return m.onScaleInfo(msg)
	}
	return nil
}

func (m *Model) setKindQuiet(k kube.Kind) {
	for i, x := range m.kinds {
		if x.Ref() == k.Ref() {
			m.kindIdx = i
			return
		}
	}
	m.kinds = append(m.kinds, k)
	m.kindIdx = len(m.kinds) - 1
}

func (m *Model) setNamespaces(list []string) {
	cur := ""
	if m.nsList.cursor < len(m.nsItems) {
		cur = m.nsItems[m.nsList.cursor]
	}
	m.nsItems = append([]string{""}, list...)
	for i, n := range m.nsItems {
		if n == cur {
			m.nsList.cursor = i
		}
	}
}

func containerItems(p *corev1.Pod) []containerItem {
	var out []containerItem
	add := func(cs []corev1.Container, init bool) {
		for _, c := range cs {
			st, ready, rs := kube.ContainerState(p, c.Name)
			out = append(out, containerItem{name: c.Name, state: st, ready: ready, restarts: rs, image: c.Image, init: init})
		}
	}
	add(p.Spec.Containers, false)
	add(p.Spec.InitContainers, true)
	for _, e := range p.Spec.EphemeralContainers {
		st, ready, rs := kube.ContainerState(p, e.Name)
		out = append(out, containerItem{name: e.Name, state: st, ready: ready, restarts: rs, image: e.Image, init: true})
	}
	return out
}
