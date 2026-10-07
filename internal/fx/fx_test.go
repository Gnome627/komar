package fx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/gnome627/komar/internal/theme"
)

const sample = `╭─ [3] Pods ──────────────────────────────╮
│   NAME                    STATUS    AGE  │
│ ● api-gateway-5jsgx       Running   4m   │
│ ● api-gateway-9ch28       Running   4m   │
│ ● api-gateway-cm22n       Running   4m   │
│ ● api-gateway-g7jkv       Running   4m   │
│                                          │
│                                          │
│                                          │
│                                          │
╰──────────────────────────────────────────╯`

func TestEffectsRun(t *testing.T) {
	lines := strings.Split(sample, "\n")
	w, h := ansi.StringWidth(lines[0]), len(lines)
	dump := os.Getenv("FX_DUMP")
	for _, name := range Names {
		a := New(name, sample, w, h, []Rect{{X: 1, Y: 3, W: w - 2, H: 1}}, Rect{X: 1, Y: 1, W: w - 2, H: h - 2}, theme.RGBSet{
			Fg: theme.RGB{R: 200, G: 200, B: 200}, Bg: theme.RGB{R: 20, G: 20, B: 30}, Accent: theme.RGB{R: 120, G: 160, B: 250},
			Red: theme.RGB{R: 250, G: 100, B: 120}, Orange: theme.RGB{R: 250, G: 160, B: 100}, Yellow: theme.RGB{R: 230, G: 180, B: 100},
			BrightYellow: theme.RGB{R: 255, G: 230, B: 140}, Green: theme.RGB{R: 160, G: 210, B: 100}, Cyan: theme.RGB{R: 120, G: 200, B: 250},
			Magenta: theme.RGB{R: 190, G: 150, B: 250}, Dim: theme.RGB{R: 90, G: 95, B: 130},
		})
		if len(a.glyphs) == 0 {
			t.Fatalf("%s: no glyphs captured", name)
		}
		frames := 0
		var out []string
		for !a.Done() && frames < 400 {
			a.Step(1.0 / 40)
			frames++
			r := a.Render()
			if got := len(strings.Split(r, "\n")); got != h {
				t.Fatalf("%s: frame has %d lines, want %d", name, got, h)
			}
			for i, l := range strings.Split(r, "\n") {
				if lw := ansi.StringWidth(l); lw != w {
					t.Fatalf("%s frame %d line %d: width %d, want %d", name, frames, i, lw, w)
				}
			}
			if frames%10 == 0 {
				out = append(out, r)
			}
		}
		if frames >= 400 {
			t.Fatalf("%s never finished", name)
		}
		if dump != "" {
			_ = os.WriteFile(filepath.Join(dump, name+".ansi"), []byte(strings.Join(out, "\n\n")), 0o644)
		}
	}
}

func TestPickNeverRepeats(t *testing.T) {
	prev := ""
	for i := 0; i < 200; i++ {
		n := Pick(nil)
		if n == prev {
			t.Fatalf("picked %s twice in a row", n)
		}
		prev = n
	}
}

func TestSplash(t *testing.T) {
	s := NewSplash(100, 30, "kubernetes · omarchy", theme.RGBSet{})
	for !s.Done() {
		s.Step(1.0 / 40)
		if r := s.Render(); len(strings.Split(r, "\n")) != 30 {
			t.Fatal("splash frame height")
		}
	}
}

// Effects composed over a live block must leave the other rows readable
// and follow their own row when it moves.
func TestComposeKeepsOtherRows(t *testing.T) {
	lines := strings.Split(sample, "\n")
	w, h := ansi.StringWidth(lines[0]), len(lines)
	plain := strings.Split(ansi.Strip(Compose(sample, w, h, nil)), "\n")
	for _, name := range Names {
		a := New(name, sample, w, h, []Rect{{X: 1, Y: 3, W: w - 2, H: 1}}, Rect{X: 1, Y: 1, W: w - 2, H: h - 2}, theme.RGBSet{})
		for !a.Done() {
			a.Step(1.0 / 40)
			out := strings.Split(ansi.Strip(Compose(sample, w, h, []Layer{{Anim: a, DY: 1}})), "\n")
			for _, y := range []int{0, 1, 2, 3, 5, h - 1} {
				if out[y] != plain[y] {
					t.Fatalf("%s at %.2fs: line %d was painted over:\n%s", name, a.t, y, out[y])
				}
			}
			if strings.Contains(out[4], "api-gateway-cm22n") && a.t > 1.5 {
				t.Fatalf("%s at %.2fs: the moved target row is still there", name, a.t)
			}
		}
	}
}
