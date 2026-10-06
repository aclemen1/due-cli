package when

import (
	"testing"
	"time"
)

var zurich, _ = time.LoadLocation("Europe/Zurich")

func TestParseMoment(t *testing.T) {
	now := time.Date(2026, 10, 6, 13, 0, 0, 0, zurich)
	cases := map[string]string{
		"2026-11-15":                "2026-11-15",
		"2026-11-15 14:00":          "2026-11-15T14:00",
		"2026-11-15T14:00":          "2026-11-15T14:00",
		"15.11.2026":                "2026-11-15",
		"5.1.2027 08:30":            "2027-01-05T08:30",
		"demain":                    "2026-10-07",
		"today":                     "2026-10-06",
		"3d":                        "2026-10-09",
		"+2w":                       "2026-10-20",
		"2h":                        "2026-10-06T15:00",
		"2026-11-15T14:00:00+01:00": "2026-11-15T14:00",
	}
	for in, want := range cases {
		m, err := ParseMoment(in, now)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if m.String() != want {
			t.Errorf("%q: got %s, want %s", in, m, want)
		}
	}
	if _, err := ParseMoment("bientôt", now); err == nil {
		t.Error("bientôt should fail")
	}
}

func TestBeforeKeepsWallClockAcrossDST(t *testing.T) {
	term := time.Date(2026, 10, 27, 9, 0, 0, 0, zurich) // after the switch of 25.10
	got, err := Before(term, "7d")
	if err != nil {
		t.Fatal(err)
	}
	if got.Hour() != 9 || got.Day() != 20 {
		t.Fatalf("got %v", got)
	}
}

func TestHorizonAndUntil(t *testing.T) {
	now := time.Date(2026, 10, 6, 13, 0, 0, 0, zurich)
	h, _ := Horizon("30d", now, "09:00")
	if h.Format("2006-01-02 15:04") != "2026-11-05 23:59" {
		t.Fatalf("horizon %v", h)
	}
	if s := Until(now.AddDate(0, 0, 7), now); s != "dans 7 jours" {
		t.Fatal(s)
	}
	if s := Day(now); s != "mar. 06.10.2026" {
		t.Fatal(s)
	}
}
