package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/gnome627/komar/internal/kube"
	"github.com/gnome627/komar/internal/state"
)

func TestInjectFlags(t *testing.T) {
	m := &Model{ctxName: "prod", ns: "api"}
	cases := []struct {
		in, want []string
	}{
		{[]string{"get", "pods"}, []string{"--context", "prod", "get", "-n", "api", "pods"}},
		{[]string{"get", "pods", "-n", "kube-system"}, []string{"--context", "prod", "get", "pods", "-n", "kube-system"}},
		{[]string{"--context", "dev", "get", "pods", "-A"}, []string{"--context", "dev", "get", "pods", "-A"}},
		{[]string{"exec", "-it", "p", "--", "sh"}, []string{"--context", "prod", "exec", "-n", "api", "-it", "p", "--", "sh"}},
		{[]string{"version"}, []string{"--context", "prod", "version"}},
	}
	for _, c := range cases {
		if got := m.injectFlags(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: got %v, want %v", c.in, got, c.want)
		}
	}
	m.ns = ""
	if got := m.injectFlags([]string{"get", "pods"}); !reflect.DeepEqual(got, []string{"--context", "prod", "get", "-A", "pods"}) {
		t.Errorf("all namespaces: %v", got)
	}
}

func TestCompletions(t *testing.T) {
	m := &Model{ctxName: "c", contexts: []string{"c", "d"}, nsItems: []string{"", "api", "default"}, nameCache: map[string][]string{}}
	if got := filterPrefix(m.completions(nil, "ro"), "ro"); len(got) == 0 || got[0] != "rollout" {
		t.Errorf("verbs: %v", got)
	}
	if got := filterPrefix(m.completions([]string{"get", "pods", "-n"}, ""), ""); !reflect.DeepEqual(got, []string{"api", "default"}) {
		t.Errorf("namespaces: %v", got)
	}
	if got := m.completions([]string{"rollout"}, ""); !reflect.DeepEqual(got, rolloutSub) {
		t.Errorf("rollout sub: %v", got)
	}
	got := filterPrefix(m.completions([]string{"get"}, "dep"), "dep")
	if len(got) == 0 || !strings.HasPrefix(got[0], "dep") {
		t.Errorf("kinds: %v", got)
	}
}

func TestMidTrunc(t *testing.T) {
	if got := midTrunc("api-gateway-65b59d8475-8bhbp", 20); len([]rune(got)) != 20 || !strings.HasSuffix(got, "8bhbp") {
		t.Errorf("midTrunc = %q", got)
	}
	if got := midTrunc("short", 8); got != "short   " {
		t.Errorf("padding: %q", got)
	}
}

func TestListClamp(t *testing.T) {
	l := listState{cursor: 50}
	l.clamp(10, 4)
	if l.cursor != 9 || l.offset != 6 {
		t.Errorf("clamp: %+v", l)
	}
	l.cursor = 0
	l.clamp(10, 4)
	if l.offset != 0 {
		t.Errorf("scroll up: %+v", l)
	}
}

func TestRelMode(t *testing.T) {
	cl := &kube.Cluster{}
	for res, want := range map[string]relMode{"pods": relContainers, "deployments": relPods, "cronjobs": relJobs, "configmaps": relNone, "nodes": relPods} {
		k, _ := cl.KindFor(res)
		if got := relModeFor(k); got != want {
			t.Errorf("%s: %v, want %v", res, got, want)
		}
	}
}

func TestViewRoundTrip(t *testing.T) {
	rows := []kube.Row{{Namespace: "api", Name: "web"}, {Namespace: "api", Name: "worker"}}
	m := &Model{
		ctxName: "prod", ns: "api", kinds: append([]kube.Kind{}, kube.Builtin...),
		hidden:   map[string]time.Time{},
		resTable: &kube.Table{Rows: rows},
		resList:  listState{cursor: 1, filter: "w"},
		tab:      tabOutput, focus: fMain, lastFocus: fRel,
	}
	v := m.snapshot()
	if v.Resource != "api/worker" || v.Tab != tabLogs || v.Context != "prod" || v.Namespace != "api" {
		t.Fatalf("snapshot: %+v", v)
	}
	v.Tab = tabYAML
	n := &Model{}
	n.applyView(v)
	if n.tab != tabYAML || n.focus != fMain || n.lastFocus != fRel || n.pendingSelect != "api/worker" || n.resList.filter != "w" {
		t.Errorf("applyView: tab=%d focus=%d last=%d select=%q filter=%q", n.tab, n.focus, n.lastFocus, n.pendingSelect, n.resList.filter)
	}
	// Values from a damaged or older session file must not break the UI.
	n = &Model{focus: fRes, lastFocus: fRes}
	n.applyView(state.View{Tab: 99, Focus: -1, LastFocus: 0})
	if n.tab != 0 || n.focus != fRes || n.lastFocus != fRes {
		t.Errorf("out of range view applied: tab=%d focus=%d last=%d", n.tab, n.focus, n.lastFocus)
	}
}

func podRows(names ...string) []kube.Row {
	rows := make([]kube.Row, len(names))
	for i, n := range names {
		rows[i] = kube.Row{Namespace: "api", Name: n, UID: "uid-" + n, Failed: strings.HasPrefix(n, "evicted")}
	}
	return rows
}

func TestFailedPodsDeleteWithoutAsking(t *testing.T) {
	m := &Model{
		cl: &kube.Cluster{}, kinds: append([]kube.Kind{}, kube.Builtin...),
		hidden: map[string]time.Time{}, dying: map[string]*dyingRow{},
		resTable: &kube.Table{Rows: podRows("evicted-a", "web")},
	}
	if cmd := m.requestDelete(m.resTarget(), false); cmd == nil {
		t.Fatal("a failed pod should be deleted right away")
	}
	if m.modal != nil {
		t.Error("a failed pod should not ask for confirmation")
	}
	if _, gone := m.hidden["uid-evicted-a"]; !gone {
		t.Error("the deleted pod should leave the list")
	}
	// A healthy pod still goes through the access check and the question.
	if got, _ := m.selectedRes(); got.Name != "web" {
		t.Fatalf("cursor on %q, want web", got.Name)
	}
	before := len(m.hidden)
	m.requestDelete(m.resTarget(), false)
	if len(m.hidden) != before {
		t.Error("a healthy pod was deleted without the check")
	}
}

func TestDyingRows(t *testing.T) {
	m := &Model{
		kinds:  append([]kube.Kind{}, kube.Builtin...),
		hidden: map[string]time.Time{}, dying: map[string]*dyingRow{},
		resTable: &kube.Table{Rows: podRows("evicted-a", "evicted-b", "web", "worker")},
		resList:  listState{cursor: 2},
	}
	m.dying["uid-evicted-a"] = &dyingRow{panel: fRes}
	m.dying["uid-evicted-b"] = &dyingRow{panel: fRes}
	if m.requestDelete(target{kind: m.kind(), name: "evicted-a", uid: "uid-evicted-a"}, false) != nil {
		t.Error("deleting a row that is already going should do nothing")
	}

	// The server has already dropped both pods; they stay until buried.
	fresh := &kube.Table{Rows: podRows("web", "worker")}
	m.keepDying(m.resTable, fresh, fRes)
	m.resTable = fresh
	if got := len(m.resRows()); got != 4 {
		t.Fatalf("%d rows after refresh, want the 2 dying ones kept (4)", got)
	}
	if got, _ := m.selectedRes(); got.Name != "web" {
		t.Fatalf("cursor on %q after refresh, want web", got.Name)
	}

	m.bury("uid-evicted-a")
	m.bury("uid-evicted-b")
	if got, _ := m.selectedRes(); got.Name != "web" || len(m.resRows()) != 2 {
		t.Errorf("after burying: cursor on %q, %d rows; want web, 2", got.Name, len(m.resRows()))
	}

	// Burying the row under the cursor at the end of the list.
	m.resList.cursor = 1
	m.dying["uid-worker"] = &dyingRow{panel: fRes}
	m.bury("uid-worker")
	if got, ok := m.selectedRes(); !ok || got.Name != "web" {
		t.Errorf("cursor fell off the list: %q %v", got.Name, ok)
	}
}

func TestHotkeysIgnoreLayout(t *testing.T) {
	if a, b := len([]rune(cyrKeys)), len([]rune(latKeys)); a != b {
		t.Fatalf("layout tables differ in length: %d and %d", a, b)
	}
	press := func(text string, mod tea.KeyMod) tea.KeyPressMsg {
		r := []rune(text)[0]
		if mod != 0 {
			return tea.KeyPressMsg{Code: r, Mod: mod}
		}
		return tea.KeyPressMsg{Code: r, Text: text}
	}
	cases := []struct {
		k    tea.KeyPressMsg
		want string
	}{
		{press("в", 0), "d"}, {press("В", 0), "D"}, {press("й", 0), "q"}, {press("д", 0), "l"},
		{press("х", 0), "["}, {press("ъ", 0), "]"}, {press("Ж", 0), ":"}, {press(".", 0), "/"}, {press(",", 0), "?"},
		{press("ц", tea.ModCtrl), "ctrl+w"}, {press("к", tea.ModCtrl), "ctrl+r"},
		{press("d", 0), "d"}, {press("5", 0), "5"}, {press("+", 0), "+"},
		{tea.KeyPressMsg{Code: tea.KeyEnter}, "enter"}, {tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl}, "ctrl+enter"},
	}
	for _, c := range cases {
		if got := hotkey(c.k); got != c.want {
			t.Errorf("%q: got %q, want %q", c.k.String(), got, c.want)
		}
	}
	// Typing Russian into an input still gives Russian.
	e := newLineEdit("")
	e.Handle(press("в", 0))
	e.Handle(press(".", 0))
	if e.String() != "в." {
		t.Errorf("typed %q, want %q", e.String(), "в.")
	}
}
