package actions

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aclemen1/due-cli/internal/ledger"
	"github.com/aclemen1/due-cli/internal/spec"
)

func TestEntriesCiteSeveralRefs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DUE_STATE", filepath.Join(dir, "state"))
	root := filepath.Join(dir, "ledger")
	if err := ledger.Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte("spheres:\n  perso:\n    root: "+root+"\n    vcs: none\n"), 0o644)
	// An entry written before refs: its single ref is read as a list of one.
	os.WriteFile(filepath.Join(root, "PE-0001.md"), []byte("---\nid: PE-0001\ntitle: Ancienne\nat: \"2026-11-01\"\nref: office:P-0002\nstate: open\ncreated: \"2026-10-01T09:00:00+02:00\"\n---\n"), 0o644)
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
	res, err := run("add", map[string]any{"title": "Fin du bail", "at": "2027-01-31", "ref": []string{"office:P-0024,contact:JC", "task:PT-0007"}, "sphere": "perso"})
	if err != nil {
		t.Fatal(err)
	}
	if e := res.(*ledger.Entry); len(e.Refs) != 3 || e.Refs[1] != "contact:JC" {
		t.Fatalf("refs: %v", e.Refs)
	}
	ls, err := run("ls", map[string]any{"ref": "contact:JC", "until": "365d"})
	if err != nil || len(ls.(*Listing).Items) != 1 {
		t.Fatalf("--ref finds an entry by any of its refs: %v %v", ls, err)
	}
	ls, _ = run("ls", map[string]any{"ref": "office:P-0002", "until": "365d"})
	if len(ls.(*Listing).Items) != 1 {
		t.Fatal("the older single ref is read")
	}
	res, err = run("edit", map[string]any{"id": "PE-0002", "ref": []string{"none"}, "sphere": "perso"})
	if err != nil || len(res.(*ledger.Entry).Refs) != 0 {
		t.Fatalf("--ref none clears: %v %v", res, err)
	}
}
