package theme

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadColorsToml(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "colors.toml"), []byte(`
accent = "#7aa2f7"
background = "#1a1b26"
foreground = "#a9b1d6"
red = "#f7768e"
`), 0o644)
	t.Setenv("KOMAR_THEME_DIR", dir)
	t.Setenv("HOME", t.TempDir())
	p, src := Load()
	if !p.FromTheme || src.Path == "" {
		t.Fatal("expected theme palette")
	}
	if p.RGB.Accent != (RGB{0x7a, 0xa2, 0xf7}) || p.RGB.Red != (RGB{0xf7, 0x76, 0x8e}) {
		t.Errorf("colors: %+v", p.RGB)
	}
}

func TestLoadAlacritty(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "alacritty.toml"), []byte(`
[colors.primary]
background = "0x1e1e2e"
foreground = "0xcdd6f4"
[colors.normal]
red = "0xf38ba8"
green = "0xa6e3a1"
blue = "0x89b4fa"
`), 0o644)
	t.Setenv("KOMAR_THEME_DIR", dir)
	t.Setenv("HOME", t.TempDir())
	p, _ := Load()
	if !p.FromTheme || p.RGB.Green != (RGB{0xa6, 0xe3, 0xa1}) || p.RGB.Accent != (RGB{0x89, 0xb4, 0xfa}) {
		t.Errorf("alacritty palette: %+v", p.RGB)
	}
}

func TestNoThemeFallsBackToANSI(t *testing.T) {
	t.Setenv("KOMAR_THEME_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	p, _ := Load()
	if p.FromTheme {
		t.Error("expected ANSI fallback")
	}
}

func TestGradient(t *testing.T) {
	a, b := RGB{0, 0, 0}, RGB{200, 100, 50}
	if Gradient(0.5, a, b) != (RGB{100, 50, 25}) {
		t.Error("midpoint")
	}
	if Gradient(2, a, b) != b || Gradient(-1, a, b) != a {
		t.Error("clamping")
	}
}
