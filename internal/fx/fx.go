// Package fx animates text in the spirit of terminaltexteffects, the
// effects behind the Omarchy screensaver. komar uses them when something
// is deleted: the row burns, crumbles, rains away and so on, a different
// effect each time.
package fx

import (
	"math"
	"math/rand"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/gnome627/komar/internal/theme"
)

// Names of all delete effects.
var Names = []string{"burn", "crumble", "matrix", "explode", "decrypt", "dust", "beam", "rain"}

// Rect is an area inside the animated block, in cells.
type Rect struct{ X, Y, W, H int }

func (r Rect) contains(x, y int) bool { return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H }

type glyph struct {
	x, y   int     // original position
	px, py float64 // current position
	vx, vy float64
	ch     string
	col    theme.RGB
	delay  float64 // seconds before this glyph starts its own animation
	life   float64
	gone   bool
	landed bool
	alt    string // scramble character
}

type particle struct {
	x, y, vx, vy float64
	ch           string
	from, to     theme.RGB
	age, life    float64
	bold         bool
}

// Anim is one running effect over a rendered block of text.
type Anim struct {
	Name   string
	W, H   int
	base   string
	area   Rect // particles stay inside this (a panel's inner area)
	clear  []Rect
	glyphs []*glyph
	parts  []*particle
	pal    theme.RGBSet
	rnd    *rand.Rand
	t      float64
	dur    float64
	// columns for matrix trails
	trails []*trail
}

type trail struct {
	x       int
	y       float64
	speed   float64
	length  int
	delay   float64
	chars   []string
	stopped bool
}

var lastPick string

// Pick chooses a random effect from allowed (all when empty), never the
// same one twice in a row.
func Pick(allowed []string) string {
	pool := allowed
	if len(pool) == 0 {
		pool = Names
	}
	if len(pool) == 1 {
		lastPick = pool[0]
		return pool[0]
	}
	for {
		n := pool[rand.Intn(len(pool))]
		if n != lastPick {
			lastPick = n
			return n
		}
	}
}

// New starts an effect. base is the rendered block (w×h cells); targets are
// the rectangles whose text gets destroyed; area bounds the particles.
func New(name, base string, w, h int, targets []Rect, area Rect, pal theme.RGBSet) *Anim {
	a := &Anim{Name: name, W: w, H: h, base: base, area: area, clear: targets, pal: pal,
		rnd: rand.New(rand.NewSource(rand.Int63()))}
	cv := lipgloss.NewCanvas(w, h)
	cv.Compose(lipgloss.NewLayer(base))
	for _, r := range targets {
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				c := cv.CellAt(x, y)
				if c == nil || c.Content == "" || c.Content == " " {
					continue
				}
				col := pal.Fg
				if rgb, ok := toRGB(c.Style.Fg); ok {
					col = rgb
				}
				a.glyphs = append(a.glyphs, &glyph{x: x, y: y, px: float64(x), py: float64(y), ch: c.Content, col: col})
			}
		}
	}
	a.setup()
	return a
}

func toRGB(c any) (theme.RGB, bool) {
	type rgba interface{ RGBA() (r, g, b, a uint32) }
	if c == nil {
		return theme.RGB{}, false
	}
	if _, isNo := c.(lipgloss.NoColor); isNo {
		return theme.RGB{}, false
	}
	switch c.(type) {
	case ansi.BasicColor, ansi.IndexedColor:
		// Palette colors depend on the terminal; let effects use theme RGB.
		return theme.RGB{}, false
	}
	if v, ok := c.(rgba); ok {
		r, g, b, _ := v.RGBA()
		return theme.RGB{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8)}, true
	}
	return theme.RGB{}, false
}

// Done reports whether the effect has finished.
func (a *Anim) Done() bool { return a.t >= a.dur }

func (a *Anim) rf(lo, hi float64) float64 { return lo + a.rnd.Float64()*(hi-lo) }

func (a *Anim) pick(s string) string {
	r := []rune(s)
	return string(r[a.rnd.Intn(len(r))])
}

const (
	fireChars    = "▖▘▝▗▚▞▙▛▜▟█▓▒░"
	smokeChars   = "·˙°∙.'"
	cipherChars  = "0123456789abcdef!@#$%&*+=?/<>[]{}§¶ΣΔΩ"
	matrixChars  = "ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉ01234789Z:"
	dustChars    = "·.:∙"
	sparkChars   = "*+·✦✧"
	rainChars    = "│╎┆┊⁚:.'"
	crumbleChars = "▒░▓"
)

func (a *Anim) setup() {
	minX, maxX := a.W, 0
	for _, g := range a.glyphs {
		if g.x < minX {
			minX = g.x
		}
		if g.x > maxX {
			maxX = g.x
		}
	}
	span := float64(maxX - minX + 1)
	if span < 1 {
		span = 1
	}
	cx := float64(minX+maxX) / 2
	for _, g := range a.glyphs {
		rel := (float64(g.x) - float64(minX)) / span
		switch a.Name {
		case "burn":
			g.delay = rel*0.9 + a.rf(0, 0.15)
			g.life = a.rf(0.35, 0.6)
		case "crumble":
			g.delay = a.rf(0.25, 0.9)
			g.vy = a.rf(0, 2)
		case "matrix":
			g.delay = a.rf(0, 0.6)
			g.life = a.rf(0.2, 0.5)
		case "explode":
			ang := a.rf(-math.Pi, 0) // upward half
			dir := 1.0
			if float64(g.x) < cx {
				dir = -1
			}
			speed := a.rf(12, 38)
			g.vx = dir*math.Abs(math.Cos(ang))*speed + a.rf(-6, 6)
			g.vy = math.Sin(ang)*speed*0.45 - a.rf(0, 4)
			g.delay = math.Abs(float64(g.x)-cx) / span * 0.15
			g.life = a.rf(0.9, 1.4)
		case "decrypt":
			g.delay = a.rf(0.35, 1.1)
		case "dust":
			g.delay = rel*0.6 + a.rf(0, 0.6)
			g.vx = a.rf(6, 18)
			g.vy = a.rf(-4, -0.5)
			g.life = a.rf(0.6, 1.1)
		case "beam":
			g.delay = rel * 0.8
			g.life = a.rf(0.3, 0.6)
		case "rain":
			g.delay = a.rf(0, 0.7)
			g.vy = a.rf(0, 3)
		}
	}
	switch a.Name {
	case "burn":
		a.dur = 2.2
	case "crumble":
		a.dur = 2.3
	case "matrix":
		a.dur = 2.0
		for x := minX; x <= maxX; x++ {
			if a.rnd.Float64() < 0.7 {
				var y0 float64
				for _, g := range a.glyphs {
					if g.x == x {
						y0 = float64(g.y)
					}
				}
				a.trails = append(a.trails, &trail{x: x, y: y0, speed: a.rf(14, 34), length: 3 + a.rnd.Intn(6), delay: a.rf(0.1, 0.8)})
			}
		}
	case "explode":
		a.dur = 1.8
	case "decrypt":
		a.dur = 1.6
	case "dust":
		a.dur = 2.2
	case "beam":
		a.dur = 1.9
	case "rain":
		a.dur = 2.0
	default:
		a.dur = 1.5
	}
}

// Step advances the effect by dt seconds.
func (a *Anim) Step(dt float64) {
	a.t += dt
	p := a.pal
	for _, g := range a.glyphs {
		if g.gone {
			continue
		}
		lt := a.t - g.delay
		if lt < 0 {
			continue
		}
		switch a.Name {
		case "burn":
			if lt > g.life {
				g.gone = true
				for i := 0; i < 2; i++ {
					a.spawn(&particle{x: float64(g.x) + a.rf(-0.3, 0.3), y: float64(g.y), vx: a.rf(-1.5, 1.5), vy: a.rf(-7, -3),
						ch: a.pick(smokeChars), from: p.Dim, to: p.Bg, life: a.rf(0.6, 1.1)})
				}
				if a.rnd.Float64() < 0.4 {
					a.spawn(&particle{x: float64(g.x), y: float64(g.y), vx: a.rf(-3, 3), vy: a.rf(-12, -6),
						ch: a.pick(sparkChars), from: p.BrightYellow, to: p.Red, life: a.rf(0.3, 0.6), bold: true})
				}
			}
		case "crumble":
			if lt > 0.35 {
				g.vy += 55 * dt
				g.py += g.vy * dt
				g.px += g.vx * dt
				bottom := float64(a.area.Y + a.area.H - 1)
				if g.py >= bottom {
					g.py = bottom
					if !g.landed {
						g.landed = true
						g.life = a.t
						g.vx = 0
					}
				}
			}
			if g.landed && a.t-g.life > 0.5 {
				g.gone = true
				a.spawn(&particle{x: g.px, y: g.py, vx: a.rf(-2, 2), vy: a.rf(-3, -1), ch: a.pick(dustChars),
					from: theme.Lerp(g.col, p.Dim, 0.6), to: p.Bg, life: a.rf(0.3, 0.6)})
			}
		case "matrix", "decrypt":
			if lt > g.life+0.3 && a.Name == "matrix" {
				g.gone = true
			}
			if a.Name == "decrypt" && lt > 0.5 {
				g.gone = true
			}
			g.alt = a.pick(map[string]string{"matrix": matrixChars, "decrypt": cipherChars}[a.Name])
		case "explode":
			g.vy += 30 * dt
			g.px += g.vx * dt
			g.py += g.vy * dt
			g.vx *= 1 - 0.8*dt
			if lt > g.life || !a.area.contains(int(math.Round(g.px)), int(math.Round(g.py))) {
				g.gone = true
			}
		case "dust":
			if lt > 0 {
				g.gone = true
				a.spawn(&particle{x: float64(g.x), y: float64(g.y), vx: g.vx, vy: g.vy, ch: a.pick(dustChars),
					from: g.col, to: p.Bg, life: g.life})
			}
		case "beam":
			if lt > g.life {
				g.gone = true
				if a.rnd.Float64() < 0.5 {
					a.spawn(&particle{x: float64(g.x), y: float64(g.y), vx: a.rf(-4, 4), vy: a.rf(-6, 6),
						ch: a.pick(sparkChars), from: p.Accent, to: p.Bg, life: a.rf(0.3, 0.7), bold: true})
				}
			}
		case "rain":
			if lt > 0.2 {
				g.vy += 40 * dt
				g.py += g.vy * dt
				if !a.area.contains(g.x, int(math.Round(g.py))) {
					g.gone = true
					a.spawn(&particle{x: float64(g.x), y: float64(a.area.Y + a.area.H - 1), vx: a.rf(-5, 5), vy: a.rf(-4, -1),
						ch: "·", from: p.Cyan, to: p.Bg, life: 0.35})
				}
			}
		}
	}
	for _, tr := range a.trails {
		if a.t < tr.delay || tr.stopped {
			continue
		}
		tr.y += tr.speed * dt
		if int(tr.y)-tr.length > a.area.Y+a.area.H {
			tr.stopped = true
		}
	}
	alive := a.parts[:0]
	for _, pt := range a.parts {
		pt.age += dt
		pt.x += pt.vx * dt
		pt.y += pt.vy * dt
		if pt.age < pt.life {
			alive = append(alive, pt)
		}
	}
	a.parts = alive
}

func (a *Anim) spawn(p *particle) {
	if len(a.parts) < 2000 {
		a.parts = append(a.parts, p)
	}
}

// Render draws the current frame as a styled string of W×H cells.
func (a *Anim) Render() string {
	cv := lipgloss.NewCanvas(a.W, a.H)
	cv.Compose(lipgloss.NewLayer(a.base))
	blank := func(x, y int) { cv.SetCell(x, y, &uv.Cell{Content: " ", Width: 1}) }
	for _, r := range a.clear {
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				blank(x, y)
			}
		}
	}
	set := func(x, y int, ch string, c theme.RGB, bold bool) {
		if !a.area.contains(x, y) {
			return
		}
		st := uv.Style{Fg: c.Color()}
		if bold {
			st.Attrs = uv.AttrBold
		}
		cv.SetCell(x, y, &uv.Cell{Content: ch, Width: 1, Style: st})
	}
	p := a.pal

	for _, tr := range a.trails {
		if a.t < tr.delay {
			continue
		}
		head := int(tr.y)
		for i := 0; i < tr.length; i++ {
			y := head - i
			if y < 0 {
				continue
			}
			c := theme.Lerp(p.Green, p.Bg, float64(i)/float64(tr.length))
			if i == 0 {
				c = theme.Lerp(p.Green, theme.RGB{R: 255, G: 255, B: 255}, 0.6)
			}
			set(tr.x, y, a.pick(matrixChars), c, i == 0)
		}
	}

	for _, g := range a.glyphs {
		if g.gone {
			continue
		}
		lt := a.t - g.delay
		x, y := int(math.Round(g.px)), int(math.Round(g.py))
		ch, col, bold := g.ch, g.col, false
		switch a.Name {
		case "burn":
			if lt >= 0 {
				k := lt / g.life
				col = theme.Gradient(k, theme.RGB{R: 255, G: 255, B: 230}, p.BrightYellow, p.Orange, p.Red, theme.Lerp(p.Red, p.Bg, 0.7))
				ch = a.pick(fireChars)
				bold = k < 0.4
			} else if lt > -0.25 {
				col = theme.Lerp(g.col, p.Orange, 1+lt/0.25)
			}
		case "crumble":
			if lt >= 0 {
				col = theme.Lerp(g.col, p.Dim, math.Min(1, lt*1.5))
				if lt > 0.15 && a.rnd.Float64() < 0.3 {
					ch = a.pick(crumbleChars)
				}
			}
		case "matrix":
			if lt >= 0 {
				ch = g.alt
				col = theme.Gradient(lt/(g.life+0.3), theme.RGB{R: 220, G: 255, B: 220}, p.Green, theme.Lerp(p.Green, p.Bg, 0.6))
			}
		case "decrypt":
			if lt >= -0.3 {
				ch = g.alt
				if ch == "" {
					ch = a.pick(cipherChars)
				}
				col = theme.Gradient(a.rnd.Float64(), p.Accent, p.Magenta, p.Cyan)
				if lt > 0 {
					col = theme.Lerp(col, p.Bg, lt/0.5)
				}
			} else if a.t > 0.05 && a.rnd.Float64() < 0.08 {
				ch = a.pick(cipherChars)
				col = p.Accent
			}
		case "explode":
			if lt >= 0 {
				col = theme.Gradient(lt/g.life, theme.RGB{R: 255, G: 255, B: 240}, p.BrightYellow, p.Orange, p.Red, p.Bg)
				bold = lt < 0.3
			}
		case "beam":
			if lt >= 0 {
				col = theme.Gradient(lt/g.life, theme.RGB{R: 255, G: 255, B: 255}, p.Accent, p.Magenta, p.Bg)
				bold = true
			}
		case "rain":
			if lt >= 0 {
				col = theme.Lerp(g.col, p.Cyan, math.Min(1, lt*3))
				if lt > 0.2 {
					ch = a.pick(rainChars)
				}
			}
		}
		set(x, y, ch, col, bold)
	}

	if a.Name == "beam" {
		// The sweeping beam itself: a bright column with a soft glow.
		var minX, maxX = a.W, 0
		ys := map[int]bool{}
		for _, g := range a.glyphs {
			if g.x < minX {
				minX = g.x
			}
			if g.x > maxX {
				maxX = g.x
			}
			ys[g.y] = true
		}
		if len(ys) > 0 {
			bx := minX + int(float64(maxX-minX+1)*(a.t/0.8))
			for y := range ys {
				for d := -2; d <= 0; d++ {
					if bx+d >= minX && bx+d <= maxX+1 && a.t < 1.05 {
						c := theme.Lerp(theme.RGB{R: 255, G: 255, B: 255}, p.Accent, float64(-d)/2)
						set(bx+d, y, []string{"░", "▒", "█"}[d+2], c, true)
					}
				}
				set(bx+1, y-1, "▁", p.Accent, false)
				set(bx+1, y+1, "▔", p.Accent, false)
			}
		}
	}

	for _, pt := range a.parts {
		k := pt.age / pt.life
		set(int(math.Round(pt.x)), int(math.Round(pt.y)), pt.ch, theme.Lerp(pt.from, pt.to, k), pt.bold && k < 0.5)
	}

	out := cv.Render()
	return padLines(out, a.W, a.H)
}

func padLines(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	for len(lines) < h {
		lines = append(lines, "")
	}
	lines = lines[:h]
	for i, l := range lines {
		if lw := ansi.StringWidth(l); lw < w {
			lines[i] = l + strings.Repeat(" ", w-lw)
		}
	}
	return strings.Join(lines, "\n")
}
