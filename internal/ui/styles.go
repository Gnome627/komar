package ui

import (
	"image/color"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/gnome627/komar/internal/theme"
)

// Styles are derived from the palette and rebuilt when the theme changes.
type Styles struct {
	P theme.Palette

	Text, Dim, Muted, Bright, Accent, AccentBold    lipgloss.Style
	Red, Green, Yellow, Orange, Blue, Magenta, Cyan lipgloss.Style
	Selected, SelectedDim                           lipgloss.Style
	Header                                          lipgloss.Style
	Match                                           lipgloss.Style
	Key, KeyDesc                                    lipgloss.Style
	BorderFocused, BorderBlur                       lipgloss.Style
	TitleFocused, TitleBlur                         lipgloss.Style
	TabActive, TabIdle                              lipgloss.Style
	Chip, ChipAccent                                lipgloss.Style
	ModalBorder                                     lipgloss.Style
	Danger                                          lipgloss.Style
}

func NewStyles(p theme.Palette) Styles {
	fg := func(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	s := Styles{P: p}
	s.Text = fg(p.Fg)
	s.Dim = fg(p.Dim)
	s.Muted = fg(p.Muted)
	s.Bright = fg(p.Bright).Bold(true)
	s.Accent = fg(p.Accent)
	s.AccentBold = fg(p.Accent).Bold(true)
	s.Red = fg(p.Red)
	s.Green = fg(p.Green)
	s.Yellow = fg(p.Yellow)
	s.Orange = fg(p.Orange)
	s.Blue = fg(p.Blue)
	s.Magenta = fg(p.Magenta)
	s.Cyan = fg(p.Cyan)
	if p.FromTheme {
		s.Selected = lipgloss.NewStyle().Background(p.Selection).Foreground(p.Bright).Bold(true)
		s.SelectedDim = lipgloss.NewStyle().Background(p.Selection).Foreground(p.Fg)
		s.Match = lipgloss.NewStyle().Background(p.Yellow).Foreground(p.Bg).Bold(true)
	} else {
		s.Selected = lipgloss.NewStyle().Reverse(true).Bold(true)
		s.SelectedDim = lipgloss.NewStyle().Underline(true)
		s.Match = lipgloss.NewStyle().Background(p.Yellow).Foreground(lipgloss.Color("0")).Bold(true)
	}
	s.Header = fg(p.Dim).Bold(true)
	s.Key = fg(p.Accent).Bold(true)
	s.KeyDesc = fg(p.Dim)
	s.BorderFocused = fg(p.Accent)
	s.BorderBlur = fg(p.Muted)
	s.TitleFocused = fg(p.Accent).Bold(true)
	s.TitleBlur = fg(p.Dim)
	s.TabActive = fg(p.Accent).Bold(true).Underline(true)
	s.TabIdle = fg(p.Dim)
	s.Chip = fg(p.Fg)
	s.ChipAccent = fg(p.Accent).Bold(true)
	s.ModalBorder = fg(p.Accent)
	s.Danger = fg(p.Red).Bold(true)
	return s
}

// statusStyle colors well-known status words.
func (s Styles) statusStyle(v string) (lipgloss.Style, bool) {
	switch v {
	case "Running", "Completed", "Complete", "Succeeded", "Active", "Bound", "Ready", "True", "Available", "Normal":
		return s.Green, true
	case "Pending", "ContainerCreating", "PodInitializing", "Init", "Terminating", "Unknown", "SchedulingDisabled", "Suspended", "NotReady,SchedulingDisabled":
		return s.Yellow, true
	case "Failed", "Error", "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "OOMKilled", "Evicted", "NotReady",
		"CreateContainerConfigError", "InvalidImageName", "Lost", "False", "BackoffLimitExceeded", "DeadlineExceeded", "Warning":
		return s.Red, true
	}
	if strings.HasPrefix(v, "Init:") {
		if strings.Contains(v, "Error") || strings.Contains(v, "BackOff") {
			return s.Red, true
		}
		return s.Yellow, true
	}
	return s.Text, false
}

// --- layout helpers -------------------------------------------------------

// oneLine keeps a line a line: a stray newline, carriage return or tab
// inside it would move the terminal cursor and tear the frame around it.
var oneLine = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ")

// fit truncates or pads s (which may contain ANSI) to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if strings.ContainsAny(s, "\n\r\t") {
		s = oneLine.Replace(s)
	}
	sw := ansi.StringWidth(s)
	if sw > w {
		// Cutting through a wide rune leaves the line a cell short.
		s = ansi.Truncate(s, w, "…")
		sw = ansi.StringWidth(s)
	}
	return s + strings.Repeat(" ", max(w-sw, 0))
}

// fitLeft right-aligns s in w cells.
func fitLeft(s string, w int) string {
	if sw := ansi.StringWidth(s); sw < w {
		return strings.Repeat(" ", w-sw) + s
	}
	return fit(s, w)
}

// clean makes text from the cluster safe to lay out: tabs become spaces,
// control characters go, and so do the invisible format runes (joiners,
// variation selectors, bidi marks) that terminals measure differently
// from each other, which shifts everything after them on the line.
func clean(s string) string {
	plain := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7e {
			plain = false
			break
		}
	}
	if plain {
		return s
	}
	s = strings.TrimRight(s, "\r\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString("    ")
		case r == '\n' || r == '\r':
			b.WriteByte(' ')
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Variation_Selector, r), r == utf8.RuneError:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// wrap breaks s into lines of at most w cells, on spaces where it can and
// through a word where it can't.
func wrap(s string, w int) []string {
	return strings.Split(ansi.Wrap(clean(s), max(w, 1), " "), "\n")
}

// shorten cuts s to at most w cells keeping both ends, without padding.
func shorten(s string, w int) string {
	if ansi.StringWidth(s) <= w {
		return s
	}
	return strings.TrimRight(midTrunc(s, max(w, 1)), " ")
}

// box draws a rounded panel of exactly w×h with titles embedded in the top
// border (lazygit style) and an optional footer in the bottom border.
func (s Styles) box(w, h int, title, right, footer string, focused bool, lines []string) string {
	if w < 4 || h < 2 {
		blank := make([]string, max(h, 0))
		for i := range blank {
			blank[i] = strings.Repeat(" ", max(w, 0))
		}
		return strings.Join(blank, "\n")
	}
	bs := s.BorderBlur
	if focused {
		bs = s.BorderFocused
	}
	inner := w - 2
	var b strings.Builder

	// Top border: ╭─ title ──── right ─╮
	top := bs.Render("╭─")
	used := 2
	if title != "" {
		t := " " + title + " "
		if ansi.StringWidth(t) > inner-2 {
			t = ansi.Truncate(t, inner-2, "…")
		}
		top += t
		used += ansi.StringWidth(t)
	}
	rightW := 0
	if right != "" {
		r := " " + right + " "
		maxR := w - used - 4
		if maxR > 6 {
			if ansi.StringWidth(r) > maxR {
				r = ansi.Truncate(r, maxR, "…")
			}
			rightW = ansi.StringWidth(r)
			right = r
		} else {
			right = ""
		}
	}
	fill := w - used - rightW - 2
	if fill < 0 {
		fill = 0
	}
	top += bs.Render(strings.Repeat("─", fill))
	if right != "" {
		top += right
	}
	top += bs.Render("─╮")
	b.WriteString(fit(top, w))
	b.WriteByte('\n')

	side := bs.Render("│")
	for i := 0; i < h-2; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		b.WriteString(side + fit(line, inner) + side)
		b.WriteByte('\n')
	}

	bottom := bs.Render("╰─")
	bused := 2
	if footer != "" {
		f := " " + footer + " "
		if ansi.StringWidth(f) > inner-2 {
			f = ansi.Truncate(f, inner-2, "…")
		}
		bottom += f
		bused += ansi.StringWidth(f)
	}
	bottom += bs.Render(strings.Repeat("─", max(w-bused-2, 0)) + "─╯")
	b.WriteString(fit(bottom, w))
	return b.String()
}

// dots draws replicas like ●●●○ (ready of desired).
func (s Styles) dots(ready, desired int) string {
	if desired > 10 || ready > 10 {
		st := s.Green
		if ready < desired {
			st = s.Yellow
		}
		return st.Render(itoa(ready) + "/" + itoa(desired))
	}
	var b strings.Builder
	for i := 0; i < max(desired, ready); i++ {
		switch {
		case i < ready && i < desired:
			b.WriteString(s.Green.Render("●"))
		case i < ready:
			b.WriteString(s.Yellow.Render("●"))
		default:
			b.WriteString(s.Muted.Render("○"))
		}
	}
	if desired == 0 && ready == 0 {
		b.WriteString(s.Muted.Render("◌"))
	}
	return b.String()
}

// spark renders values as a sparkline using block eighths, scaled to
// maxV (square-root scale so rare events stay visible next to noisy ones).
func spark(vals []float64, maxV float64) string {
	const bars = "▁▂▃▄▅▆▇█"
	if maxV <= 0 {
		for _, v := range vals {
			maxV = math.Max(maxV, v)
		}
	}
	r := []rune(bars)
	var b strings.Builder
	for _, v := range vals {
		if v <= 0 || maxV <= 0 {
			b.WriteRune(' ')
			continue
		}
		i := int(math.Sqrt(v/maxV) * float64(len(r)-1))
		b.WriteRune(r[min(i, len(r)-1)])
	}
	return b.String()
}

// meter renders a small usage bar like ▰▰▰▱▱.
func (s Styles) meter(pct float64, n int) string {
	filled := int(pct/100*float64(n) + 0.5)
	if filled > n {
		filled = n
	}
	st := s.Green
	if pct > 70 {
		st = s.Yellow
	}
	if pct > 90 {
		st = s.Red
	}
	return st.Render(strings.Repeat("▰", filled)) + s.Muted.Render(strings.Repeat("▱", n-filled))
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		buf[p] = '-'
	}
	return string(buf[p:])
}

// overlay places fg centered over bg (both multi-line, bg is w×h).
func overlay(bg, fg string, w, h int) string {
	fw, fh := lipgloss.Size(fg)
	x := max((w-fw)/2, 0)
	y := max((h-fh)/2, 0)
	return overlayAt(bg, fg, x, y)
}

func overlayAt(bg, fg string, x, y int) string {
	c := lipgloss.NewCompositor(
		lipgloss.NewLayer(bg),
		lipgloss.NewLayer(fg).X(x).Y(y).Z(1),
	)
	// The compositor drops trailing blanks and grows with a layer that
	// sticks out; the result has to stay the size of bg.
	w, h := lipgloss.Size(bg)
	lines := strings.Split(c.Render(), "\n")
	for len(lines) < h {
		lines = append(lines, "")
	}
	lines = lines[:h]
	for i, l := range lines {
		lines[i] = fit(l, w)
	}
	return strings.Join(lines, "\n")
}
