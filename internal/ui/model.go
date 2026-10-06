// Package ui is the komar terminal interface: lazygit-style panels on the
// left (context, namespace, resources, related), a tabbed main panel on the
// right (logs, describe, yaml, events, rollout, kubectl output), a
// waybar-like top bar and a command line for raw kubectl.
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnome627/komar/internal/fx"
	"github.com/gnome627/komar/internal/i18n"
	"github.com/gnome627/komar/internal/kube"
	"github.com/gnome627/komar/internal/state"
	"github.com/gnome627/komar/internal/theme"
)

const (
	fMain = 0
	fCtx  = 1
	fNs   = 2
	fRes  = 3
	fRel  = 4
)

const (
	tabLogs = iota
	tabDescribe
	tabYAML
	tabEvents
	tabRollout
	tabOutput
	tabCount
)

var tabKeys = []string{"tab.logs", "tab.describe", "tab.yaml", "tab.events", "tab.rollout", "tab.output"}

type relMode int

const (
	relNone relMode = iota
	relPods
	relJobs
	relContainers
)

// listState is cursor + scroll + filter of a list panel.
type listState struct {
	cursor, offset int
	filter         string
}

func (l *listState) clamp(n, height int) {
	if l.cursor >= n {
		l.cursor = n - 1
	}
	if l.cursor < 0 {
		l.cursor = 0
	}
	if height < 1 {
		height = 1
	}
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+height {
		l.offset = l.cursor - height + 1
	}
	if l.offset > max(n-height, 0) {
		l.offset = max(n-height, 0)
	}
	if l.offset < 0 {
		l.offset = 0
	}
}

func (l *listState) move(d, n int) {
	l.cursor += d
	if l.cursor < 0 {
		l.cursor = 0
	}
	if l.cursor >= n {
		l.cursor = n - 1
	}
	if l.cursor < 0 {
		l.cursor = 0
	}
}

// target is the object actions apply to.
type target struct {
	kind      kube.Kind
	ns, name  string
	uid       string
	owners    []metav1.OwnerReference
	container string
}

func (t target) valid() bool { return t.name != "" }
func (t target) key() string {
	return t.kind.Ref() + "|" + t.ns + "|" + t.name
}

type containerItem struct {
	name     string
	state    string
	ready    bool
	restarts int32
	image    string
	init     bool
}

type activeAnim struct {
	anim  *fx.Anim
	panel int
	w, h  int
}

// Options configure the program from flags.
type Options struct {
	Kubeconfig string
	Context    string
	Namespace  string
	NoSplash   bool
}

// Model is the whole application state.
type Model struct {
	opts  Options
	kcfg  *kube.Config
	conf  state.Config
	sess  *state.Session
	hist  *state.History
	pal   theme.Palette
	st    Styles
	tsrc  theme.Source
	w, h  int
	ready bool

	cl         *kube.Cluster
	ctxName    string
	connecting string
	connErr    string
	version    string
	user       string
	gen        int
	ctx        context.Context
	cancel     context.CancelFunc

	focus     int
	lastFocus int

	contexts []string
	ctxList  listState

	nsItems []string // "" first = all namespaces
	nsList  listState
	ns      string

	kinds    []kube.Kind // tab cycle (builtins + custom picked)
	kindIdx  int
	allKinds []kube.Kind

	resTable *kube.Table
	resErr   string
	resList  listState
	resKey   string

	relMode       relMode
	relTable      *kube.Table
	relContainers []containerItem
	relPod        *corev1.Pod
	relErr        string
	relList       listState
	relKey        string

	pendingSelect string

	tab      int
	logs     logView
	describe textView
	yaml     textView
	output   textView
	events   eventsView
	rollout  rolloutView

	modal  modal
	prompt *prompt
	cmd    *cmdLine

	status    string
	statusErr bool
	statusAt  time.Time

	hidden map[string]time.Time
	anims  map[int]*activeAnim
	splash *fx.Splash

	framing    bool
	usage      *kube.Usage
	podCPU     int64
	podMem     int64
	podUsageOf string
	warnings   int

	nameCache    map[string][]string
	pendingNames []tea.Cmd
	lastTick     time.Time
}

// New builds the model; Init connects to the cluster.
func New(opts Options) (*Model, error) {
	kcfg, err := kube.LoadConfig(opts.Kubeconfig)
	if err != nil {
		return nil, err
	}
	ctxs := kcfg.Contexts()
	if len(ctxs) == 0 {
		return nil, fmt.Errorf("%s", i18n.T("err.no_kubeconfig"))
	}
	pal, src := theme.Load()
	m := &Model{
		opts: opts, kcfg: kcfg, conf: state.LoadConfig(), sess: state.LoadSession(), hist: state.LoadHistory(),
		pal: pal, st: NewStyles(pal), tsrc: src,
		contexts: ctxs, focus: fRes, lastFocus: fRes,
		kinds:     append([]kube.Kind{}, kube.Builtin...),
		hidden:    map[string]time.Time{},
		anims:     map[int]*activeAnim{},
		nameCache: map[string][]string{},
		describe:  textView{},
		events:    newEventsView(),
	}
	m.logs = newLogView(m.conf.LogTail)
	start := opts.Context
	if start == "" {
		start = kcfg.Current
	}
	if start == "" || !contains(ctxs, start) {
		start = ctxs[0]
	}
	m.ctxName = start
	for i, c := range ctxs {
		if c == start {
			m.ctxList.cursor = i
		}
	}
	if !opts.NoSplash && m.conf.SplashEnabled() {
		m.splash = fx.NewSplash(80, 24, "kubernetes · omarchy", pal.RGB)
	}
	return m, nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.connect(m.ctxName), tickCmd()}
	if m.splash != nil {
		cmds = append(cmds, m.startFrames())
	}
	return tea.Batch(cmds...)
}

// --- status line ---------------------------------------------------------

func (m *Model) setStatus(s string) {
	m.status, m.statusErr, m.statusAt = s, false, time.Now()
}

func (m *Model) setError(s string) {
	m.status, m.statusErr, m.statusAt = s, true, time.Now()
}

// --- kinds ---------------------------------------------------------------

func (m *Model) kind() kube.Kind { return m.kinds[m.kindIdx] }

func (m *Model) setKind(k kube.Kind) {
	for i, x := range m.kinds {
		if x.Ref() == k.Ref() {
			m.kindIdx = i
			m.onKindChanged()
			return
		}
	}
	m.kinds = append(m.kinds, k)
	m.kindIdx = len(m.kinds) - 1
	m.onKindChanged()
}

func (m *Model) onKindChanged() {
	m.resTable = nil
	m.resErr = ""
	m.resList = listState{}
	m.clearRelated()
	m.sess.Kinds[m.ctxName] = m.kind().Ref()
	m.sess.Save()
}

func (m *Model) clearRelated() {
	m.relTable, m.relContainers, m.relPod, m.relErr, m.relKey = nil, nil, nil, "", ""
	m.relList = listState{}
	m.relMode = relNone
}

// --- rows ----------------------------------------------------------------

func (m *Model) visibleRows(t *kube.Table, filter string) []kube.Row {
	if t == nil {
		return nil
	}
	f := strings.ToLower(filter)
	out := make([]kube.Row, 0, len(t.Rows))
	for _, r := range t.Rows {
		if _, gone := m.hidden[r.UID]; gone && r.UID != "" {
			continue
		}
		if f != "" && !strings.Contains(strings.ToLower(r.Namespace+"/"+r.Name), f) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (m *Model) resRows() []kube.Row { return m.visibleRows(m.resTable, m.resList.filter) }

func (m *Model) relRows() []kube.Row {
	if m.relMode == relPods || m.relMode == relJobs {
		return m.visibleRows(m.relTable, m.relList.filter)
	}
	return nil
}

func (m *Model) relLen() int {
	switch m.relMode {
	case relContainers:
		return len(m.relContainers)
	case relPods, relJobs:
		return len(m.relRows())
	}
	return 0
}

func (m *Model) selectedRes() (kube.Row, bool) {
	rows := m.resRows()
	if m.resList.cursor >= 0 && m.resList.cursor < len(rows) {
		return rows[m.resList.cursor], true
	}
	return kube.Row{}, false
}

func (m *Model) selectedRel() (kube.Row, bool) {
	rows := m.relRows()
	if m.relList.cursor >= 0 && m.relList.cursor < len(rows) {
		return rows[m.relList.cursor], true
	}
	return kube.Row{}, false
}

func rowNS(r kube.Row, fallback string) string {
	if r.Namespace != "" {
		return r.Namespace
	}
	return fallback
}

// resTarget is the object selected in the resources panel.
func (m *Model) resTarget() target {
	r, ok := m.selectedRes()
	if !ok {
		return target{}
	}
	return target{kind: m.kind(), ns: rowNS(r, m.ns), name: r.Name, uid: r.UID, owners: r.Owners}
}

// target is what actions apply to: the related panel's item when it has
// focus (a pod of a deployment, a job of a cronjob), otherwise the
// resources panel's item. A container chosen in the related panel narrows
// a pod target.
func (m *Model) target() target {
	t := m.resTarget()
	useRel := m.focus == fRel || (m.focus == fMain && m.lastFocus == fRel)
	if useRel {
		switch m.relMode {
		case relPods, relJobs:
			if r, ok := m.selectedRel(); ok {
				k := kube.Builtin[0]
				if m.relMode == relJobs {
					k, _ = m.findKind("jobs")
				}
				return target{kind: k, ns: rowNS(r, t.ns), name: r.Name, uid: r.UID, owners: r.Owners}
			}
		case relContainers:
			if m.relList.cursor < len(m.relContainers) {
				t.container = m.relContainers[m.relList.cursor].name
			}
		}
	}
	return t
}

func (m *Model) findKind(res string) (kube.Kind, bool) {
	if m.cl != nil {
		return m.cl.KindFor(res)
	}
	for _, k := range kube.Builtin {
		if k.Resource == res {
			return k, true
		}
	}
	return kube.Kind{}, false
}

// --- focus ---------------------------------------------------------------

func (m *Model) setFocus(f int) tea.Cmd {
	if f == m.focus {
		return nil
	}
	if m.focus != fMain {
		m.lastFocus = m.focus
	}
	m.focus = f
	return m.onSelectionChanged()
}

// onSelectionChanged reloads whatever depends on the current target.
func (m *Model) onSelectionChanged() tea.Cmd {
	var cmds []tea.Cmd
	t := m.resTarget()
	relKey := t.key()
	if relKey != m.relKey {
		m.relKey = relKey
		m.relTable, m.relContainers, m.relPod, m.relErr = nil, nil, nil, ""
		m.relList = listState{}
		m.relMode = relModeFor(t.kind)
		if t.valid() {
			cmds = append(cmds, m.loadRelated())
		}
	}
	cmds = append(cmds, m.loadTab(false))
	return tea.Batch(cmds...)
}

func relModeFor(k kube.Kind) relMode {
	switch {
	case k.Resource == "pods":
		return relContainers
	case k.Resource == "cronjobs":
		return relJobs
	case k.HasPods() || k.Resource == "nodes":
		return relPods
	}
	return relNone
}

// --- tick ----------------------------------------------------------------

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type frameMsg struct{}

const frameDur = time.Second / 40

func (m *Model) startFrames() tea.Cmd {
	if m.framing {
		return nil
	}
	m.framing = true
	return tea.Tick(frameDur, func(time.Time) tea.Msg { return frameMsg{} })
}

func (m *Model) onFrame() tea.Cmd {
	dt := frameDur.Seconds()
	active := false
	if m.splash != nil {
		m.splash.Step(dt)
		if m.splash.Done() {
			m.splash = nil
		} else {
			active = true
		}
	}
	for id, a := range m.anims {
		a.anim.Step(dt)
		if a.anim.Done() {
			delete(m.anims, id)
		} else {
			active = true
		}
	}
	if !active {
		m.framing = false
		return nil
	}
	return tea.Tick(frameDur, func(time.Time) tea.Msg { return frameMsg{} })
}

func (m *Model) onTick() tea.Cmd {
	m.lastTick = time.Now()
	var cmds []tea.Cmd
	cmds = append(cmds, tickCmd())
	// Theme switched in Omarchy? Pick up the new colors.
	if src := theme.CurrentSource(); src != m.tsrc {
		m.pal, m.tsrc = theme.Load()
		m.st = NewStyles(m.pal)
	}
	for uid, at := range m.hidden {
		if time.Since(at) > 3*time.Minute {
			delete(m.hidden, uid)
		}
	}
	if m.cl == nil || m.connecting != "" {
		return tea.Batch(cmds...)
	}
	cmds = append(cmds, m.loadResources())
	if m.relKey != "" && m.relMode != relNone {
		cmds = append(cmds, m.loadRelated())
	}
	sec := m.lastTick.Unix()
	if m.tab == tabEvents || sec%10 < 2 {
		cmds = append(cmds, m.loadEvents())
	}
	if m.tab == tabRollout {
		cmds = append(cmds, m.loadRollout(false))
	}
	if sec%10 < 2 {
		cmds = append(cmds, m.loadMetrics())
	}
	if sec%30 < 2 {
		cmds = append(cmds, m.loadNamespaces())
	}
	return tea.Batch(cmds...)
}

// --- update --------------------------------------------------------------

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.ready = true
		if m.splash != nil {
			m.splash = fx.NewSplash(m.w, m.h, "kubernetes · omarchy", m.pal.RGB)
		}
		// Animations are drawn at a fixed size; drop them on resize.
		m.anims = map[int]*activeAnim{}
		return m, nil
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	case tea.PasteMsg:
		switch {
		case m.cmd != nil:
			m.cmd.edit.Paste(msg.Content)
			m.cmd.refresh(m)
		case m.prompt != nil:
			m.prompt.edit.Paste(msg.Content)
			if m.prompt.onChange != nil {
				m.prompt.onChange(m, m.prompt.edit.String())
			}
		}
		return m, nil
	case tea.MouseWheelMsg:
		return m, m.handleWheel(msg)
	case tickMsg:
		return m, m.onTick()
	case frameMsg:
		return m, m.onFrame()
	}
	return m, m.handleData(msg)
}

func (m *Model) handleWheel(msg tea.MouseWheelMsg) tea.Cmd {
	d := 3
	if msg.Button == tea.MouseWheelUp {
		d = -3
	}
	switch m.focus {
	case fMain:
		m.scrollMain(d)
	case fRes:
		m.resList.move(d, len(m.resRows()))
		return m.onSelectionChanged()
	case fRel:
		m.relList.move(d, m.relLen())
		return m.loadTab(false)
	}
	return nil
}

func (m *Model) handleKey(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	if m.splash != nil {
		m.splash = nil
		return nil
	}
	if key == "ctrl+c" {
		return m.quit()
	}
	if m.modal != nil {
		closed, cmd := m.modal.update(m, k)
		if closed {
			m.modal = nil
		}
		return cmd
	}
	if m.cmd != nil {
		cmd := m.cmd.handle(m, k)
		return tea.Batch(cmd, m.flushPending())
	}
	if m.prompt != nil {
		return m.handlePrompt(k)
	}

	// Main panel keys first when it has focus: tabs have their own keys.
	if m.focus == fMain {
		if handled, cmd := m.handleMainKey(k); handled {
			return cmd
		}
	}

	switch key {
	case "q":
		return m.quit()
	case "?", "f1":
		m.modal = newHelpModal(m)
		return nil
	case ":":
		m.openCmdLine("")
		return m.flushPending()
	case "tab":
		return m.setFocus((m.focus + 1) % 5)
	case "shift+tab":
		return m.setFocus((m.focus + 4) % 5)
	case "0":
		return m.setFocus(fMain)
	case "1":
		return m.setFocus(fCtx)
	case "2":
		return m.setFocus(fNs)
	case "3":
		return m.setFocus(fRes)
	case "4":
		return m.setFocus(fRel)
	case "C":
		m.modal = m.contextPicker()
		return nil
	case "N":
		m.modal = m.namespacePicker()
		return nil
	case "K":
		return m.openKindPicker()
	case "ctrl+r":
		return m.refreshAll()
	case "esc":
		if m.focus == fMain {
			return m.setFocus(m.lastFocus)
		}
		if m.focus == fRes && m.resList.filter != "" {
			m.resList.filter = ""
			return m.onSelectionChanged()
		}
		if m.focus == fRel && m.relList.filter != "" {
			m.relList.filter = ""
			return nil
		}
		if m.focus == fRel {
			return m.setFocus(fRes)
		}
		return nil
	}

	switch m.focus {
	case fCtx:
		return m.handleListKey(k, &m.ctxList, len(m.contexts), func() tea.Cmd {
			if m.ctxList.cursor < len(m.contexts) {
				return m.switchContext(m.contexts[m.ctxList.cursor])
			}
			return nil
		}, nil)
	case fNs:
		return m.handleListKey(k, &m.nsList, len(m.nsItems), func() tea.Cmd {
			if m.nsList.cursor < len(m.nsItems) {
				return m.switchNamespace(m.nsItems[m.nsList.cursor])
			}
			return nil
		}, nil)
	case fRes:
		switch key {
		case "[", "left":
			m.kindIdx = (m.kindIdx + len(m.kinds) - 1) % len(m.kinds)
			m.onKindChanged()
			return tea.Batch(m.loadResources(), m.onSelectionChanged())
		case "]", "right":
			m.kindIdx = (m.kindIdx + 1) % len(m.kinds)
			m.onKindChanged()
			return tea.Batch(m.loadResources(), m.onSelectionChanged())
		}
		if cmd, ok := m.handleTargetKey(k); ok {
			return cmd
		}
		return m.handleListKey(k, &m.resList, len(m.resRows()), func() tea.Cmd {
			t := m.resTarget()
			if t.kind.Resource == "pods" {
				return m.startExec(t, false)
			}
			if m.relMode != relNone {
				return m.setFocus(fRel)
			}
			m.tab = tabDescribe
			return tea.Batch(m.setFocus(fMain), m.loadTab(true))
		}, func() {
			m.startFilter(&m.resList)
		})
	case fRel:
		if cmd, ok := m.handleTargetKey(k); ok {
			return cmd
		}
		return m.handleListKey(k, &m.relList, m.relLen(), func() tea.Cmd {
			t := m.target()
			if t.kind.Resource == "pods" {
				return m.startExec(t, false)
			}
			m.tab = tabDescribe
			return tea.Batch(m.setFocus(fMain), m.loadTab(true))
		}, func() {
			m.startFilter(&m.relList)
		})
	case fMain:
		if cmd, ok := m.handleTargetKey(k); ok {
			return cmd
		}
	}
	return nil
}

// handleListKey moves a list; enter calls onEnter, / calls onFilter.
func (m *Model) handleListKey(k tea.KeyPressMsg, l *listState, n int, onEnter func() tea.Cmd, onFilter func()) tea.Cmd {
	page := max(m.h/3, 5)
	before := l.cursor
	switch k.String() {
	case "j", "down":
		l.move(1, n)
	case "k", "up":
		l.move(-1, n)
	case "g", "home":
		l.cursor = 0
	case "G", "end":
		l.cursor = max(n-1, 0)
	case "ctrl+d", "pgdown":
		l.move(page, n)
	case "ctrl+u", "pgup":
		l.move(-page, n)
	case "enter":
		if onEnter != nil {
			return onEnter()
		}
	case "/":
		if onFilter != nil {
			onFilter()
		}
		return nil
	}
	if l.cursor != before {
		return m.onSelectionChanged()
	}
	return nil
}

// handleTargetKey runs actions on the current target.
func (m *Model) handleTargetKey(k tea.KeyPressMsg) (tea.Cmd, bool) {
	t := m.target()
	switch k.String() {
	case "l":
		m.tab = tabLogs
		return m.loadTab(true), true
	case "i":
		m.tab = tabDescribe
		return m.loadTab(true), true
	case "y":
		m.tab = tabYAML
		return m.loadTab(true), true
	case "e":
		m.tab = tabEvents
		return m.loadTab(true), true
	case "u":
		m.tab = tabRollout
		return tea.Batch(m.loadTab(true), m.setFocus(fMain)), true
	case "o":
		m.tab = tabOutput
		return nil, true
	case "L":
		m.askSelector()
		return nil, true
	case "ctrl+enter", "X", "ctrl+j":
		return m.startExec(t, true), true
	case "x":
		return m.startExec(t, false), true
	case "b":
		return m.startDebug(t, false), true
	case "B":
		return m.startDebug(t, true), true
	case "d", "delete":
		return m.requestDelete(t, false), true
	case "D", "shift+delete":
		return m.requestDelete(t, true), true
	case "s":
		return m.openScale(t), true
	case "+", "=":
		return m.scaleBy(t, 1), true
	case "-", "_":
		return m.scaleBy(t, -1), true
	case "r":
		return m.requestRestart(t), true
	}
	return nil, false
}

func (m *Model) flushPending() tea.Cmd {
	if len(m.pendingNames) == 0 {
		return nil
	}
	cmds := m.pendingNames
	m.pendingNames = nil
	return tea.Batch(cmds...)
}

func (m *Model) quit() tea.Cmd {
	m.logs.stop()
	if m.cancel != nil {
		m.cancel()
	}
	m.sess.Context = m.ctxName
	m.sess.Save()
	return tea.Quit
}

func (m *Model) refreshAll() tea.Cmd {
	m.nameCache = map[string][]string{}
	if err := m.kcfg.Reload(); err == nil {
		m.contexts = m.kcfg.Contexts()
	}
	m.setStatus(i18n.T("status.loading"))
	return tea.Batch(m.loadResources(), m.loadNamespaces(), m.loadRelated(), m.loadTab(true), m.loadEvents(), m.loadMetrics())
}

// --- filter & prompts ----------------------------------------------------

type prompt struct {
	label    string
	edit     lineEdit
	onChange func(m *Model, v string)
	onSubmit func(m *Model, v string) tea.Cmd
	onCancel func(m *Model)
}

func (m *Model) startFilter(l *listState) {
	orig := l.filter
	m.prompt = &prompt{
		label: "/",
		edit:  newLineEdit(l.filter),
		onChange: func(m *Model, v string) {
			l.filter = v
			l.cursor, l.offset = 0, 0
		},
		onSubmit: func(m *Model, v string) tea.Cmd { return m.onSelectionChanged() },
		onCancel: func(m *Model) {
			l.filter = orig
		},
	}
}

func (m *Model) handlePrompt(k tea.KeyPressMsg) tea.Cmd {
	p := m.prompt
	switch k.String() {
	case "esc":
		if p.onCancel != nil {
			p.onCancel(m)
		}
		m.prompt = nil
		return nil
	case "enter":
		m.prompt = nil
		if p.onSubmit != nil {
			return p.onSubmit(m, p.edit.String())
		}
		return nil
	}
	if p.edit.Handle(k) && p.onChange != nil {
		p.onChange(m, p.edit.String())
	}
	return nil
}
