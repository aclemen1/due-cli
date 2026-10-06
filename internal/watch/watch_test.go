package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherNamesTheSource(t *testing.T) {
	ledger, office := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(office, "0001-x"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := Start([]Root{
		{Source: "due", Dir: ledger, Keep: func(p string) bool { return filepath.Ext(p) == ".md" }},
		{Source: "office", Dir: office, Depth: 1, Keep: func(p string) bool { return filepath.Base(p) == "dossier.md" }},
	}, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	expect := func(want string) {
		t.Helper()
		select {
		case got := <-w.C:
			if got != want {
				t.Fatalf("got %s, want %s", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no event for %s", want)
		}
	}
	os.WriteFile(filepath.Join(ledger, "E-0001.md"), []byte("x"), 0o644)
	expect("due")
	os.WriteFile(filepath.Join(office, "0001-x", "transcript.jsonl"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(office, "0001-x", "dossier.md"), []byte("x"), 0o644)
	expect("office")
	select {
	case got := <-w.C:
		t.Fatalf("unexpected %s: the transcript must not count", got)
	case <-time.After(200 * time.Millisecond):
	}
}
