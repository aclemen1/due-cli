package tui

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/aclemen1/due-cli/internal/actions"
	"github.com/aclemen1/due-cli/internal/watch"
)

// The TUI reloads itself when its binary is rebuilt, or on SIGUSR1, once at rest.

const checkEvery = 3 * time.Second

type binStamp struct {
	path  string
	mtime time.Time
	inode uint64
}

func stampOf(path string) (binStamp, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return binStamp{}, false
	}
	s := binStamp{path: path, mtime: fi.ModTime()}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		s.inode = st.Ino
	}
	return s, true
}

func ownBinary() (binStamp, bool) {
	exe, err := os.Executable()
	if err != nil {
		return binStamp{}, false
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return stampOf(exe)
}

// Build names the running binary: its version and short commit.
func Build() string {
	s := "v" + actions.Version
	if bi, ok := debug.ReadBuildInfo(); ok {
		rev, dirty := "", false
		for _, kv := range bi.Settings {
			switch kv.Key {
			case "vcs.revision":
				rev = kv.Value
			case "vcs.modified":
				dirty = kv.Value == "true"
			}
		}
		if len(rev) >= 7 {
			s += " " + rev[:7]
			if dirty {
				s += "+"
			}
		}
	}
	return s
}

type (
	binCheckMsg struct{}
	signalMsg   struct{}
)

func checkBin() tea.Cmd {
	return tea.Tick(checkEvery, func(time.Time) tea.Msg { return binCheckMsg{} })
}

func waitSignal(ch chan os.Signal) tea.Cmd {
	return func() tea.Msg { <-ch; return signalMsg{} }
}

// atRest: no form, no prompt open.
func (m *model) atRest() bool { return !m.modal.Open() && m.prompt == pNone }

// maybeReload quits for a reload when one is pending and the TUI is at rest.
func (m *model) maybeReload() tea.Cmd {
	if !m.reloadWanted || !m.atRest() {
		return nil
	}
	m.reloading = true
	m.persist()
	return tea.Quit
}

func (m *model) onBinCheck() tea.Cmd {
	if s, ok := stampOf(m.bin.path); ok && m.bin.path != "" && (!s.mtime.Equal(m.bin.mtime) || s.inode != m.bin.inode) {
		m.reloadWanted = true
	}
	if cmd := m.maybeReload(); cmd != nil {
		return cmd
	}
	return checkBin()
}

// startSignals routes SIGUSR1 to the model.
func startSignals() chan os.Signal {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	return ch
}

// execAgain replaces the process with the new binary, same arguments.
func execAgain(path string) error {
	env := append(os.Environ(), "DUE_TUI_RELOADED=1")
	return syscall.Exec(path, os.Args, env)
}

func reloadedStatus() string {
	if os.Getenv("DUE_TUI_RELOADED") == "" {
		return ""
	}
	_ = os.Unsetenv("DUE_TUI_RELOADED")
	return fmt.Sprintf("rechargé %s", Build())
}

type macosMsg watch.MacosEvent

func waitMacos(w *watch.Macos) tea.Cmd {
	return func() tea.Msg { return macosMsg(<-w.C) }
}

// onMacos re-reads the sources macos watch says changed; polling takes over if it stops.
func (m *model) onMacos(ev watch.MacosEvent) tea.Cmd {
	if ev.End {
		m.macosOn = false
		return nil
	}
	cmds := []tea.Cmd{waitMacos(m.macos)}
	switch ev.Event {
	case "ready":
		m.macosOn = true
	case "reset":
		cmds = append(cmds, m.loadAll())
	case "changed":
		for _, c := range m.connectors() {
			for _, d := range ev.Domains {
				if c.c.Type == d {
					cmds = append(cmds, m.loadConn(c.key()))
				}
			}
		}
	}
	return tea.Batch(cmds...)
}
