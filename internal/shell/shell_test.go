package shell

import (
	"reflect"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	cases := map[string][]string{
		`get pods -n kube-system`:               {"get", "pods", "-n", "kube-system"},
		`exec -it p -- sh -c 'echo "hi there"'`: {"exec", "-it", "p", "--", "sh", "-c", `echo "hi there"`},
		`get pods -l "app in (a, b)"`:           {"get", "pods", "-l", "app in (a, b)"},
		`  spaced   out  `:                      {"spaced", "out"},
		`a\ b c`:                                {"a b", "c"},
		`x ''`:                                  {"x", ""},
	}
	for in, want := range cases {
		got, err := SplitArgs(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
	if _, err := SplitArgs(`get "pods`); err == nil {
		t.Error("unterminated quote should fail")
	}
}

func TestInteractive(t *testing.T) {
	yes := [][]string{
		{"edit", "deploy/x"}, {"exec", "-it", "p", "--", "sh"}, {"logs", "-f", "p"}, {"get", "pods", "-w"},
		{"port-forward", "svc/x", "8080:80"}, {"debug", "-it", "p", "--image", "busybox"},
	}
	no := [][]string{{"get", "pods"}, {"logs", "p", "--tail", "10"}, {"describe", "pod", "x"}, {"delete", "pod", "x", "--force"}}
	for _, a := range yes {
		if !Interactive(a) {
			t.Errorf("%v should be interactive", a)
		}
	}
	for _, a := range no {
		if Interactive(a) {
			t.Errorf("%v should not be interactive", a)
		}
	}
}

func TestNoShellAndForbidden(t *testing.T) {
	if !NoShell(`OCI runtime exec failed: exec failed: unable to start container process: exec: "sh": executable file not found in $PATH: unknown`) {
		t.Error("expected no-shell detection")
	}
	if !Forbidden(`Error from server (Forbidden): pods "x" is forbidden: User "u" cannot create resource "pods/exec"`) {
		t.Error("expected forbidden detection")
	}
}
