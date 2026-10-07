// Package state keeps what komar remembers between runs (command history,
// last namespace per context, the last view) and the optional user config.
package state

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const maxHistory = 500

func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "komar")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "komar")
}

func configDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "komar")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "komar")
}

// Config is ~/.config/komar/config.toml.
type Config struct {
	// Effects to pick from when deleting: burn, crumble, matrix, explode,
	// decrypt, dust, beam. Empty means all of them.
	Effects []string `toml:"effects"`
	// DisableEffects turns delete animations off.
	DisableEffects bool `toml:"disable_effects"`
	// Splash shows the animated logo on start.
	Splash *bool `toml:"splash"`
	// DebugImage is used by `kubectl debug`.
	DebugImage string `toml:"debug_image"`
	// LogTail is how many lines per container to fetch initially.
	LogTail int64 `toml:"log_tail"`
	// Language forces "en" or "ru".
	Language string `toml:"language"`
}

func LoadConfig() Config {
	c := Config{DebugImage: "busybox:latest", LogTail: 500}
	_, _ = toml.DecodeFile(filepath.Join(configDir(), "config.toml"), &c)
	if c.DebugImage == "" {
		c.DebugImage = "busybox:latest"
	}
	if c.LogTail <= 0 {
		c.LogTail = 500
	}
	return c
}

func (c Config) SplashEnabled() bool { return c.Splash == nil || *c.Splash }

// History is the kubectl command line history.
type History struct {
	Items []string
	path  string
}

func LoadHistory() *History {
	h := &History{path: filepath.Join(stateDir(), "history")}
	f, err := os.Open(h.path)
	if err != nil {
		return h
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			h.Items = append(h.Items, line)
		}
	}
	return h
}

// Add appends a command (moving duplicates to the end) and saves.
func (h *History) Add(cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}
	out := h.Items[:0]
	for _, it := range h.Items {
		if it != cmd {
			out = append(out, it)
		}
	}
	h.Items = append(out, cmd)
	if len(h.Items) > maxHistory {
		h.Items = h.Items[len(h.Items)-maxHistory:]
	}
	_ = os.MkdirAll(filepath.Dir(h.path), 0o700)
	_ = os.WriteFile(h.path, []byte(strings.Join(h.Items, "\n")+"\n"), 0o600)
}

// Session remembers per-context choices.
type Session struct {
	Namespaces map[string]string `json:"namespaces"`
	Context    string            `json:"context"`
	Kinds      map[string]string `json:"kinds"`
	Last       *View             `json:"last,omitempty"`
}

// View is what was on screen when komar was last open, so the next start
// can pick up where it left off.
type View struct {
	Context   string `json:"context"`
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Resource  string `json:"resource,omitempty"`
	Related   string `json:"related,omitempty"`
	Filter    string `json:"filter,omitempty"`
	Tab       int    `json:"tab"`
	Focus     int    `json:"focus"`
	LastFocus int    `json:"last_focus"`
}

func sessionPath() string { return filepath.Join(stateDir(), "session.json") }

func LoadSession() *Session {
	s := &Session{Namespaces: map[string]string{}, Kinds: map[string]string{}}
	if b, err := os.ReadFile(sessionPath()); err == nil {
		_ = json.Unmarshal(b, s)
	}
	if s.Namespaces == nil {
		s.Namespaces = map[string]string{}
	}
	if s.Kinds == nil {
		s.Kinds = map[string]string{}
	}
	return s
}

func (s *Session) Save() {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(stateDir(), 0o700)
	_ = os.WriteFile(sessionPath(), b, 0o600)
}
