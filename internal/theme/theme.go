// Package theme loads colors from the active Omarchy theme so komar always
// matches the desktop. Outside Omarchy it falls back to the terminal's own
// ANSI palette, which follows whatever theme the terminal uses.
package theme

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/BurntSushi/toml"
)

// RGB is a plain 24-bit color used where we need math (gradients, fades).
type RGB struct{ R, G, B uint8 }

func (c RGB) Color() color.Color { return color.RGBA{R: c.R, G: c.G, B: c.B, A: 0xff} }

func (c RGB) Hex() string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// Lerp blends a toward b by t in [0,1].
func Lerp(a, b RGB, t float64) RGB {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	l := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return RGB{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B)}
}

// Gradient samples a multi-stop gradient at t in [0,1].
func Gradient(t float64, stops ...RGB) RGB {
	if len(stops) == 0 {
		return RGB{}
	}
	if len(stops) == 1 || t <= 0 {
		return stops[0]
	}
	if t >= 1 {
		return stops[len(stops)-1]
	}
	seg := t * float64(len(stops)-1)
	i := int(seg)
	return Lerp(stops[i], stops[i+1], seg-float64(i))
}

func ParseHex(s string) (RGB, bool) {
	s = strings.TrimSpace(strings.Trim(s, `"'`))
	s = strings.TrimPrefix(s, "#")
	s = strings.TrimPrefix(s, "0x")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return RGB{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return RGB{}, false
	}
	return RGB{uint8(v >> 16), uint8(v >> 8), uint8(v)}, true
}

// Palette is what the UI draws with.
type Palette struct {
	Name string
	// FromTheme is true when colors came from an Omarchy theme file.
	FromTheme bool
	Dark      bool

	// UI colors. With FromTheme=false these are ANSI colors.
	Fg, Dim, Muted, Bright, Bg, Panel, Selection, Accent color.Color
	Red, Green, Yellow, Orange, Blue, Magenta, Cyan      color.Color

	// RGB versions for effects; always set (defaults when no theme).
	RGB RGBSet
}

type RGBSet struct {
	Bg, Fg, Dim, Accent, Selection                  RGB
	Red, Green, Yellow, Orange, Blue, Magenta, Cyan RGB
	BrightYellow, BrightRed                         RGB
}

// Tokyo Night, the Omarchy default, used for effects when no theme is found.
var defaultRGB = RGBSet{
	Bg: RGB{0x1a, 0x1b, 0x26}, Fg: RGB{0xa9, 0xb1, 0xd6}, Dim: RGB{0x56, 0x5f, 0x89},
	Accent: RGB{0x7a, 0xa2, 0xf7}, Selection: RGB{0x29, 0x2e, 0x42},
	Red: RGB{0xf7, 0x76, 0x8e}, Green: RGB{0x9e, 0xce, 0x6a}, Yellow: RGB{0xe0, 0xaf, 0x68},
	Orange: RGB{0xff, 0x9e, 0x64}, Blue: RGB{0x7a, 0xa2, 0xf7}, Magenta: RGB{0xbb, 0x9a, 0xf7},
	Cyan: RGB{0x7d, 0xcf, 0xff}, BrightYellow: RGB{0xff, 0xe0, 0x8a}, BrightRed: RGB{0xff, 0x7a, 0x93},
}

// Source describes where the palette came from so we can notice changes.
type Source struct {
	Path    string
	ModTime time.Time
}

// ThemeDirs lists the places an Omarchy install keeps the current theme:
// 4.x uses ~/.local/state, 3.x uses ~/.config.
func ThemeDirs() []string {
	home, _ := os.UserHomeDir()
	var dirs []string
	if d := os.Getenv("KOMAR_THEME_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	return append(dirs,
		filepath.Join(home, ".local/state/omarchy/current/theme"),
		filepath.Join(home, ".config/omarchy/current/theme"),
	)
}

// CurrentSource finds the theme file in use, if any.
func CurrentSource() Source {
	for _, dir := range ThemeDirs() {
		for _, f := range []string{"colors.toml", "alacritty.toml"} {
			p := filepath.Join(dir, f)
			// The theme dir is usually a symlink that gets swapped on theme
			// change, so stat through it.
			if st, err := os.Stat(p); err == nil {
				real, _ := filepath.EvalSymlinks(p)
				if real == "" {
					real = p
				}
				return Source{Path: real, ModTime: st.ModTime()}
			}
		}
	}
	return Source{}
}

// Load builds the palette from the current Omarchy theme or ANSI fallback.
func Load() (Palette, Source) {
	src := CurrentSource()
	if src.Path != "" {
		var set map[string]RGB
		var err error
		if strings.HasSuffix(src.Path, "colors.toml") {
			set, err = parseColorsToml(src.Path)
		} else {
			set, err = parseAlacritty(src.Path)
		}
		if err == nil && len(set) > 0 {
			return fromMap(set, themeName(src.Path)), src
		}
	}
	return ansiPalette(), src
}

func themeName(path string) string {
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		filepath.Join(home, ".local/state/omarchy/current/theme.name"),
		filepath.Join(home, ".config/omarchy/current/theme.name"),
	} {
		if b, err := os.ReadFile(p); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return filepath.Base(filepath.Dir(path))
}

func parseColorsToml(path string) (map[string]RGB, error) {
	var raw map[string]any
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return nil, err
	}
	out := map[string]RGB{}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			if c, ok := ParseHex(s); ok {
				out[k] = c
			}
		}
	}
	if m, ok := raw["mode"].(string); ok && m == "light" {
		out["__light"] = RGB{}
	}
	return out, nil
}

func parseAlacritty(path string) (map[string]RGB, error) {
	var raw struct {
		Colors struct {
			Primary   map[string]string `toml:"primary"`
			Normal    map[string]string `toml:"normal"`
			Bright    map[string]string `toml:"bright"`
			Selection map[string]string `toml:"selection"`
			Cursor    map[string]string `toml:"cursor"`
		} `toml:"colors"`
	}
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return nil, err
	}
	out := map[string]RGB{}
	put := func(dst, src string) {
		if c, ok := ParseHex(src); ok {
			out[dst] = c
		}
	}
	put("background", raw.Colors.Primary["background"])
	put("foreground", raw.Colors.Primary["foreground"])
	put("selection", raw.Colors.Selection["background"])
	for _, n := range []string{"red", "green", "yellow", "blue", "magenta", "cyan"} {
		put(n, raw.Colors.Normal[n])
		put("bright_"+n, raw.Colors.Bright[n])
	}
	put("muted", raw.Colors.Bright["black"])
	put("dark_foreground", raw.Colors.Bright["black"])
	put("accent", raw.Colors.Cursor["cursor"])
	return out, nil
}

func fromMap(m map[string]RGB, name string) Palette {
	set := defaultRGB
	pick := func(dst *RGB, keys ...string) {
		for _, k := range keys {
			if c, ok := m[k]; ok {
				*dst = c
				return
			}
		}
	}
	pick(&set.Bg, "background")
	pick(&set.Fg, "foreground")
	pick(&set.Dim, "dark_foreground", "muted")
	pick(&set.Selection, "selection", "lighter_background")
	pick(&set.Red, "red")
	pick(&set.Green, "green")
	pick(&set.Yellow, "yellow")
	pick(&set.Blue, "blue")
	pick(&set.Magenta, "magenta")
	pick(&set.Cyan, "cyan")
	set.Orange = set.Yellow
	pick(&set.Orange, "orange", "bright_yellow")
	set.BrightYellow = Lerp(set.Yellow, RGB{255, 255, 255}, 0.35)
	pick(&set.BrightRed, "bright_red", "red")
	set.Accent = set.Blue
	pick(&set.Accent, "accent")

	_, light := m["__light"]
	muted := set.Dim
	pick(&muted, "muted")
	bright := Lerp(set.Fg, RGB{255, 255, 255}, 0.3)
	if light {
		bright = Lerp(set.Fg, RGB{0, 0, 0}, 0.3)
	}
	pick(&bright, "bright_foreground")
	panel := set.Bg
	pick(&panel, "lighter_background")

	return Palette{
		Name: name, FromTheme: true, Dark: !light,
		Fg: set.Fg.Color(), Dim: set.Dim.Color(), Muted: muted.Color(), Bright: bright.Color(),
		Bg: set.Bg.Color(), Panel: panel.Color(), Selection: set.Selection.Color(), Accent: set.Accent.Color(),
		Red: set.Red.Color(), Green: set.Green.Color(), Yellow: set.Yellow.Color(), Orange: set.Orange.Color(),
		Blue: set.Blue.Color(), Magenta: set.Magenta.Color(), Cyan: set.Cyan.Color(),
		RGB: set,
	}
}

func ansiPalette() Palette {
	c := lipgloss.Color
	return Palette{
		Name: "terminal", Dark: true,
		Fg: lipgloss.NoColor{}, Dim: c("8"), Muted: c("8"), Bright: c("15"),
		Bg: lipgloss.NoColor{}, Panel: lipgloss.NoColor{}, Selection: c("0"), Accent: c("4"),
		Red: c("1"), Green: c("2"), Yellow: c("3"), Orange: c("11"), Blue: c("4"), Magenta: c("5"), Cyan: c("6"),
		RGB: defaultRGB,
	}
}
