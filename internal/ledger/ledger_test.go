package ledger

import (
	"testing"
	"time"
)

var zurich, _ = time.LoadLocation("Europe/Zurich")

func newLedger(t *testing.T, now time.Time) *Ledger {
	t.Helper()
	root := t.TempDir()
	if err := Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	return &Ledger{Sphere: "perso", Root: root, VCS: "none", By: "test", Clock: "09:00", Now: func() time.Time { return now }}
}

func TestSaveAndRead(t *testing.T) {
	now := time.Date(2026, 10, 6, 13, 0, 0, 0, zurich)
	l := newLedger(t, now)
	e := &Entry{ID: l.NextID(), Title: "Passeport", At: "2026-12-01", Notice: []string{"30d"}, State: Open, Created: now.Format(time.RFC3339), Body: "Prendre rendez-vous.\n\n---\nfin"}
	if err := l.Save(e); err != nil {
		t.Fatal(err)
	}
	got, err := l.Get("1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "E-0001" || got.Title != "Passeport" || got.Body != e.Body || got.Notice[0] != "30d" {
		t.Fatalf("got %+v", got)
	}
	if l.NextID() != "E-0002" {
		t.Fatal(l.NextID())
	}
}

func TestPending(t *testing.T) {
	created := time.Date(2026, 10, 1, 12, 0, 0, 0, zurich)
	e := &Entry{ID: "E-0001", Title: "x", At: "2026-10-20", Notice: []string{"14d", "7d", "1d"}, Do: "tell", State: Open, Created: created.Format(time.RFC3339)}
	at := func(s string) time.Time { t, _ := time.ParseInLocation("2006-01-02 15:04", s, zurich); return t }
	l := newLedger(t, created)

	// 14d (06.10 09:00) is after the creation: it fires.
	fire, skip := l.Pending(e, at("2026-10-06 09:01"))
	if fire == nil || fire.Kind != "notice 14d" || len(skip) != 0 {
		t.Fatalf("06.10: %v %v", fire, skip)
	}
	l.Record(e, *fire, "ok", "")
	if fire, _ := l.Pending(e, at("2026-10-06 10:00")); fire != nil {
		t.Fatalf("fired twice: %v", fire)
	}
	// Asleep from 12.10 to 20.10 at noon: 7d and 1d are skipped, the term fires.
	fire, skip = l.Pending(e, at("2026-10-20 12:00"))
	if fire == nil || fire.Kind != "term" || len(skip) != 2 {
		t.Fatalf("20.10: %v %v", fire, skip)
	}
}

func TestPendingRules(t *testing.T) {
	created := time.Date(2026, 10, 6, 13, 0, 0, 0, zurich)
	l := newLedger(t, created)
	// The term came before the creation: it fires once; its notice never does.
	e := &Entry{ID: "E-0002", At: "2026-10-06T10:00", Notice: []string{"1h"}, Do: "tell", State: Open, Created: created.Format(time.RFC3339)}
	fire, skip := l.Pending(e, created.Add(time.Minute))
	if fire == nil || fire.Kind != "term" || len(skip) != 0 {
		t.Fatalf("%v %v", fire, skip)
	}
	// Without an action the term fires nothing.
	e.Do = ""
	if fire, _ := l.Pending(e, created.Add(time.Minute)); fire != nil {
		t.Fatalf("no action: %v", fire)
	}
	// Done entries never fire.
	e.Do, e.State = "tell", Done
	if fire, _ := l.Pending(e, created.Add(time.Minute)); fire != nil {
		t.Fatal("done entry fired")
	}
}
