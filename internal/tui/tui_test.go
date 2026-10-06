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

// drive feeds the messages of due's commands back into the model; timers and
// cursor blinks are left out.
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
	case <-time.After(120 * time.Millisecond):
		return
	}
	switch msg := got.(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			drive(t, m, c)
		}
	case ledgerMsg, connMsg, doneMsg:
		_, next := m.Update(msg)
		drive(t, m, next)
	}
}

func keyOf(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

func press(t *testing.T, m *model, s string) tea.Cmd {
	t.Helper()
	_, cmd := m.Update(keyOf(s))
	if s == "q" && m.form == nil && m.prompt == pNone {
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
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.Local)
	actions.SetClock(func() time.Time { return now })
	t.Cleanup(func() { actions.SetClock(nil) })
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(cfgPath, cfg, []string{"perso"})
	m.w, m.h = 130, 32
	drive(t, m, m.loadLedger())
	return m
}

func screen(m *model) string { return ansi.Strip(m.render()) }

func add(t *testing.T, m *model, title, at, notice string) {
	t.Helper()
	press(t, m, "a")
	typeText(t, m, title)
	press(t, m, "tab")
	typeText(t, m, at)
	press(t, m, "tab")
	typeText(t, m, notice)
	press(t, m, "ctrl+s")
	if m.form != nil {
		t.Fatalf("form still open: %s\n%s", m.form.err, screen(m))
	}
}

func TestEmptyLedgerThenFormAndDetail(t *testing.T) {
	m := setup(t)
	if s := screen(m); !strings.Contains(s, "Le registre est vide") || !strings.Contains(s, "1 registre") {
		t.Fatalf("empty ledger:\n%s", s)
	}
	press(t, m, "a")
	typeText(t, m, "Résilier Swisscom")
	press(t, m, "tab")
	typeText(t, m, "15.10.2026")
	if s := screen(m); !strings.Contains(s, "→ jeu. 15.10.2026 (09:00) · dans 8 jours") {
		t.Fatalf("live date:\n%s", s)
	}
	press(t, m, "tab")
	typeText(t, m, "7d,1d")
	if s := screen(m); !strings.Contains(s, "→ message les 08.10 09:00 · 14.10 09:00") {
		t.Fatalf("live notices:\n%s", s)
	}
	press(t, m, "ctrl+s")
	s := screen(m)
	for _, want := range []string{"Ce mois-ci", "Résilier Swisscom", "dans 8 j", "Calendrier", "préavis 7d", "terme", "1 échéance ouverte"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q:\n%s", want, s)
		}
	}
}

func TestFormEditsTheEntry(t *testing.T) {
	m := setup(t)
	add(t, m, "Passeport", "01.12.2026", "30d")
	press(t, m, "e")
	if m.form == nil || m.form.val("title") != "Passeport" || m.form.val("notice") != "30d" {
		t.Fatal("e opens the form filled in")
	}
	typeText(t, m, " d'Eve")
	press(t, m, "ctrl+s")
	if s := screen(m); !strings.Contains(s, "Passeport d'Eve") || !strings.Contains(s, "PE-0001 modifiée") {
		t.Fatalf("after edit:\n%s", s)
	}
}

func TestViewsEscAndDone(t *testing.T) {
	m := setup(t)
	add(t, m, "Garantie lave-linge", "01.03.2028", "")
	if s := screen(m); !strings.Contains(s, "Plus tard") || !strings.Contains(s, "Garantie lave-linge") {
		t.Fatalf("the ledger shows every date:\n%s", s)
	}
	press(t, m, "s")
	if s := screen(m); !strings.Contains(s, "Rien d'échu") || !strings.Contains(s, "jusqu'au") {
		t.Fatalf("toutes keeps the horizon:\n%s", s)
	}
	press(t, m, "esc")
	if !m.ledgerOnly() {
		t.Fatal("esc comes back to the ledger")
	}
	press(t, m, "esc")
	if press(t, m, "esc"); !m.ledgerOnly() {
		t.Fatal("esc never quits")
	}
	press(t, m, "d")
	if len(m.items) != 0 {
		t.Fatal("a done entry leaves the list")
	}
	press(t, m, "f")
	if s := screen(m); !strings.Contains(s, "Faites et abandonnées") {
		t.Fatalf("f shows done entries:\n%s", s)
	}
}

func TestQ(t *testing.T) {
	m := setup(t)
	press(t, m, "/")
	press(t, m, "q")
	if m.filter != "q" {
		t.Fatalf("q is typed in the filter, got %q", m.filter)
	}
	press(t, m, "esc")
	press(t, m, "a")
	press(t, m, "q")
	if m.form == nil || m.form.val("title") != "q" {
		t.Fatal("q is typed in the form")
	}
	press(t, m, "esc")
	if cmd := press(t, m, "q"); cmd == nil {
		t.Fatal("q quits")
	}
}

func TestNarrowStacksTheDetail(t *testing.T) {
	m := setup(t)
	add(t, m, "Impôts", "31.03.2027", "")
	m.w, m.h = 80, 30
	if s := screen(m); !strings.Contains(s, "┄") || !strings.Contains(s, "Calendrier") {
		t.Fatalf("narrow: detail below the list:\n%s", s)
	}
	press(t, m, "tab")
	if s := screen(m); strings.Contains(s, "Calendrier") {
		t.Fatalf("tab hides the detail:\n%s", s)
	}
}

func TestTwoSpheresAndTheFormAsksWhich(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DUE_STATE", filepath.Join(dir, "state"))
	var conf strings.Builder
	conf.WriteString("spheres:\n")
	for _, sp := range []struct{ name, prefix string }{{"perso", "P"}, {"pro", "U"}} {
		root := filepath.Join(dir, sp.name)
		if err := ledger.Init(root, "none"); err != nil {
			t.Fatal(err)
		}
		conf.WriteString("  " + sp.name + ":\n    root: " + root + "\n    vcs: none\n    prefix: " + sp.prefix + "\n")
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(conf.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.Local)
	actions.SetClock(func() time.Time { return now })
	t.Cleanup(func() { actions.SetClock(nil) })
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(cfgPath, cfg, cfg.Names())
	m.w, m.h = 130, 32
	drive(t, m, m.loadLedger())

	press(t, m, "a")
	if s := screen(m); !strings.Contains(s, "Sphère") || !strings.Contains(s, "à choisir") {
		t.Fatalf("the form asks for the sphere:\n%s", s)
	}
	press(t, m, "tab")
	typeText(t, m, "Renouveler le contrat cloud")
	press(t, m, "tab")
	typeText(t, m, "31.03.2027")
	press(t, m, "ctrl+s")
	if m.form == nil || !strings.Contains(m.form.err, "sphère") {
		t.Fatal("no default sphere: saving without one is refused")
	}
	press(t, m, "l")
	press(t, m, "l")
	press(t, m, "ctrl+s")
	if m.form != nil {
		t.Fatalf("form still open: %s", m.form.err)
	}
	s := screen(m)
	if !strings.Contains(s, "UE-0001") || !strings.Contains(s, "perso + pro") || !strings.Contains(s, "U  ") {
		t.Fatalf("the entry goes to pro and shows its sphere:\n%s", s)
	}
}

func TestNowRuleBetweenPastAndFuture(t *testing.T) {
	m := setup(t)
	add(t, m, "Payer la facture", "01.10.2026", "")
	add(t, m, "Résilier l'abonnement", "20.10.2026", "")
	s := screen(m)
	late, rule, next := strings.Index(s, "PE-0001  Payer la facture"), strings.Index(s, "maintenant · mer. 07.10 10:00"), strings.Index(s, "PE-0002  Résilier")
	if late < 0 || rule < 0 || next < 0 || !(late < rule && rule < next) {
		t.Fatalf("the now rule sits between past and future:\n%s", s)
	}
}

func TestTableHidesRepeatedValues(t *testing.T) {
	m := setup(t)
	add(t, m, "Résilier Swisscom", "20.10.2026", "")
	add(t, m, "Renvoyer le routeur", "20.10.2026", "")
	s := screen(m)
	var second string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "PE-0002  Renvoyer") {
			second = l
			break
		}
	}
	if !strings.Contains(s, "Période") || !strings.Contains(s, "Ce mois-ci  mar. 20.10") || second == "" || strings.Contains(second, "20.10") || strings.Contains(second, "Ce mois-ci") {
		t.Fatalf("a table, with the date and period shown once:\n%s", s)
	}
}

func TestStateSurvivesARestart(t *testing.T) {
	m := setup(t)
	add(t, m, "Passeport", "01.12.2026", "")
	add(t, m, "Garantie", "01.03.2027", "")
	press(t, m, "k") // select the first entry
	press(t, m, "tab")
	press(t, m, "f")
	press(t, m, "c")
	press(t, m, "c")
	_, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}) // ctrl+c, not q

	again := newModel(m.cfgPath, m.cfg, m.spheres)
	again.w, again.h = 130, 32
	again.restore()
	drive(t, again, again.loadLedger())
	if again.detailOn || !again.showDone || again.critOnly {
		t.Fatalf("restored: detail %v, done %v, critical %v", again.detailOn, again.showDone, again.critOnly)
	}
	if it, ok := again.current(); !ok || it.Title != "Passeport" {
		t.Fatalf("the selected line comes back, got %+v", it)
	}
}
