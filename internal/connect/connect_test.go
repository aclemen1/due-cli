package connect

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aclemen1/due-cli/internal/config"
)

func TestKeepRoutine(t *testing.T) {
	c := config.Connector{Owners: []string{"office:perso", "-"}, Exclude: []string{"*-ingest"}}
	daily := []string{"FREQ=DAILY;BYHOUR=7"}
	cases := []struct {
		id, owner string
		rrules    []string
		want      bool
	}{
		{"office/perso-p-desk-brief", "office:perso/P-DESK", daily, true},
		{"office/pro-u-desk-brief", "office:pro/U-DESK", daily, false},
		{"cockpit-capteurs", "", daily, true},
		{"office/perso-ingest", "office:perso", daily, false},
		{"self-sync", "", []string{"FREQ=MINUTELY;INTERVAL=5"}, false},
	}
	for _, k := range cases {
		if got := keepRoutine(c, k.id, k.owner, k.rrules); got != k.want {
			t.Errorf("%s: got %v", k.id, got)
		}
	}
}

func TestCommandConnector(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "dates.sh")
	body := `#!/bin/sh
echo '{"ok":true,"result":[{"id":"g1","title":"Garantie lave-linge","at":"2026-10-30"},{"id":"g2","title":"Loin","at":"2027-06-01"}]}'
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	items, err := One(config.Connector{Name: "mnemo", Type: "command", Run: []string{script, "{until}"}},
		Window{Until: now.AddDate(0, 0, 30), Now: now, Sphere: "perso"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "g1" || !items[0].AllDay || items[0].Source != "mnemo" {
		t.Fatalf("got %+v", items)
	}
}

func TestCommandPastKinds(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "dates.sh")
	body := `#!/bin/sh
echo '{"ok":true,"result":[
 {"id":"a","title":"IRM","at":"2026-08-25T11:15:00+02:00","kind":"appointment"},
 {"id":"b","title":"Facture ASEMA","at":"2026-08-06","kind":"payment"},
 {"id":"c","title":"Fin du bail","at":"2026-10-31","kind":"contract"}]}'
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local)
	items, err := One(config.Connector{Name: "mnemo", Type: "command", Run: []string{script}, PastKinds: []string{"payment", "legal", "contract", "renewal"}},
		Window{Until: now.AddDate(0, 0, 30), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != "b" || !items[0].Late || items[1].Detail != "contrat" {
		t.Fatalf("got %+v", items)
	}
}

func TestOfficeWaitingSince(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "office")
	body := `#!/bin/sh
echo '{"ok":true,"result":[{"id":"P-0007","title":"Sinistre","waiting_on":"AXA","wait_until":"2026-10-16T23:59:59+02:00","waiting_since":"2026-09-01T10:00:00+02:00"}]}'
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local)
	items, err := One(config.Connector{Name: "office", Type: "office", Bin: script}, Window{Until: now.AddDate(0, 0, 30), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Since == nil || items[0].Detail != "attend AXA · depuis 36 j" {
		t.Fatalf("got %+v", items)
	}
}

func TestTaskConnector(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "task")
	body := `#!/bin/sh
case "$*" in *incubating*) echo '{"ok":true,"result":{"items":[{"id":"UT-0009","title":"Relancer le fournisseur","state":"incubating","until":"2026-10-20"},{"id":"UT-0010","title":"Un jour","state":"incubating"}]}}'; exit;; esac
echo '{"ok":true,"result":{"items":[
 {"id":"UT-0001","title":"Propositions d économies","who":"Alain","due":"2026-10-09","state":"open","mine":true},
 {"id":"UT-0002","title":"Offre Camptocamp","who":"Marc","due":"2026-10-12T14:00","state":"waiting","waiting_on":"Camptocamp","mine":false,"refs":["office:U-0002","oj:RDIR-3"]},
 {"id":"UT-0003","title":"Sans date","state":"open","mine":true}],"counts":{}}}'
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.Local)
	items, err := One(config.Connector{Name: "task", Type: "task", Bin: script}, Window{Until: now.AddDate(0, 0, 30), Now: now, Sphere: "pro"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || !items[0].AllDay || items[0].Ref != "task:UT-0001" || items[1].Detail != "Marc · attend Camptocamp" || items[1].Ref != "office:U-0002" || len(items[1].Refs) != 2 || items[1].Refs[0] != "oj:RDIR-3" ||
		items[2].Title != "réveil : Relancer le fournisseur" || items[2].ID != "UT-0009@wake" {
		t.Fatalf("got %+v", items)
	}
}

func TestHideCitedDates(t *testing.T) {
	dir := t.TempDir()
	dates := filepath.Join(dir, "dates")
	tasks := filepath.Join(dir, "tasks")
	os.WriteFile(dates, []byte(`#!/bin/sh
echo '{"ok":true,"result":[
 {"id":"01A#0@2026-10-15","title":"Délai de réclamation","at":"2026-10-15","ref":"perso/finances/impots.md"},
 {"id":"01A#1@2026-10-25","title":"Acompte mensuel","at":"2026-10-25","ref":"perso/finances/impots.md"}]}'
`), 0o755)
	os.WriteFile(tasks, []byte(`#!/bin/sh
echo '{"ok":true,"result":{"items":[{"id":"PT-0121","refs":["mnemo:01A#0","mnemo:perso/finances/impots.md","contact:AC"]}]}}'
`), 0o755)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.Local)
	items, err := One(config.Connector{Name: "mnemo", Type: "command", Run: []string{dates},
		HideCited: &config.HideCited{Run: []string{tasks}, Prefix: "mnemo:"}}, Window{Until: now.AddDate(0, 0, 30), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "Acompte mensuel" {
		t.Fatalf("only the cited date is hidden, not its record: %+v", items)
	}
}
