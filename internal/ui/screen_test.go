package ui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	corev1 "k8s.io/api/core/v1"

	"github.com/gnome627/komar/internal/i18n"
	"github.com/gnome627/komar/internal/kube"
	"github.com/gnome627/komar/internal/state"
	"github.com/gnome627/komar/internal/theme"
)

// screenModel is a model filled with the kind of data that breaks layouts:
// long names, wide runes, tabs and carriage returns in logs.
func screenModel() *Model {
	pal, _ := theme.Load()
	cols := []kube.Column{{Name: "Name"}, {Name: "Ready"}, {Name: "Status"}, {Name: "Restarts"}, {Name: "Age"}}
	var rows []kube.Row
	for i, n := range []string{"api-gateway-65b59d8475-8bhbp", "web", "очень-длинное-имя-пода-с-кириллицей-7f9c", "worker-日本語-0"} {
		rows = append(rows, kube.Row{Namespace: "production-eu-west", Name: n, UID: fmt.Sprint("uid-", i),
			Cells: []string{n, "1/2", "CrashLoopBackOff", "12 (3m ago)", "41d"}})
	}
	m := &Model{
		pal: pal, st: NewStyles(pal), ready: true, hist: &state.History{}, cl: &kube.Cluster{},
		ctxName: "arn:aws:eks:eu-west-1:123456789012:cluster/production", version: "v1.31.2-eks", user: "kubernetes-admin",
		contexts: []string{"arn:aws:eks:eu-west-1:123456789012:cluster/production", "kind-dev", "minikube"},
		nsItems:  []string{"", "default", "kube-system", "production-eu-west", "monitoring", "ingress-nginx"},
		ns:       "production-eu-west",
		kinds:    append([]kube.Kind{}, kube.Builtin...),
		hidden:   map[string]time.Time{}, dying: map[string]*dyingRow{}, nameCache: map[string][]string{},
		resTable: &kube.Table{Columns: cols, Rows: rows},
		relMode:  relContainers, relPod: &corev1.Pod{},
		relContainers: []containerItem{
			{name: "istio-proxy-sidecar-container", state: "CrashLoopBackOff", restarts: 142, image: "registry.example.com/team/istio/proxyv2:1.24.0"},
			{name: "init", state: "Completed", init: true, image: "busybox"},
		},
		usage: &kube.Usage{CPUPercent: 93, MemPercent: 71}, warnings: 128,
		podUsageOf: "x", podCPU: 1250, podMem: 3 << 30,
		events: newEventsView(), logs: newLogView(100),
	}
	m.events.loaded = true
	for i := 0; i < 6; i++ {
		g := &kube.EventGroup{Type: corev1.EventTypeWarning, Reason: "FailedScheduling", Namespace: "production-eu-west", Kind: "Pod",
			Object: "api-gateway-65b59d8475-8bhbp", Count: 1234, Last: time.Now(),
			Message: "0/12 nodes are available:\t3 node(s) had untolerated taint\r\n🔥 日本語 preemption is not helpful"}
		g.Bins[kube.EventWindow-1] = 4
		m.events.groups = append(m.events.groups, g)
	}
	m.logs.sources = []logSource{{"api-gateway-65b59d8475-8bhbp", "app"}, {"api-gateway-65b59d8475-x2x2x", "istio-proxy"}}
	for i := 0; i < 40; i++ {
		text := []string{
			"2026-10-07T12:00:00Z INFO\tlistening\ton :8080 " + strings.Repeat("x", i*7),
			"progress 10%\rprogress 100%\rdone",
			"🔥 日本語のログ " + strings.Repeat("ログ", i),
			"bell\a back\bspace esc\x1b[2Jclear \x1b]0;title\x07 vt\v ff\f del\x7f c1\u0085\u009b nul\x00",
			"кириллица и emoji ⚠️ 👨‍👩‍👧 " + strings.Repeat("ы", i*5),
		}[i%5]
		m.logs.lines = append(m.logs.lines, logEntry{src: i % 2, text: clean(ansi.Strip(text))})
	}
	long := "Name:\tapi-gateway\r\nLabels:\tapp=api,\ttier=backend " + strings.Repeat("лейбл=значение,", 20) + "\n  Warning  BackOff  🔥 日本語 " + strings.Repeat("w", 200)
	m.describe.set(strings.Repeat(long+"\n", 20))
	m.yaml.set(strings.Repeat("metadata:\n  annotations:\n    note: \"\ttab 日本語 "+strings.Repeat("y", 150)+"\"\r\n", 20))
	m.output = textView{title: "kubectl get pods -A -o wide --show-labels --sort-by=.metadata.creationTimestamp"}
	m.output.set(strings.Repeat("NAMESPACE   NAME\tREADY\rSTATUS "+strings.Repeat("z", 180)+"\n", 30))
	m.rollout = rolloutView{loaded: true, status: kube.RolloutStatus{Desired: 5, Updated: 3, Ready: 2, Old: 2, Message: strings.Repeat("deadline exceeded ", 12)},
		history: []kube.Revision{{Number: 12, Current: true, Images: []string{"registry.example.com/team/api-gateway:2026.10.07-abcdef1234"}, Cause: strings.Repeat("kubectl set image ", 8)}, {Number: 11, Images: []string{"a", "b"}}}}
	return m
}

// screenCases are the states of the model worth drawing.
func screenCases() map[string]func(m *Model) {
	cases := map[string]func(m *Model){}
	for f, fn := range map[int]string{fMain: "main", fCtx: "ctx", fNs: "ns", fRes: "res", fRel: "rel"} {
		for t := 0; t < tabCount; t++ {
			cases[fmt.Sprintf("focus=%s/tab=%d", fn, t)] = func(m *Model) { m.focus, m.tab = f, t }
		}
	}
	cases["allns"] = func(m *Model) { m.ns = "" }
	cases["logs-nowrap-search"] = func(m *Model) {
		m.tab, m.focus = tabLogs, fMain
		m.logs.wrap, m.logs.follow = false, false
		m.logs.query, m.logs.re = "日本|prog", compileSearch("日本|prog")
		m.logs.rebuildMatches()
	}
	cases["logs-one-source"] = func(m *Model) { m.tab = tabLogs; m.logs.sources = m.logs.sources[:1] }
	cases["status"] = func(m *Model) { m.setError(strings.Repeat("forbidden: user cannot delete pods ", 6)) }
	cases["connecting"] = func(m *Model) { m.connecting = m.ctxName }
	cases["reserr"] = func(m *Model) {
		m.resErr = "pods is forbidden: User \"system:serviceaccount:production-eu-west:deployer\" cannot list resource"
	}
	cases["rel-pods"] = func(m *Model) { m.relMode, m.relTable = relPods, m.resTable; m.focus = fRel }
	cases["prompt"] = func(m *Model) {
		m.prompt = &prompt{label: "Поиск в логах /", edit: newLineEdit(strings.Repeat("длинный запрос ", 12))}
	}
	cases["cmdline"] = func(m *Model) {
		m.openCmdLine("get pods -n production-eu-west --field-selector=status.phase=Running ")
		m.cmd.sugg = []string{"api-gateway-65b59d8475-8bhbp", "очень-длинное-имя-пода-с-кириллицей-7f9c-" + strings.Repeat("x", 80), "c", "d", "e", "f", "g", "h", "i", "j"}
		m.cmd.suggIdx = 9
	}
	cases["confirm"] = func(m *Model) {
		m.modal = &confirmModal{title: "Удалить", question: "Удалить pod/очень-длинное-имя-пода-с-кириллицей-7f9c-" + strings.Repeat("x", 60) + "?",
			body: "production-eu-west\nэто действие нельзя отменить", danger: true}
	}
	cases["info"] = func(m *Model) {
		m.modal = &infoModal{title: "Доступ запрещён", body: strings.Repeat("pods \"x\" is forbidden: User cannot create resource pods/exec in API group. ", 12) + "\n" + strings.Repeat("y", 300)}
	}
	cases["scale"] = func(m *Model) {
		m.modal = &scaleModal{t: target{kind: kube.Builtin[1], name: "очень-длинное-имя-деплоймента-" + strings.Repeat("x", 60)}, orig: 3, value: 24}
	}
	cases["picker"] = func(m *Model) { m.modal = m.namespacePicker() }
	cases["kinds"] = func(m *Model) {
		p := &pickerModal{title: "Типы ресурсов", labels: map[string]string{}}
		for i := 0; i < 40; i++ {
			it := fmt.Sprintf("certificaterequests.cert-manager.io/v1-%d-%s", i, strings.Repeat("x", 50))
			p.items = append(p.items, it)
			p.labels[it] = "CertificateRequest (cr)"
		}
		p.refilter()
		p.cursor = 30
		m.modal = p
	}
	cases["help"] = func(m *Model) { m.modal = newHelpModal(m) }
	return cases
}

func screenLines(m *Model, w, h int) []string {
	m.w, m.h = w, h
	return strings.Split(m.View().Content, "\n")
}

// TestScreenStaysInFrame draws every state at every window size and checks
// that the result is exactly the window: nothing longer than a line, no
// extra lines, no control characters that would move the terminal cursor.
func TestScreenStaysInFrame(t *testing.T) {
	defer i18n.Set(i18n.Current())
	// Every width up to where the layout stops changing, each at one of the
	// heights, and every height at the widths around the layout switches.
	heights := []int{6, 7, 8, 9, 10, 12, 15, 20, 24, 40}
	sizes := [][2]int{}
	for w := minW; w <= 150; w += 1 + w/100*4 {
		sizes = append(sizes, [2]int{w, heights[w%len(heights)]})
	}
	for _, w := range []int{minW, 44, twoColumnW - 1, twoColumnW, 120} {
		for _, h := range heights {
			sizes = append(sizes, [2]int{w, h})
		}
	}
	bad := 0
	byCase := map[string]int{}
	for _, lang := range []i18n.Lang{i18n.EN, i18n.RU} {
		i18n.Set(lang)
		for name, setup := range screenCases() {
			for _, sz := range sizes {
				m := screenModel()
				setup(m)
				lines := screenLines(m, sz[0], sz[1])
				fail := func(format string, a ...any) {
					byCase[name]++
					if bad++; bad <= 25 {
						t.Errorf("%s %s %dx%d: %s", lang, name, sz[0], sz[1], fmt.Sprintf(format, a...))
					}
				}
				if len(lines) != sz[1] {
					fail("%d lines", len(lines))
				}
				for i, l := range lines {
					if lw := ansi.StringWidth(l); lw != sz[0] {
						fail("line %d is %d cells: %q", i, lw, ansi.Strip(l))
						break
					}
					if j := strings.IndexFunc(ansi.Strip(l), isControl); j >= 0 {
						fail("line %d has a control character: %q", i, ansi.Strip(l))
						break
					}
				}
			}
		}
	}
	if bad > 25 {
		t.Errorf("…and %d more: %v", bad-25, byCase)
	}
}

// TestScreenDump writes screens to KOMAR_DUMP for looking at them by eye:
// KOMAR_DUMP=/tmp/x KOMAR_SIZES=60x16,40x12 go test -run ScreenDump ./internal/ui
func TestScreenDump(t *testing.T) {
	dir := os.Getenv("KOMAR_DUMP")
	if dir == "" {
		t.Skip("KOMAR_DUMP is not set")
	}
	i18n.Set(i18n.RU)
	for _, sz := range strings.Split(os.Getenv("KOMAR_SIZES"), ",") {
		var w, h int
		if _, err := fmt.Sscanf(sz, "%dx%d", &w, &h); err != nil {
			t.Fatalf("size %q: %v", sz, err)
		}
		var b strings.Builder
		for name, setup := range screenCases() {
			m := screenModel()
			setup(m)
			fmt.Fprintf(&b, "=== %s %s\n", sz, name)
			for _, l := range screenLines(m, w, h) {
				b.WriteString(ansi.Strip(l) + "|\n")
			}
		}
		if err := os.WriteFile(fmt.Sprintf("%s/%s.txt", dir, sz), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) }

func click(m *Model, x, y int) { m.handleClick(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}) }

// find returns the cell where text starts on the drawn screen.
func find(t *testing.T, m *Model, text string) (int, int) {
	t.Helper()
	for y, l := range screenLines(m, m.w, m.h) {
		if i := strings.Index(ansi.Strip(l), text); i >= 0 {
			return ansi.StringWidth(ansi.Strip(l)[:i]), y
		}
	}
	t.Fatalf("%q is not on the screen", text)
	return 0, 0
}

func TestMouse(t *testing.T) {
	defer i18n.Set(i18n.Current())
	i18n.Set(i18n.EN)
	m := screenModel()
	m.focus, m.w, m.h = fCtx, 120, 30

	x, y := find(t, m, "web")
	click(m, x, y)
	if got, _ := m.selectedRes(); m.focus != fRes || got.Name != "web" {
		t.Errorf("click on a row: focus=%d, row %q; want the resources panel on web", m.focus, got.Name)
	}
	m.tab = tabYAML
	m.handleWheel(tea.MouseWheelMsg{X: m.w - 5, Y: 10, Button: tea.MouseWheelDown})
	if m.focus != fRes || m.yaml.top == 0 {
		t.Errorf("wheel over the main panel: focus=%d top=%d", m.focus, m.yaml.top)
	}
	x, y = find(t, m, "Events")
	click(m, x, y)
	if m.focus != fMain || m.tab != tabEvents {
		t.Errorf("click on a tab: focus=%d tab=%d", m.focus, m.tab)
	}
	x, y = find(t, m, "kube-system")
	click(m, x, y)
	if m.focus != fNs || m.nsItems[m.nsList.cursor] != "kube-system" {
		t.Errorf("click on a namespace: focus=%d cursor=%d", m.focus, m.nsList.cursor)
	}

	// The top bar opens the pickers; a click on an item picks it and one
	// outside closes the modal.
	x, y = find(t, m, "◆ production")
	click(m, x, y)
	if _, ok := m.modal.(*pickerModal); !ok {
		t.Fatalf("click on the namespace in the top bar: modal is %T", m.modal)
	}
	click(m, 0, m.h-1)
	if m.modal != nil {
		t.Error("click outside did not close the modal")
	}
	picked := ""
	m.modal = &pickerModal{items: []string{"a", "b", "c"}, filtered: []string{"a", "b", "c"},
		onPick: func(m *Model, v string) tea.Cmd { picked = v; return nil }}
	screenLines(m, m.w, m.h)
	w, h := lipgloss.Size(m.modal.view(m))
	click(m, (m.w-w)/2+4, (m.h-h)/2+5)
	if picked != "b" || m.modal != nil {
		t.Errorf("click on the second item picked %q", picked)
	}

	// A narrow window has one column: the tab arrows still work.
	m.w, m.focus, m.tab = 50, fMain, tabLogs
	x, y = find(t, m, "› 1/6")
	click(m, x, y)
	if m.tab != tabDescribe {
		t.Errorf("click on › in a narrow window: tab=%d", m.tab)
	}
}
