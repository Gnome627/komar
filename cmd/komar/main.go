// komar is a keyboard-driven Kubernetes TUI made to feel at home in Omarchy.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/gnome627/komar/internal/i18n"
	"github.com/gnome627/komar/internal/state"
	"github.com/gnome627/komar/internal/ui"
)

var version = "dev"

func main() {
	var opts ui.Options
	var showVersion bool
	var lang string
	flag.StringVar(&opts.Kubeconfig, "kubeconfig", "", "path to kubeconfig (default: $KUBECONFIG or ~/.kube/config)")
	flag.StringVar(&opts.Context, "context", "", "context to start in")
	flag.StringVar(&opts.Namespace, "n", "", "namespace to start in")
	flag.BoolVar(&opts.NoSplash, "no-splash", false, "skip the startup animation")
	flag.StringVar(&lang, "lang", "", "interface language: en or ru (default: from locale)")
	flag.BoolVar(&showVersion, "version", false, "print version")
	flag.Parse()

	if showVersion {
		fmt.Println("komar", version)
		return
	}

	conf := state.LoadConfig()
	switch {
	case lang != "":
		i18n.Set(i18n.Lang(lang))
	case conf.Language != "":
		i18n.Set(i18n.Lang(conf.Language))
	default:
		i18n.Set(i18n.Detect())
	}

	m, err := ui.New(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "komar:", err)
		os.Exit(1)
	}
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "komar:", err)
		os.Exit(1)
	}
}
