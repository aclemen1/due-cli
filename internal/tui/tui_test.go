package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aclemen1/tuikit"
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
	case ledgerMsg, connMsg, doneMsg, notesMsg, tuikit.DoneMsg, tuikit.CancelMsg:
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
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

func press(t *testing.T, m *model, s string) tea.Cmd {
	t.Helper()
	_, cmd := m.Update(keyOf(s))
	if s == "q" && !m.modal.Open() && m.prompt == pNone {
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
	if m.modal.Open() {
		t.Fatalf("form still open:\n%s", screen(m))
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
	if s := screen(m); !strings.Contains(s, "→ 7d jeu. 08.10.2026 · 1d mer. 14.10.2026") {
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
	press(t, m, "E")
	if s := screen(m); !m.modal.Open() || !strings.Contains(s, "Modifier PE-0001") || !strings.Contains(s, "30d") {
		t.Fatalf("E opens the form filled in:\n%s", s)
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
	press(t, m, "2")
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
	if !m.modal.Open() {
		t.Fatal("q is typed in the form, which stays open")
	}
	press(t, m, "esc")
	press(t, m, "o")
	press(t, m, "enter")
	if m.modal.Open() {
		t.Fatalf("esc then Oui closes the form:\n%s", screen(m))
	}
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
	if s := screen(m); !strings.Contains(s, "Sphère *") || !strings.Contains(s, "( ) perso   ( ) pro") {
		t.Fatalf("the form asks for the sphere:\n%s", s)
	}
	press(t, m, "tab")
	typeText(t, m, "Renouveler le contrat cloud")
	press(t, m, "tab")
	typeText(t, m, "31.03.2027")
	press(t, m, "ctrl+s")
	if !m.modal.Open() {
		t.Fatal("no default sphere: saving without one is refused")
	}
	for m.modal.Open() && !strings.Contains(screen(m), "› Sphère") {
		press(t, m, "shift+tab")
	}
	press(t, m, "right")
	press(t, m, "right")
	press(t, m, "ctrl+s")
	if m.modal.Open() {
		t.Fatalf("form still open:\n%s", screen(m))
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
	press(t, m, "!")
	press(t, m, "!")
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

func TestConventionKeys(t *testing.T) {
	m := setup(t)
	press(t, m, "c")
	if !m.modal.Open() {
		t.Fatal("c opens a new entry")
	}
	press(t, m, "esc")
	add(t, m, "Garantie", "01.03.2027", "")
	add(t, m, "Bail", "31.10.2026", "")
	press(t, m, "G")
	press(t, m, "g")
	press(t, m, "g")
	if it, _ := m.current(); it.Title != "Bail" {
		t.Fatalf("gg goes to the top, got %s", it.Title)
	}
	press(t, m, "]")
	if it, _ := m.current(); it.Title != "Garantie" {
		t.Fatalf("] goes to the next group, got %s", it.Title)
	}
	press(t, m, "space")
	if len(m.items) != 1 {
		t.Fatal("space marks the entry done")
	}
	press(t, m, "t")
	if !strings.Contains(screen(m), "tri titre") {
		t.Fatalf("t sorts by title:\n%s", screen(m))
	}
	press(t, m, "h")
	press(t, m, "x")
	if !m.modal.Open() || !strings.Contains(screen(m), "Abandonner") {
		t.Fatal("x asks before dropping")
	}
	press(t, m, "esc")
	press(t, m, "#")
	if !m.modal.Open() || !strings.Contains(screen(m), "Recopiez") {
		t.Fatalf("# asks for the id before deleting:\n%s", screen(m))
	}
}

func TestNotesThroughTheNoteTool(t *testing.T) {
	m := setup(t)
	dir := t.TempDir()
	store := filepath.Join(dir, "notes.txt")
	add := filepath.Join(dir, "note-add")
	ls := filepath.Join(dir, "note-ls")
	os.WriteFile(add, []byte("#!/bin/sh\necho \"$1|$3|$(cat)\" >> "+store+"\n"), 0o755)
	os.WriteFile(ls, []byte("#!/bin/sh\nif [ -s "+store+" ]; then b=$(cut -d'|' -f3 "+store+"); echo \"{\\\"ok\\\":true,\\\"result\\\":{\\\"items\\\":[{\\\"id\\\":\\\"PN-0001\\\",\\\"created\\\":\\\"2026-10-07T10:00:00+02:00\\\",\\\"body\\\":\\\"$b\\\"}]}}\"; else echo '{\"ok\":true,\"result\":{\"items\":[]}}'; fi\n"), 0o755)
	m.cfg.Notes.Ls = []string{ls, "{ref}"}
	m.cfg.Notes.Add = []string{add, "{ref}", "x", "{sphere}"}
	pressAdd(t, m)
	press(t, m, "N")
	if !m.modal.Open() || !strings.Contains(screen(m), "Note sur PE-0001") {
		t.Fatalf("N opens the note editor:\n%s", screen(m))
	}
	typeText(t, m, "Copie du contrat chez le notaire")
	press(t, m, "ctrl+s")
	var b []byte
	for i := 0; i < 40 && len(b) == 0; i++ {
		time.Sleep(50 * time.Millisecond)
		b, _ = os.ReadFile(store)
	}
	if string(b) != "due:PE-0001|perso|Copie du contrat chez le notaire\n" {
		t.Fatalf("note add got %q", b)
	}
	drive(t, m, m.loadLedger())
	m.Update(m.loadNotes()())
	if s := screen(m); !strings.Contains(s, "Notes") || !strings.Contains(s, "Copie du contrat chez le notaire") {
		t.Fatalf("the detail lists the notes:\n%s", s)
	}
}

func pressAdd(t *testing.T, m *model) {
	t.Helper()
	add(t, m, "Fin du bail", "31.10.2026", "")
}

func TestSelectOpensOnTheLine(t *testing.T) {
	m := setup(t)
	add(t, m, "Garantie", "01.03.2027", "")
	add(t, m, "Bail", "31.10.2026", "")
	again := newModel(m.cfgPath, m.cfg, m.spheres)
	again.w, again.h = 130, 32
	again.pendingSelect = "pe-1"
	drive(t, again, again.loadLedger())
	if it, ok := again.current(); !ok || it.ID != "PE-0001" || !again.detailOn {
		t.Fatalf("--select opens on PE-0001, got %+v", it)
	}
	unknown := newModel(m.cfgPath, m.cfg, m.spheres)
	unknown.w, unknown.h = 130, 32
	unknown.pendingSelect = "XX-9"
	drive(t, unknown, unknown.loadLedger())
	if unknown.pendingSelect != "" || !strings.Contains(screen(unknown), "introuvable") {
		t.Fatalf("an unknown id gives a message:\n%s", screen(unknown))
	}
}
