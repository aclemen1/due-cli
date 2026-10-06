package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/aclemen1/due-cli/internal/actions"
	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/ledger"
)

// drive feeds the messages of due's commands back into the model.
func drive(t *testing.T, m *model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var got tea.Msg
	select {
	case got = <-ch:
	case <-time.After(200 * time.Millisecond): // a cursor blink, not ours
		return
	}
	switch msg := got.(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			drive(t, m, c)
		}
	case loadedMsg, doneMsg, detailMsg:
		_, next := m.Update(msg)
		drive(t, m, next)
	}
}

func press(t *testing.T, m *model, s string) tea.Cmd {
	t.Helper()
	var k tea.KeyPressMsg
	switch s {
	case "enter":
		k = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		k = tea.KeyPressMsg{Code: tea.KeyEscape}
	default:
		k = tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
	}
	_, cmd := m.Update(k)
	if s == "q" {
		return cmd
	}
	drive(t, m, cmd)
	return nil
}

func typeText(t *testing.T, m *model, s string) {
	for _, r := range s {
		press(t, m, string(r))
	}
}

func setup(t *testing.T) *model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DUE_STATE", filepath.Join(dir, "state"))
	root := filepath.Join(dir, "ledger")
	if err := ledger.Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("spheres:\n  perso:\n    root: "+root+"\n    vcs: none\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 6, 13, 0, 0, 0, time.Local)
	actions.SetClock(func() time.Time { return now })
	t.Cleanup(func() { actions.SetClock(nil) })
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(cfgPath, cfg, "perso")
	m.w, m.h = 120, 30
	drive(t, m, m.load())
	return m
}

func screen(m *model) string { return ansi.Strip(m.render()) }

func TestAddDetailDoneAndKeys(t *testing.T) {
	m := setup(t)
	if !strings.Contains(screen(m), "Rien d'échu") {
		t.Fatalf("empty list:\n%s", screen(m))
	}
	press(t, m, "a")
	typeText(t, m, "Renouveler le passeport")
	press(t, m, "enter")
	typeText(t, m, "20.10.2026")
	press(t, m, "enter")
	typeText(t, m, "7d,1d")
	press(t, m, "enter")
	s := screen(m)
	if !strings.Contains(s, "E-0001 Renouveler le passeport") || !strings.Contains(s, "mar. 20.10.2026") {
		t.Fatalf("after add:\n%s", s)
	}
	press(t, m, "enter")
	s = screen(m)
	if m.view != vDetail || !strings.Contains(s, "notice 7d") || !strings.Contains(s, "préavis") {
		t.Fatalf("detail:\n%s", s)
	}
	press(t, m, "esc")
	if m.view != vList {
		t.Fatal("esc must go back to the list")
	}
	press(t, m, "esc")
	if press(t, m, "esc"); m.view != vList {
		t.Fatal("esc in the list must not quit")
	}
	press(t, m, "d")
	if !strings.Contains(screen(m), "E-0001 faite") || len(m.items) != 0 {
		t.Fatalf("after done:\n%s", screen(m))
	}
	if cmd := press(t, m, "q"); cmd == nil {
		t.Fatal("q must quit")
	}
}

func TestQTypesInPrompt(t *testing.T) {
	m := setup(t)
	press(t, m, "/")
	press(t, m, "q")
	if m.prompt != pFilter || m.filter != "q" {
		t.Fatalf("q must be typed in the filter, got %q", m.filter)
	}
}
