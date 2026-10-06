package fx

import (
	"math"
	"math/rand"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/gnome627/komar/internal/theme"
)

// Logo is "komar" in the same figlet face as the Omarchy logo.
var Logo = []string{
	" ▄█   ▄█▄   ▄█████▄    ▄███████████▄    ▄███████   ▄███████",
	"███ ▄███▀  ███   ███  ███   ███   ███  ███   ███  ███   ███",
	"███▐██▀    ███   ███  ███   ███   ███  ███   ███  ███   ███",
	"▄█████▀    ███   ███  ███   ███   ███ ▄███▄▄▄███ ▄███▄▄▄██▀",
	"▀▀█████▄   ███   ███  ███   ███   ███ ▀███▀▀▀███ ▀███▀▀▀▀  ",
	"███▐██▄    ███   ███  ███   ███   ███  ███   ███ ██████████",
	"███ ▀███▄  ███   ███  ███   ███   ███  ███   ███  ███   ███",
	"███   ▀█▀   ▀█████▀    ▀█   ███   █▀   ███   █▀   ███   ███",
	"▀                                                 ███   █▀ ",
}

// SmallLogo is used when the terminal is too narrow for Logo.
var SmallLogo = []string{
	"█▄▀ █▀█ █▀▄▀█ ▄▀█ █▀█",
	"█ █ █▄█ █ ▀ █ █▀█ █▀▄",
}

type sglyph struct {
	tx, ty   int
	sx, sy   float64
	ch       string
	delay    float64
	settleAt float64
}

// Splash is the startup animation: the logo assembles out of scrambled
// characters flying in, the way the Omarchy screensaver shows its logo.
type Splash struct {
	W, H     int
	glyphs   []*sglyph
	pal      theme.RGBSet
	t        float64
	sub      string
	subX     int
	subY     int
	logoW    int
	logoX    int
	rnd      *rand.Rand
	Duration float64
}

func NewSplash(w, h int, subtitle string, pal theme.RGBSet) *Splash {
	s := &Splash{W: w, H: h, pal: pal, sub: subtitle, rnd: rand.New(rand.NewSource(rand.Int63())), Duration: 2.0}
	logo := Logo
	if w < 64 || h < 14 {
		logo = SmallLogo
	}
	lw := 0
	for _, l := range logo {
		if n := len([]rune(l)); n > lw {
			lw = n
		}
	}
	s.logoW = lw
	x0 := (w - lw) / 2
	y0 := (h - len(logo) - 2) / 2
	s.logoX = x0
	for y, l := range logo {
		for x, r := range []rune(l) {
			if r == ' ' {
				continue
			}
			g := &sglyph{tx: x0 + x, ty: y0 + y, ch: string(r)}
			g.sx = float64(s.rnd.Intn(max(w, 1)))
			g.sy = float64(s.rnd.Intn(max(h, 1)))
			g.delay = s.rnd.Float64() * 0.35
			g.settleAt = 0.55 + float64(x)/float64(lw)*0.45 + s.rnd.Float64()*0.15
			s.glyphs = append(s.glyphs, g)
		}
	}
	s.subY = y0 + len(logo) + 1
	s.subX = (w - len([]rune(subtitle))) / 2
	return s
}

func (s *Splash) Step(dt float64) { s.t += dt }
func (s *Splash) Done() bool      { return s.t >= s.Duration }

func easeOutCubic(t float64) float64 {
	t = math.Max(0, math.Min(1, t))
	return 1 - math.Pow(1-t, 3)
}

func (s *Splash) Render() string {
	cv := lipgloss.NewCanvas(s.W, s.H)
	p := s.pal
	set := func(x, y int, ch string, c theme.RGB, bold bool) {
		if x < 0 || y < 0 || x >= s.W || y >= s.H {
			return
		}
		st := uv.Style{Fg: c.Color()}
		if bold {
			st.Attrs = uv.AttrBold
		}
		cv.SetCell(x, y, &uv.Cell{Content: ch, Width: 1, Style: st})
	}
	for _, g := range s.glyphs {
		lt := s.t - g.delay
		if lt < 0 {
			continue
		}
		k := easeOutCubic(lt / (g.settleAt - g.delay))
		x := int(math.Round(g.sx + (float64(g.tx)-g.sx)*k))
		y := int(math.Round(g.sy + (float64(g.ty)-g.sy)*k))
		final := theme.Gradient(float64(g.tx-s.logoX)/float64(max(s.logoW, 1)), p.Accent, p.Magenta, p.Cyan)
		if s.t < g.settleAt {
			set(x, y, s.pick(cipherChars), theme.Lerp(p.Dim, final, k), false)
			continue
		}
		// Settled: a short white flash then the gradient color.
		flash := math.Max(0, 1-(s.t-g.settleAt)/0.25)
		set(g.tx, g.ty, g.ch, theme.Lerp(final, theme.RGB{R: 255, G: 255, B: 255}, flash*0.8), false)
	}
	if s.t > 1.0 {
		n := int((s.t - 1.0) / 0.02)
		sub := []rune(s.sub)
		if n > len(sub) {
			n = len(sub)
		}
		for i := 0; i < n; i++ {
			set(s.subX+i, s.subY, string(sub[i]), p.Dim, false)
		}
	}
	return padLines(cv.Render(), s.W, s.H)
}

func (s *Splash) pick(chars string) string {
	r := []rune(chars)
	return string(r[s.rnd.Intn(len(r))])
}

// Banner renders the logo statically (for help screen and narrow fallback).
func Banner(pal theme.RGBSet, small bool) string {
	logo := Logo
	if small {
		logo = SmallLogo
	}
	w := len([]rune(logo[0]))
	var b strings.Builder
	for i, l := range logo {
		for x, r := range []rune(l) {
			c := theme.Gradient(float64(x)/float64(w), pal.Accent, pal.Magenta, pal.Cyan)
			b.WriteString(lipgloss.NewStyle().Foreground(c.Color()).Render(string(r)))
		}
		if i < len(logo)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
