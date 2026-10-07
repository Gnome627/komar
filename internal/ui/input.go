package ui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// lineEdit is a tiny single-line editor used by every prompt.
type lineEdit struct {
	val []rune
	pos int
}

func newLineEdit(s string) lineEdit {
	r := []rune(s)
	return lineEdit{val: r, pos: len(r)}
}

func (e *lineEdit) String() string { return string(e.val) }

func (e *lineEdit) Set(s string) {
	e.val = []rune(s)
	e.pos = len(e.val)
}

// Keys of the ЙЦУКЕН layout by the QWERTY key they sit on.
const (
	cyrKeys = "йцукенгшщзхъфывапролджэячсмитьбюёЙЦУКЕНГШЩЗХЪФЫВАПРОЛДЖЭЯЧСМИТЬБЮЁіїєґІЇЄҐў"
	latKeys = "qwertyuiop[]asdfghjkl;'zxcvbnm,.`QWERTYUIOP{}ASDFGHJKL:\"ZXCVBNM<>~s]'\\S}\"|o"
)

var keyPositions = func() map[rune]rune {
	m := map[rune]rune{}
	lat := []rune(latKeys)
	for i, r := range []rune(cyrKeys) {
		m[r] = lat[i]
	}
	// The / and ? key gives . and , in ЙЦУКЕН. Neither is a shortcut of its
	// own, so they open search and help in any layout.
	m['.'], m[','] = '/', '?'
	return m
}()

// hotkey names a key press the way shortcuts are written, by the position
// of the key rather than the letter the current layout puts on it: with a
// Russian layout on, в is "d" and ctrl+ц is "ctrl+w". Text typed into an
// input still comes from k.Text.
func hotkey(k tea.KeyPressMsg) string {
	s := k.String()
	r, size := utf8.DecodeLastRuneInString(s)
	if l, ok := keyPositions[r]; ok && (size == len(s) || s[len(s)-size-1] == '+') {
		return s[:len(s)-size] + string(l)
	}
	return s
}

// Handle applies an editing key; it returns false for keys it doesn't use.
func (e *lineEdit) Handle(k tea.KeyPressMsg) bool {
	switch hotkey(k) {
	case "left", "ctrl+b":
		if e.pos > 0 {
			e.pos--
		}
	case "right", "ctrl+f":
		if e.pos < len(e.val) {
			e.pos++
		}
	case "home", "ctrl+a":
		e.pos = 0
	case "end", "ctrl+e":
		e.pos = len(e.val)
	case "backspace", "ctrl+h":
		if e.pos > 0 {
			e.val = append(e.val[:e.pos-1], e.val[e.pos:]...)
			e.pos--
		}
	case "delete", "ctrl+d":
		if e.pos < len(e.val) {
			e.val = append(e.val[:e.pos], e.val[e.pos+1:]...)
		}
	case "ctrl+w", "alt+backspace":
		i := e.pos
		for i > 0 && e.val[i-1] == ' ' {
			i--
		}
		for i > 0 && e.val[i-1] != ' ' {
			i--
		}
		e.val = append(e.val[:i], e.val[e.pos:]...)
		e.pos = i
	case "ctrl+u":
		e.val = e.val[e.pos:]
		e.pos = 0
	case "ctrl+k":
		e.val = e.val[:e.pos]
	case "space":
		e.insert(" ")
	default:
		if k.Text != "" && !k.Mod.Contains(tea.ModCtrl) && !k.Mod.Contains(tea.ModAlt) {
			e.insert(k.Text)
			return true
		}
		return false
	}
	return true
}

func (e *lineEdit) insert(s string) {
	var rs []rune
	for _, r := range s {
		if unicode.IsPrint(r) {
			rs = append(rs, r)
		}
	}
	nv := make([]rune, 0, len(e.val)+len(rs))
	nv = append(nv, e.val[:e.pos]...)
	nv = append(nv, rs...)
	nv = append(nv, e.val[e.pos:]...)
	e.val = nv
	e.pos += len(rs)
}

// Paste inserts pasted text (newlines become spaces).
func (e *lineEdit) Paste(s string) {
	e.insert(strings.ReplaceAll(s, "\n", " "))
}

// View renders the text with a block cursor, scrolled to fit w cells.
func (e *lineEdit) View(s Styles, w int) string {
	start := 0
	if e.pos >= w-1 {
		start = e.pos - w + 2
	}
	vis := e.val[start:]
	cur := e.pos - start
	var b strings.Builder
	for i, r := range vis {
		if i >= w-1 {
			break
		}
		if i == cur {
			b.WriteString(s.Selected.Render(string(r)))
		} else {
			b.WriteString(s.Bright.UnsetBold().Render(string(r)))
		}
	}
	if cur >= len(vis) {
		b.WriteString(s.Selected.Render(" "))
	}
	return b.String()
}
