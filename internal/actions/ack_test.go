package actions

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aclemen1/due-cli/internal/ledger"
	"github.com/aclemen1/due-cli/internal/spec"
)

func TestAckTakesALineOff(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DUE_STATE", filepath.Join(dir, "state"))
	root := filepath.Join(dir, "ledger")
	if err := ledger.Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "dates")
	os.WriteFile(script, []byte(`#!/bin/sh
echo '{"ok":true,"result":[{"id":"01A#0","title":"Fin du délai de dépôt","at":"2026-09-30","kind":"legal"}]}'
`), 0o755)
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte("spheres:\n  perso:\n    root: "+root+"\n    vcs: none\n    connectors:\n      - {name: mnemo, type: command, run: [\""+script+"\"]}\n"), 0o644)
	SetClock(func() time.Time { return time.Date(2026, 10, 10, 9, 0, 0, 0, time.Local) })
	defer SetClock(nil)
	run := func(name string, args map[string]any) (any, error) {
		a := spec.Find("due", name)
		parsed, err := spec.ArgsFrom(a, args)
		if err != nil {
			return nil, err
		}
		return a.Run(&spec.Context{Args: parsed, Config: cfgPath, Format: "json"})
	}
	count := func(all bool) int {
		res, err := run("ls", map[string]any{"all": all})
		if err != nil {
			t.Fatal(err)
		}
		return len(res.(*Listing).Items)
	}
	if count(false) != 1 {
		t.Fatal("the late line is listed")
	}
	if _, err := run("ack", map[string]any{"id": "01A#0", "sphere": "perso"}); err != nil {
		t.Fatal(err)
	}
	if count(false) != 0 || count(true) != 1 {
		t.Fatal("ack takes the line off; --all shows it")
	}
	if _, err := run("ack", map[string]any{"id": "01A#0"}); err == nil {
		t.Fatal("a write needs --sphere")
	}
	if _, err := run("unack", map[string]any{"id": "01A#0", "sphere": "perso"}); err != nil {
		t.Fatal(err)
	}
	if count(false) != 1 {
		t.Fatal("unack gives it back")
	}
}
