package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/gnome627/komar/internal/fx"
	"github.com/gnome627/komar/internal/i18n"
	"github.com/gnome627/komar/internal/kube"
	"github.com/gnome627/komar/internal/shell"
)

type actionMsg struct {
	gen   int
	op    string
	t     target
	ok    string
	err   error
	panel int
}

type deleteCheckMsg struct {
	gen      int
	t        target
	force    bool
	replicas int64
	allowed  bool
	reason   string
	user     string
}

type scaleInfoMsg struct {
	gen     int
	t       target
	desired int
	ready   int
	err     error
}

type execCheckMsg struct {
	gen        int
	t          target
	debug      bool
	allowed    bool
	reason     string
	user       string
	containers []string
	err        error
}

type execDoneMsg struct {
	t      target
	err    error
	stderr string
	debug  bool
}

func kindLabel(t target) string { return strings.ToLower(t.kind.KindName) }

// --- delete --------------------------------------------------------------

// requestDelete checks RBAC first (so a doomed delete doesn't play its
// effect), then asks for confirmation, except for pods whose controller
// keeps more than one replica: those come back on their own.
//
// Failed pods (Evicted, Error, ContainerStatusUnknown…) skip all of that:
// they are leftovers, and there are often dozens to clear, so d deletes
// right away and moves on to the next row.
func (m *Model) requestDelete(t target, force bool) tea.Cmd {
	if !t.valid() || m.cl == nil {
		return nil
	}
	if _, already := m.dying[t.uid]; already {
		return nil
	}
	if t.failed && t.kind.Resource == "pods" {
		return m.doDelete(t, force)
	}
	cl, gen := m.cl, m.gen
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		msg := deleteCheckMsg{gen: gen, t: t, force: force, allowed: true}
		ns := ""
		if t.kind.Namespaced {
			ns = t.ns
		}
		if ok, reason, err := cl.CanI(ctx, "delete", t.kind.Group, t.kind.Resource, "", ns); err == nil && !ok {
			msg.allowed, msg.reason = false, reason
			msg.user = cl.WhoAmI(ctx)
			return msg
		}
		if t.kind.Resource == "pods" && !force && len(t.owners) > 0 {
			msg.replicas = cl.OwnerReplicas(ctx, t.ns, t.owners)
		}
		return msg
	}
}

func (m *Model) onDeleteCheck(msg deleteCheckMsg) tea.Cmd {
	if msg.gen != m.gen {
		return nil
	}
	t := msg.t
	if !msg.allowed {
		res := t.kind.Resource
		if t.kind.Group != "" {
			res += "." + t.kind.Group
		}
		body := i18n.T("exec.forbidden_body", msg.user, "delete", res, t.ns, "delete", res, t.ns)
		if msg.reason != "" {
			body += "\n\n" + i18n.T("exec.reason", msg.reason)
		}
		m.modal = &infoModal{title: "⛔ " + i18n.T("exec.forbidden"), body: body, danger: true}
		return nil
	}
	if msg.replicas > 1 {
		return m.doDelete(t, msg.force)
	}
	m.confirmDelete(t, msg.force)
	return nil
}

func (m *Model) confirmDelete(t target, force bool) {
	q := i18n.T("confirm.delete", kindLabel(t), t.name)
	if force {
		q = i18n.T("confirm.force_delete", kindLabel(t), t.name)
	}
	body := ""
	if t.kind.Namespaced {
		body = "namespace: " + t.ns + "\ncontext:   " + m.ctxName
	} else {
		body = i18n.T("confirm.not_ns_scoped") + "\ncontext: " + m.ctxName
	}
	m.modal = &confirmModal{title: i18n.T("confirm.title"), question: q, body: body, danger: true,
		onYes: func(m *Model) tea.Cmd { return m.doDelete(t, force) }}
}

// panelOf tells which panel lists the target (for the delete effect).
func (m *Model) panelOf(t target) int {
	if r, ok := m.selectedRel(); ok && r.UID == t.uid && (m.relMode == relPods || m.relMode == relJobs) {
		return fRel
	}
	return fRes
}

func (m *Model) doDelete(t target, force bool) tea.Cmd {
	panel := m.panelOf(t)
	if !m.startDeleteEffect(t, panel) && t.uid != "" {
		m.hidden[t.uid] = time.Now()
	}
	cl, gen := m.cl, m.gen
	cmds := []tea.Cmd{m.startFrames(), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := cl.Delete(ctx, t.kind, t.ns, t.name, force)
		return actionMsg{gen: gen, op: "delete", t: t, err: err, panel: panel,
			ok: i18n.T("delete.ok", kindLabel(t), t.name)}
	}}
	// The row stays in place while it burns; step past it so the next d
	// lands on the next row.
	if _, burning := m.dying[t.uid]; burning {
		switch panel {
		case fRes:
			m.resList.move(1, len(m.resRows()))
			cmds = append(cmds, m.onSelectionChanged())
		case fRel:
			m.relList.move(1, m.relLen())
			cmds = append(cmds, m.loadTab(false))
		}
	}
	return tea.Batch(cmds...)
}

// startDeleteEffect animates the selected row away, in place: the row
// keeps its line in the list until the effect ends (see bury) and the rest
// of the panel stays live. It reports whether an effect was started.
func (m *Model) startDeleteEffect(t target, panel int) bool {
	if m.conf.DisableEffects || !m.ready || t.uid == "" {
		return false
	}
	g := m.layout()
	var w, h, rowY int
	var base string
	switch panel {
	case fRes:
		w, h = g.leftW, g.resH
		base = m.renderResPanel(w, h)
		rowY = 2 + m.resList.cursor - m.resList.offset
	case fRel:
		w, h = g.leftW, g.relH
		base = m.renderRelPanel(w, h)
		rowY = 2 + m.relList.cursor - m.relList.offset
	default:
		return false
	}
	if w < 4 || rowY < 1 || rowY >= h-1 {
		return false
	}
	name := fx.Pick(m.conf.Effects)
	a := fx.New(name, base, w, h, []fx.Rect{{X: 1, Y: rowY, W: w - 2, H: 1}}, fx.Rect{X: 1, Y: 1, W: w - 2, H: h - 2}, m.pal.RGB)
	m.dying[t.uid] = &dyingRow{anim: a, panel: panel, y: rowY}
	return true
}

// panelList is the table and list state behind a panel that can hold
// dying rows.
func (m *Model) panelList(panel int) (*kube.Table, *listState) {
	if panel == fRel {
		if m.relMode != relPods && m.relMode != relJobs {
			return nil, &m.relList
		}
		return m.relTable, &m.relList
	}
	return m.resTable, &m.resList
}

// bury takes a dying row out of its list, keeping the cursor on the object
// it was on (the rows below move up by one).
func (m *Model) bury(uid string) {
	d, ok := m.dying[uid]
	if !ok {
		return
	}
	delete(m.dying, uid)
	t, l := m.panelList(d.panel)
	rows := m.visibleRows(t, l.filter)
	selected := ""
	if l.cursor >= 0 && l.cursor < len(rows) && rows[l.cursor].UID != uid {
		selected = rows[l.cursor].UID
	}
	m.hidden[uid] = time.Now()
	left := m.visibleRows(t, l.filter)
	for i, r := range left {
		if r.UID == selected {
			l.cursor = i
		}
	}
	l.cursor = max(min(l.cursor, len(left)-1), 0)
}

// keepDying carries rows whose effect is still playing over to a fresh
// table: the server drops a deleted pod at once, and without this the row
// would vanish mid-effect.
func (m *Model) keepDying(old, fresh *kube.Table, panel int) {
	if old == nil || fresh == nil || len(m.dying) == 0 {
		return
	}
	have := make(map[string]bool, len(fresh.Rows))
	for _, r := range fresh.Rows {
		have[r.UID] = true
	}
	for i, r := range old.Rows {
		d, ok := m.dying[r.UID]
		if !ok || d.panel != panel || have[r.UID] {
			continue
		}
		at := min(i, len(fresh.Rows))
		fresh.Rows = append(fresh.Rows[:at], append([]kube.Row{r}, fresh.Rows[at:]...)...)
	}
}

// overlayDying draws the running delete effects over a freshly rendered
// panel.
func (m *Model) overlayDying(panel, w, h int, base string) string {
	if len(m.dying) == 0 {
		return base
	}
	t, l := m.panelList(panel)
	rows := m.visibleRows(t, l.filter)
	var layers []fx.Layer
	for i, r := range rows {
		d, ok := m.dying[r.UID]
		if !ok || d.panel != panel || d.anim.W != w || d.anim.H != h {
			continue
		}
		y := 2 + i - l.offset
		if y < 2 || y >= h-1 {
			continue
		}
		layers = append(layers, fx.Layer{Anim: d.anim, DY: y - d.y})
	}
	if len(layers) == 0 {
		return base
	}
	return fx.Compose(base, w, h, layers)
}

// --- scale ---------------------------------------------------------------

func (m *Model) openScale(t target) tea.Cmd {
	if !t.valid() {
		return nil
	}
	if !t.kind.Scalable() {
		m.setError(i18n.T("scale.unsupported", kindLabel(t)))
		return nil
	}
	cl, gen := m.cl, m.gen
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		d, r, err := cl.Replicas(ctx, t.kind, t.ns, t.name)
		return scaleInfoMsg{gen: gen, t: t, desired: d, ready: r, err: err}
	}
}

func (m *Model) onScaleInfo(msg scaleInfoMsg) tea.Cmd {
	if msg.gen != m.gen {
		return nil
	}
	if msg.err != nil {
		m.setError(i18n.T("scale.fail", kube.ErrString(msg.err)))
		return nil
	}
	m.modal = &scaleModal{t: msg.t, orig: msg.desired, value: msg.desired, ready: msg.ready}
	return nil
}

func (m *Model) scaleBy(t target, d int) tea.Cmd {
	if !t.valid() {
		return nil
	}
	if !t.kind.Scalable() {
		m.setError(i18n.T("scale.unsupported", kindLabel(t)))
		return nil
	}
	cl, gen := m.cl, m.gen
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cur, _, err := cl.Replicas(ctx, t.kind, t.ns, t.name)
		if err != nil {
			return actionMsg{gen: gen, op: "scale", t: t, err: err}
		}
		n := max(cur+d, 0)
		err = cl.Scale(ctx, t.kind, t.ns, t.name, n)
		return actionMsg{gen: gen, op: "scale", t: t, err: err, ok: i18n.T("scale.ok", t.name, n)}
	}
}

func (m *Model) scaleTo(t target, n int) tea.Cmd {
	cl, gen := m.cl, m.gen
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := cl.Scale(ctx, t.kind, t.ns, t.name, n)
		return actionMsg{gen: gen, op: "scale", t: t, err: err, ok: i18n.T("scale.ok", t.name, n)}
	}
}

// --- rollout -------------------------------------------------------------

func (m *Model) requestRestart(t target) tea.Cmd {
	if !t.valid() {
		return nil
	}
	if !t.kind.Rollable() {
		m.setError(i18n.T("restart.unsupported", kindLabel(t)))
		return nil
	}
	m.modal = &confirmModal{title: i18n.T("confirm.title"), question: i18n.T("confirm.restart", kindLabel(t), t.name),
		body: "namespace: " + t.ns + "\ncontext:   " + m.ctxName,
		onYes: func(m *Model) tea.Cmd {
			cl, gen := m.cl, m.gen
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				err := cl.RolloutRestart(ctx, t.kind, t.ns, t.name)
				return actionMsg{gen: gen, op: "restart", t: t, err: err, ok: i18n.T("restart.ok", t.name)}
			}
		}}
	return nil
}

func (m *Model) requestUndo(t target, rev int64) tea.Cmd {
	m.modal = &confirmModal{title: i18n.T("confirm.title"), question: i18n.T("confirm.undo", kindLabel(t), t.name, rev),
		body: "namespace: " + t.ns + "\ncontext:   " + m.ctxName, danger: true,
		onYes: func(m *Model) tea.Cmd {
			cl, gen := m.cl, m.gen
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				args := append(cl.KubectlArgs(), "-n", t.ns, "rollout", "undo", t.kind.Ref()+"/"+t.name,
					fmt.Sprintf("--to-revision=%d", rev))
				cmd, err := shell.Command(ctx, cl.KubectlEnv(), args...)
				if err == nil {
					var out []byte
					out, err = cmd.CombinedOutput()
					if err != nil {
						err = fmt.Errorf("%s", strings.TrimSpace(string(out)))
					}
				}
				return actionMsg{gen: gen, op: "undo", t: t, err: err, ok: i18n.T("undo.ok", t.name, rev)}
			}
		}}
	return nil
}

func (m *Model) onAction(msg actionMsg) tea.Cmd {
	if msg.gen != m.gen {
		return nil
	}
	if msg.err != nil {
		if msg.op == "delete" {
			delete(m.dying, msg.t.uid)
			delete(m.hidden, msg.t.uid)
		}
		if kube.IsForbidden(msg.err) {
			m.showForbidden(msg.err.Error())
			return nil
		}
		m.setError(i18n.T(msg.op+".fail", kube.ErrString(msg.err)))
		return m.loadResources()
	}
	m.setStatus("✓ " + msg.ok)
	cmds := []tea.Cmd{m.loadResources(), m.loadRelated()}
	switch msg.op {
	case "restart", "undo":
		m.tab = tabRollout
		cmds = append(cmds, m.loadRollout(true))
	}
	return tea.Batch(cmds...)
}

// --- exec / debug --------------------------------------------------------

// execPod resolves the pod to exec into: the target itself, or for a
// workload the selected/first running pod in the related panel.
func (m *Model) execPod(t target) (target, bool) {
	if t.kind.Resource == "pods" {
		return t, true
	}
	if m.relMode == relPods {
		rows := m.relRows()
		if r, ok := m.selectedRel(); ok {
			return target{kind: kube.Builtin[0], ns: rowNS(r, t.ns), name: r.Name, uid: r.UID}, true
		}
		for _, r := range rows {
			if !r.Deleting {
				return target{kind: kube.Builtin[0], ns: rowNS(r, t.ns), name: r.Name, uid: r.UID}, true
			}
		}
	}
	return target{}, false
}

// startExec opens a shell in the pod. It always goes to its own floating
// terminal window, so komar stays on screen next to it.
func (m *Model) startExec(t target) tea.Cmd {
	pod, ok := m.execPod(t)
	if !ok || m.cl == nil {
		m.setError(i18n.T("exec.no_pod"))
		return nil
	}
	return m.checkAccess(pod, false)
}

func (m *Model) startDebug(t target) tea.Cmd {
	pod, ok := m.execPod(t)
	if !ok || m.cl == nil {
		m.setError(i18n.T("debug.not_pod"))
		return nil
	}
	return m.checkAccess(pod, true)
}

// checkAccess asks the API server whether we may exec (or attach an
// ephemeral debug container) before trying, so a missing permission is
// reported plainly instead of as a cryptic kubectl error.
func (m *Model) checkAccess(t target, debug bool) tea.Cmd {
	cl, gen := m.cl, m.gen
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		msg := execCheckMsg{gen: gen, t: t, debug: debug}
		verb, sub := "create", "exec"
		if debug {
			verb, sub = "patch", "ephemeralcontainers"
		}
		msg.allowed, msg.reason, msg.err = cl.CanI(ctx, verb, "", "pods", sub, t.ns)
		msg.user = cl.WhoAmI(ctx)
		if msg.err != nil {
			// Can't ask (old cluster, review API blocked): let kubectl try.
			msg.allowed, msg.err = true, nil
		}
		if p, err := cl.Pod(ctx, t.ns, t.name); err == nil {
			for _, c := range p.Spec.Containers {
				msg.containers = append(msg.containers, c.Name)
			}
		}
		return msg
	}
}

func (m *Model) onExecCheck(msg execCheckMsg) tea.Cmd {
	if msg.gen != m.gen {
		return nil
	}
	t := msg.t
	if !msg.allowed {
		verb, res := "create", "pods/exec"
		title := i18n.T("exec.forbidden")
		if msg.debug {
			verb, res = "patch", "pods/ephemeralcontainers"
			title = i18n.T("debug.forbidden")
		}
		body := i18n.T("exec.forbidden_body", msg.user, verb, res, t.ns, verb, res, t.ns)
		if msg.reason != "" {
			body += "\n\n" + i18n.T("exec.reason", msg.reason)
		}
		m.modal = &infoModal{title: "⛔ " + title, body: body, danger: true}
		return nil
	}
	run := func(m *Model, container string) tea.Cmd {
		t.container = container
		if msg.debug {
			return m.askDebugImage(t)
		}
		return m.runExec(t)
	}
	if t.container == "" && len(msg.containers) > 1 {
		m.modal = &pickerModal{id: "container", title: i18n.T("exec.pick_container"), items: msg.containers,
			onPick: func(m *Model, v string) tea.Cmd { return run(m, v) }}
		m.modal.(*pickerModal).refilter()
		return nil
	}
	return run(m, t.container)
}

func (m *Model) askDebugImage(t target) tea.Cmd {
	m.prompt = &prompt{
		label: i18n.T("debug.image") + ":",
		edit:  newLineEdit(m.conf.DebugImage),
		onSubmit: func(m *Model, img string) tea.Cmd {
			img = strings.TrimSpace(img)
			if img == "" {
				return nil
			}
			args := shell.DebugArgs(m.ctxName, t.ns, t.name, t.container, img)
			return m.runInteractive(t, args, true)
		},
	}
	return nil
}

func (m *Model) runExec(t target) tea.Cmd {
	return m.runInteractive(t, shell.ExecArgs(m.ctxName, t.ns, t.name, t.container), false)
}

// openWindow runs kubectl in its own floating terminal window, so komar
// stays on screen next to it. Everything that needs the terminal goes
// there: shells, debug containers, editors, port-forwards. It reports
// false when no terminal emulator can be found; the caller then runs the
// command in place.
func (m *Model) openWindow(label string, args []string) bool {
	k, err := shell.Kubectl()
	if err != nil {
		return false
	}
	term, err := shell.FindTerminal()
	if err != nil {
		return false
	}
	if err := term.Launch(m.cl.KubectlEnv(), append([]string{k}, args...)); err != nil {
		return false
	}
	m.setStatus("↗ " + label + " — " + i18n.T("exec.new_window") + " (" + term.Name + ")")
	return true
}

// runInteractive runs kubectl with the terminal: in its own window, or
// here (komar steps aside until it exits) when no window can be opened.
func (m *Model) runInteractive(t target, args []string, debug bool) tea.Cmd {
	if m.openWindow(t.name, args) {
		return nil
	}
	cmd, err := shell.Command(nil, m.cl.KubectlEnv(), args...)
	if err != nil {
		m.setError(err.Error())
		return nil
	}
	buf := &tailBuffer{max: 8192}
	cmd.Stderr = io.MultiWriter(os.Stderr, buf)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return execDoneMsg{t: t, err: err, stderr: buf.String(), debug: debug}
	})
}

func (m *Model) onExecDone(msg execDoneMsg) tea.Cmd {
	switch {
	case msg.err == nil:
		return m.loadRelated()
	case shell.NoShell(msg.stderr) && !msg.debug:
		m.modal = &infoModal{title: msg.t.name, body: i18n.T("exec.no_shell"), debugHint: true}
	case shell.Forbidden(msg.stderr):
		m.showForbidden(strings.TrimSpace(msg.stderr))
	default:
		detail := strings.TrimSpace(lastLine(msg.stderr))
		if detail == "" {
			detail = msg.err.Error()
		}
		m.setError(i18n.T("exec.failed", detail))
	}
	return nil
}

// showForbidden explains an RBAC denial in plain words.
func (m *Model) showForbidden(raw string) {
	user := m.user
	if user == "" && m.cl != nil {
		user = m.cl.KubeUser
	}
	m.modal = &infoModal{title: "⛔ " + i18n.T("exec.forbidden"), body: "user: " + user + "\n\n" + raw, danger: true}
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
