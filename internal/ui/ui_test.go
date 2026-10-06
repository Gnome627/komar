package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gnome627/komar/internal/kube"
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
