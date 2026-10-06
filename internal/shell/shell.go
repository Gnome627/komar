// Package shell runs kubectl: interactive sessions (exec, debug, edit) in
// this terminal or in a new terminal window, and plain commands whose
// output komar shows in a panel.
package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// ShellProbe picks the best shell available in the container.
const ShellProbe = `if command -v bash >/dev/null 2>&1; then exec bash; elif command -v ash >/dev/null 2>&1; then exec ash; else exec sh; fi`

// Kubectl returns the kubectl binary or an error explaining it's missing.
func Kubectl() (string, error) {
	if p := os.Getenv("KOMAR_KUBECTL"); p != "" {
		return p, nil
	}
	p, err := exec.LookPath("kubectl")
	if err == nil {
		return p, nil
	}
	// The installer may have put kubectl next to komar.
	if self, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(self), "kubectl")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("kubectl not found in PATH")
}

// ExecArgs builds `kubectl exec -it` for a pod container.
func ExecArgs(context, ns, pod, container string) []string {
	args := []string{"--context", context, "-n", ns, "exec", "-it", pod}
	if container != "" {
		args = append(args, "-c", container)
	}
	return append(args, "--", "sh", "-c", ShellProbe)
}

// DebugArgs builds `kubectl debug` with an ephemeral container that shares
// the target container's process namespace.
func DebugArgs(context, ns, pod, container, image string) []string {
	args := []string{"--context", context, "-n", ns, "debug", "-it", pod, "--image", image, "--profile", "general"}
	if container != "" {
		args = append(args, "--target", container)
	}
	return args
}

// NoShell reports whether kubectl's stderr says the container has no shell.
func NoShell(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "executable file not found") ||
		strings.Contains(s, "no such file or directory") && strings.Contains(s, "exec")
}

// Forbidden reports whether kubectl's stderr is an RBAC denial.
func Forbidden(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), "forbidden")
}

// Command builds an exec.Cmd for kubectl.
func Command(ctx context.Context, env []string, args ...string) (*exec.Cmd, error) {
	k, err := Kubectl()
	if err != nil {
		return nil, err
	}
	var cmd *exec.Cmd
	if ctx != nil {
		cmd = exec.CommandContext(ctx, k, args...)
	} else {
		cmd = exec.Command(k, args...)
	}
	cmd.Env = env
	return cmd, nil
}

// Interactive reports whether a kubectl command line needs the terminal
// (an editor, a TTY, or a stream that only ends on ctrl+c).
func Interactive(args []string) bool {
	if len(args) == 0 {
		return false
	}
	verb := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			verb = a
			break
		}
	}
	switch verb {
	case "edit", "attach", "port-forward", "proxy", "debug":
		return true
	}
	for _, a := range args {
		switch {
		case a == "-it" || a == "-ti" || a == "-i" || a == "-t" || a == "--tty" || a == "--stdin":
			return true
		case a == "-f" && verb == "logs", a == "--follow" || strings.HasPrefix(a, "--follow="):
			return true
		case a == "-w" || a == "--watch" || a == "--watch-only":
			return true
		}
	}
	return false
}

// HasFlag reports whether args already set one of the flags.
func HasFlag(args []string, names ...string) bool {
	for _, a := range args {
		for _, n := range names {
			if a == n || strings.HasPrefix(a, n+"=") {
				return true
			}
		}
	}
	return false
}

// Terminal is a way to open a new terminal window running a command.
type Terminal struct {
	Name string
	argv func(cmd []string) []string
}

func prefix(p ...string) func([]string) []string {
	return func(cmd []string) []string { return append(append([]string{}, p...), cmd...) }
}

// FindTerminal picks how to open a new window: KOMAR_TERMINAL, then
// xdg-terminal-exec (what Omarchy uses), then $TERMINAL, then well-known
// emulators.
func FindTerminal() (Terminal, error) {
	if t := os.Getenv("KOMAR_TERMINAL"); t != "" {
		parts := strings.Fields(t)
		return Terminal{Name: parts[0], argv: prefix(parts...)}, nil
	}
	if _, err := exec.LookPath("xdg-terminal-exec"); err == nil {
		p := []string{"xdg-terminal-exec"}
		// Omarchy floats windows with this app id.
		if _, err := exec.LookPath("omarchy-launch-tui"); err == nil {
			p = append(p, "--app-id=TUI.float")
		}
		return Terminal{Name: "xdg-terminal-exec", argv: prefix(append(p, "-e")...)}, nil
	}
	known := map[string][]string{
		"foot":                {"foot"},
		"alacritty":           {"alacritty", "-e"},
		"ghostty":             {"ghostty", "-e"},
		"kitty":               {"kitty"},
		"wezterm":             {"wezterm", "start", "--"},
		"gnome-terminal":      {"gnome-terminal", "--"},
		"kgx":                 {"kgx", "--"},
		"ptyxis":              {"ptyxis", "--"},
		"konsole":             {"konsole", "-e"},
		"xfce4-terminal":      {"xfce4-terminal", "-x"},
		"tilix":               {"tilix", "-e"},
		"x-terminal-emulator": {"x-terminal-emulator", "-e"},
		"xterm":               {"xterm", "-e"},
	}
	if t := os.Getenv("TERMINAL"); t != "" {
		base := filepath.Base(t)
		if p, ok := known[base]; ok {
			return Terminal{Name: base, argv: prefix(append([]string{t}, p[1:]...)...)}, nil
		}
		return Terminal{Name: base, argv: prefix(t, "-e")}, nil
	}
	for _, name := range []string{"foot", "alacritty", "ghostty", "kitty", "wezterm", "ptyxis", "kgx", "gnome-terminal", "konsole", "xfce4-terminal", "tilix", "x-terminal-emulator", "xterm"} {
		if _, err := exec.LookPath(name); err == nil {
			return Terminal{Name: name, argv: prefix(known[name]...)}, nil
		}
	}
	return Terminal{}, errors.New("no terminal emulator found")
}

// Launch starts cmd in a new terminal window, detached from komar. When the
// command fails the window waits for enter so the error stays readable.
func (t Terminal) Launch(env []string, cmd []string) error {
	quoted := make([]string, len(cmd))
	for i, c := range cmd {
		quoted[i] = shellQuote(c)
	}
	script := fmt.Sprintf(`%s; rc=$?; if [ $rc -ne 0 ]; then printf '\n[exit %%s] press enter…' "$rc"; read _; fi`, strings.Join(quoted, " "))
	argv := t.argv([]string{"sh", "-c", script})
	c := exec.Command(argv[0], argv[1:]...)
	c.Env = env
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:,@%+", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// SplitArgs splits a command line like a shell would (quotes, escapes),
// without expanding anything.
func SplitArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inArg := false
	var quote rune
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
			inArg = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inArg = true
		case r == ' ' || r == '\t':
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}
