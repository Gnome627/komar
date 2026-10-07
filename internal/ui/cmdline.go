package ui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/gnome627/komar/internal/i18n"
	"github.com/gnome627/komar/internal/kube"
	"github.com/gnome627/komar/internal/shell"
)

// cmdLine is the ":" prompt for raw kubectl. The current context and
// namespace are added unless the command sets them itself.
type cmdLine struct {
	edit    lineEdit
	histIdx int
	draft   string
	sugg    []string
	suggIdx int
	partial string
}

type cmdOutputMsg struct {
	line        string
	out         string
	code        int
	err         error
	interactive bool
}

var kubectlVerbs = []string{
	"get", "describe", "logs", "exec", "delete", "apply", "edit", "rollout", "scale", "top", "port-forward",
	"explain", "label", "annotate", "patch", "create", "run", "cp", "debug", "auth", "api-resources",
	"api-versions", "config", "cordon", "uncordon", "drain", "taint", "diff", "wait", "events", "set",
	"version", "cluster-info", "attach", "expose", "autoscale", "certificate", "kustomize", "replace",
}

var internalCmds = []string{"ctx", "ns", "q"}

var resourceVerbs = map[string]bool{
	"get": true, "describe": true, "delete": true, "edit": true, "label": true, "annotate": true, "patch": true,
	"scale": true, "explain": true, "wait": true, "set": true, "expose": true, "autoscale": true,
}

var podVerbs = map[string]bool{"logs": true, "exec": true, "attach": true, "port-forward": true, "cp": true, "debug": true}

var commonFlags = []string{"-n", "-A", "-o", "-l", "-w", "-f", "-c", "-it", "--all-namespaces", "--watch",
	"--previous", "--tail=", "--since=", "--force", "--grace-period=0", "--replicas=", "--to-revision=",
	"--show-labels", "--sort-by=", "--field-selector=", "--selector=", "--dry-run=client", "--context", "--namespace"}

var rolloutSub = []string{"status", "history", "undo", "restart", "pause", "resume"}

func (m *Model) openCmdLine(initial string) {
	m.cmd = &cmdLine{edit: newLineEdit(initial), histIdx: -1, suggIdx: -1}
	m.cmd.refresh(m)
}

func (c *cmdLine) handle(m *Model, k tea.KeyPressMsg) tea.Cmd {
	switch hotkey(k) {
	case "esc", "ctrl+g":
		if c.suggIdx >= 0 {
			c.suggIdx = -1
			return nil
		}
		m.cmd = nil
		return nil
	case "enter":
		if c.suggIdx >= 0 && c.suggIdx < len(c.sugg) {
			c.accept(m, c.sugg[c.suggIdx])
			return nil
		}
		line := strings.TrimSpace(c.edit.String())
		m.cmd = nil
		if line == "" {
			return nil
		}
		return m.runCmdLine(line)
	case "tab":
		if len(c.sugg) == 0 {
			return nil
		}
		if len(c.sugg) == 1 {
			c.accept(m, c.sugg[0])
			return nil
		}
		// Complete the common prefix first, then cycle.
		if cp := commonPrefix(c.sugg); len(cp) > len(c.partial) && c.suggIdx < 0 {
			c.replacePartial(cp, false)
			c.refresh(m)
			return nil
		}
		c.suggIdx = (c.suggIdx + 1) % len(c.sugg)
		return nil
	case "shift+tab", "ctrl+p":
		if len(c.sugg) > 0 {
			c.suggIdx = (c.suggIdx - 1 + len(c.sugg)) % len(c.sugg)
		}
		return nil
	case "ctrl+n":
		if len(c.sugg) > 0 {
			c.suggIdx = (c.suggIdx + 1) % len(c.sugg)
		}
		return nil
	case "up":
		items := m.hist.Items
		if len(items) == 0 {
			return nil
		}
		if c.histIdx < 0 {
			c.draft = c.edit.String()
			c.histIdx = len(items)
		}
		if c.histIdx > 0 {
			c.histIdx--
			c.edit.Set(items[c.histIdx])
		}
		c.sugg, c.suggIdx = nil, -1
		return nil
	case "down":
		items := m.hist.Items
		if c.histIdx < 0 {
			return nil
		}
		c.histIdx++
		if c.histIdx >= len(items) {
			c.histIdx = -1
			c.edit.Set(c.draft)
		} else {
			c.edit.Set(items[c.histIdx])
		}
		c.sugg, c.suggIdx = nil, -1
		return nil
	}
	if c.edit.Handle(k) {
		c.histIdx = -1
		c.refresh(m)
	}
	return nil
}

func commonPrefix(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	p := xs[0]
	for _, x := range xs[1:] {
		for !strings.HasPrefix(x, p) {
			p = p[:len(p)-1]
		}
	}
	return p
}

func (c *cmdLine) accept(m *Model, s string) {
	c.replacePartial(s, !strings.HasSuffix(s, "/") && !strings.HasSuffix(s, "="))
	c.suggIdx = -1
	c.refresh(m)
}

func (c *cmdLine) replacePartial(s string, space bool) {
	v := string(c.edit.val[:c.edit.pos])
	rest := string(c.edit.val[c.edit.pos:])
	v = v[:len(v)-len(c.partial)] + s
	if space {
		v += " "
	}
	c.edit.Set(v + rest)
	c.edit.pos = len([]rune(v))
}

// refresh recomputes completions for the word under the cursor.
func (c *cmdLine) refresh(m *Model) {
	before := string(c.edit.val[:c.edit.pos])
	fields := strings.Fields(before)
	partial := ""
	if !strings.HasSuffix(before, " ") && len(fields) > 0 {
		partial = fields[len(fields)-1]
		fields = fields[:len(fields)-1]
	}
	if len(fields) > 0 && fields[0] == "kubectl" {
		fields = fields[1:]
	}
	c.partial = partial
	c.sugg = filterPrefix(m.completions(fields, partial), partial)
	if len(c.sugg) == 1 && c.sugg[0] == partial {
		c.sugg = nil
	}
	if c.suggIdx >= len(c.sugg) {
		c.suggIdx = -1
	}
}

func filterPrefix(cands []string, partial string) []string {
	p := strings.ToLower(partial)
	var pre, sub []string
	seen := map[string]bool{}
	for _, cnd := range cands {
		if seen[cnd] {
			continue
		}
		seen[cnd] = true
		l := strings.ToLower(cnd)
		switch {
		case strings.HasPrefix(l, p):
			pre = append(pre, cnd)
		case p != "" && strings.Contains(l, p):
			sub = append(sub, cnd)
		}
	}
	out := append(pre, sub...)
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

func (m *Model) completions(fields []string, partial string) []string {
	if len(fields) == 0 {
		return append(append([]string{}, kubectlVerbs...), internalCmds...)
	}
	last := fields[len(fields)-1]
	switch last {
	case "-n", "--namespace":
		return m.nsItems[1:]
	case "--context":
		return m.contexts
	case "-o", "--output":
		return []string{"yaml", "json", "wide", "name", "jsonpath=", "custom-columns="}
	case "-c", "--container":
		return nil
	}
	if strings.HasPrefix(partial, "-") {
		return commonFlags
	}
	verb := fields[0]
	switch verb {
	case "ctx":
		return m.contexts
	case "ns":
		return m.nsItems[1:]
	}
	var pos []string
	for i, f := range fields[1:] {
		if strings.HasPrefix(f, "-") {
			continue
		}
		prev := fields[i]
		if prev == "-n" || prev == "--namespace" || prev == "-o" || prev == "--context" || prev == "-c" || prev == "-l" {
			continue
		}
		pos = append(pos, f)
	}
	if verb == "rollout" {
		if len(pos) == 0 {
			return rolloutSub
		}
		pos = pos[1:]
		if len(pos) == 0 {
			return m.kindOrRefCandidates(partial, []string{"deployment", "statefulset", "daemonset"})
		}
		return nil
	}
	if verb == "top" {
		if len(pos) == 0 {
			return []string{"pod", "node"}
		}
		return m.namesFor(pos[0])
	}
	if verb == "auth" {
		return []string{"can-i", "whoami", "reconcile"}
	}
	if podVerbs[verb] {
		if strings.Contains(partial, "/") {
			return m.kindOrRefCandidates(partial, nil)
		}
		if len(pos) == 0 {
			return m.namesFor("pods")
		}
		return nil
	}
	if resourceVerbs[verb] {
		if strings.Contains(partial, "/") {
			return m.kindOrRefCandidates(partial, nil)
		}
		if len(pos) == 0 {
			return m.kindNames()
		}
		return m.namesFor(pos[0])
	}
	return nil
}

func (m *Model) kindNames() []string {
	var out []string
	for _, k := range kube.Builtin {
		out = append(out, k.Resource)
		if k.Short != "" {
			out = append(out, k.Short)
		}
	}
	for _, k := range m.allKinds {
		out = append(out, k.Resource)
		if k.Short != "" {
			out = append(out, k.Short)
		}
	}
	return out
}

// kindOrRefCandidates completes "kind/name" forms.
func (m *Model) kindOrRefCandidates(partial string, kinds []string) []string {
	if i := strings.Index(partial, "/"); i >= 0 {
		kind := partial[:i]
		var out []string
		for _, n := range m.namesFor(kind) {
			out = append(out, kind+"/"+n)
		}
		return out
	}
	if kinds == nil {
		kinds = m.kindNames()
	}
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, k+"/")
	}
	return out
}

// namesFor returns object names of a kind in the current namespace,
// fetching them in the background the first time.
func (m *Model) namesFor(kindName string) []string {
	if m.cl == nil {
		return nil
	}
	k, ok := m.cl.KindFor(kindName)
	if !ok {
		return nil
	}
	key := k.Ref() + "|" + m.ns
	if names, ok := m.nameCache[key]; ok {
		return names
	}
	// Use what's already on screen while fetching.
	if m.resTable != nil && m.kind().Ref() == k.Ref() {
		var names []string
		for _, r := range m.resTable.Rows {
			names = append(names, r.Name)
		}
		m.nameCache[key] = names
		return names
	}
	m.nameCache[key] = nil
	cl, ns := m.cl, m.ns
	m.pendingNames = append(m.pendingNames, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		t, err := cl.ListTable(ctx, k, kube.ListOptions{Namespace: ns})
		if err != nil {
			return namesMsg{key: key}
		}
		var names []string
		for _, r := range t.Rows {
			names = append(names, r.Name)
		}
		sort.Strings(names)
		return namesMsg{key: key, names: names}
	})
	return nil
}

// runCmdLine executes a command line typed after ":".
func (m *Model) runCmdLine(line string) tea.Cmd {
	m.hist.Add(line)
	args, err := shell.SplitArgs(line)
	if err != nil {
		m.setError(err.Error())
		return nil
	}
	if len(args) > 0 && args[0] == "kubectl" {
		args = args[1:]
	}
	if len(args) == 0 {
		return nil
	}
	switch args[0] {
	case "q", "quit":
		return m.quit()
	case "ctx":
		if len(args) > 1 {
			return m.switchContext(args[1])
		}
		m.modal = m.contextPicker()
		return nil
	case "ns":
		if len(args) > 1 {
			ns := args[1]
			if ns == "*" || ns == "all" {
				ns = ""
			}
			return m.switchNamespace(ns)
		}
		m.modal = m.namespacePicker()
		return nil
	}
	if m.cl == nil {
		return nil
	}
	full := m.injectFlags(args)
	display := "kubectl " + strings.Join(full, " ")
	if shell.Interactive(args) {
		if m.openWindow(display, full) {
			return nil
		}
		cmd, err := shell.Command(nil, m.cl.KubectlEnv(), full...)
		if err != nil {
			m.setError(err.Error())
			return nil
		}
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			return cmdOutputMsg{line: display, err: err, interactive: true, code: exitCode(err)}
		})
	}
	m.output = textView{title: display, loading: true}
	m.tab = tabOutput
	m.focus = fMain
	m.setStatus(i18n.T("cmd.running", display))
	env := m.cl.KubectlEnv()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd, err := shell.Command(ctx, env, full...)
		if err != nil {
			return cmdOutputMsg{line: display, err: err, code: -1}
		}
		out, err := cmd.CombinedOutput()
		return cmdOutputMsg{line: display, out: string(out), err: err, code: exitCode(err)}
	}
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

// injectFlags adds --context and -n unless the command already sets them.
func (m *Model) injectFlags(args []string) []string {
	out := []string{}
	if !shell.HasFlag(args, "--context") {
		out = append(out, "--context", m.ctxName)
	}
	verbIdx := -1
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			verbIdx = i
			break
		}
	}
	needNS := !shell.HasFlag(args, "-n", "--namespace", "-A", "--all-namespaces")
	for _, a := range args {
		if strings.HasPrefix(a, "-n") && len(a) > 2 && !strings.HasPrefix(a, "--") {
			needNS = false // -nfoo
		}
	}
	verb := ""
	if verbIdx >= 0 {
		verb = args[verbIdx]
	}
	nsFlag := []string(nil)
	if needNS && verbIdx >= 0 && !nonNamespacedVerb(verb) {
		if m.ns != "" {
			nsFlag = []string{"-n", m.ns}
		} else if verb == "get" || verb == "events" || verb == "top" {
			nsFlag = []string{"-A"}
		}
	}
	if verbIdx < 0 || nsFlag == nil {
		return append(out, args...)
	}
	out = append(out, args[:verbIdx+1]...)
	out = append(out, nsFlag...)
	return append(out, args[verbIdx+1:]...)
}

func nonNamespacedVerb(v string) bool {
	switch v {
	case "config", "version", "api-resources", "api-versions", "cluster-info", "cordon", "uncordon", "drain",
		"taint", "certificate", "kustomize", "completion", "plugin", "options":
		return true
	}
	return false
}

func (m *Model) onCmdOutput(msg cmdOutputMsg) tea.Cmd {
	if msg.interactive {
		if msg.code != 0 && msg.err != nil {
			m.setError(msg.line + " — " + i18n.T("cmd.exit", msg.code))
		}
		return tea.Batch(m.loadResources(), m.loadRelated())
	}
	m.output.title = msg.line
	m.output.set(msg.out)
	if msg.err != nil && msg.code < 0 {
		m.output.err = msg.err.Error()
	}
	if msg.code != 0 {
		m.setError(i18n.T("cmd.exit", msg.code))
	} else {
		m.setStatus("✓ " + msg.line)
	}
	return tea.Batch(m.loadResources(), m.loadRelated())
}

// view returns the suggestion popup (may be empty) and the prompt line.
func (c *cmdLine) view(m *Model, w int) (popup []string, line string) {
	s := m.st
	label := s.AccentBold.Render(" ⎈ kubectl ")
	ctxInfo := s.Dim.Render("--context " + m.ctxName)
	if m.ns != "" {
		ctxInfo += s.Dim.Render(" -n " + m.ns)
	}
	avail := w - lipWidth(label) - lipWidth(ctxInfo) - 3
	line = label + c.edit.View(s, avail)
	line = fit(line, w-lipWidth(ctxInfo)-1) + ctxInfo + " "
	if len(c.sugg) == 0 {
		return nil, line
	}
	maxRows := 8
	start := 0
	if c.suggIdx >= maxRows {
		start = c.suggIdx - maxRows + 1
	}
	hint := i18n.T("cmd.hint")
	width := lipWidth(hint) + 2
	for _, sg := range c.sugg {
		width = max(width, len([]rune(sg))+4)
	}
	width = min(width, w-14)
	bs := s.BorderFocused
	popup = append(popup, bs.Render("╭"+strings.Repeat("─", width)+"╮"))
	for i := start; i < len(c.sugg) && i < start+maxRows; i++ {
		item := " " + c.sugg[i]
		if i == c.suggIdx {
			item = s.Selected.Render(fit(item, width))
		} else {
			item = s.Text.Render(fit(item, width))
		}
		popup = append(popup, bs.Render("│")+item+bs.Render("│"))
	}
	if len(c.sugg) > start+maxRows {
		popup = append(popup, bs.Render("│")+s.Dim.Render(fit(fmt.Sprintf(" +%d", len(c.sugg)-start-maxRows), width))+bs.Render("│"))
	}
	popup = append(popup, bs.Render("├"+strings.Repeat("─", width)+"┤"))
	popup = append(popup, bs.Render("│")+s.Dim.Render(fit(" "+hint, width))+bs.Render("│"))
	popup = append(popup, bs.Render("╰"+strings.Repeat("─", width)+"╯"))
	return popup, line
}
