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
