// Package when parses the dates and durations of due's command line.
package when

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var durRe = regexp.MustCompile(`^(\d+)\s*(min|h|d|w)$`)

// ParseDuration reads 30min, 2h, 7d or 2w.
func ParseDuration(s string) (time.Duration, error) {
	m := durRe.FindStringSubmatch(strings.TrimSpace(strings.ToLower(s)))
	if m == nil {
		return 0, fmt.Errorf("duration %q: expected a number and min, h, d or w, e.g. 7d", s)
	}
	n, _ := strconv.Atoi(m[1])
	unit := map[string]time.Duration{"min": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
	return time.Duration(n) * unit, nil
}

// Before moves t back by a duration; days and weeks keep the wall-clock time
// across a change of daylight saving time.
func Before(t time.Time, s string) (time.Time, error) {
	d, err := ParseDuration(s)
	if err != nil {
		return t, err
	}
	if d%(24*time.Hour) == 0 {
		return t.AddDate(0, 0, -int(d/(24*time.Hour))), nil
	}
	return t.Add(-d), nil
}

// Moment is a point in time given as a date alone or as a date and a time.
type Moment struct {
	Date   string // 2006-01-02
	Clock  string // 15:04, empty for a date alone
	AllDay bool
}

// String is the form stored in an entry: 2026-11-15 or 2026-11-15T14:00.
func (m Moment) String() string {
	if m.AllDay {
		return m.Date
	}
	return m.Date + "T" + m.Clock
}

// Time places the moment in loc; a date alone takes defaultClock.
func (m Moment) Time(loc *time.Location, defaultClock string) time.Time {
	c := m.Clock
	if m.AllDay {
		c = defaultClock
	}
	t, err := time.ParseInLocation("2006-01-02 15:04", m.Date+" "+c, loc)
	if err != nil {
		t, _ = time.ParseInLocation("2006-01-02", m.Date, loc)
	}
	return t
}

var layouts = []struct {
	layout string
	clock  bool
}{
	{"2006-01-02T15:04", true},
	{"2006-01-02 15:04", true},
	{"2006-01-02", false},
	{"02.01.2006 15:04", true},
	{"02.01.2006", false},
	{"2.1.2006 15:04", true},
	{"2.1.2006", false},
}

// ParseMoment reads 2026-11-15, 2026-11-15 14:00, 15.11.2026, 15.11.2026 14:00,
// RFC 3339, today, tomorrow, or a delay from now: 3d, 2w (a date), 2h (a time).
func ParseMoment(s string, now time.Time) (Moment, error) {
	s = strings.TrimSpace(s)
	low := strings.ToLower(s)
	switch low {
	case "today", "aujourd'hui", "auj":
		return Moment{Date: now.Format("2006-01-02"), AllDay: true}, nil
	case "tomorrow", "demain":
		return Moment{Date: now.AddDate(0, 0, 1).Format("2006-01-02"), AllDay: true}, nil
	}
	if d, err := ParseDuration(strings.TrimPrefix(low, "+")); err == nil {
		if d%(24*time.Hour) == 0 {
			return Moment{Date: now.AddDate(0, 0, int(d/(24*time.Hour))).Format("2006-01-02"), AllDay: true}, nil
		}
		t := now.Add(d)
		return Moment{Date: t.Format("2006-01-02"), Clock: t.Format("15:04")}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		t = t.In(now.Location())
		return Moment{Date: t.Format("2006-01-02"), Clock: t.Format("15:04")}, nil
	}
	for _, l := range layouts {
		t, err := time.ParseInLocation(l.layout, s, now.Location())
		if err != nil {
			continue
		}
		if l.clock {
			return Moment{Date: t.Format("2006-01-02"), Clock: t.Format("15:04")}, nil
		}
		return Moment{Date: t.Format("2006-01-02"), AllDay: true}, nil
	}
	return Moment{}, fmt.Errorf("date %q: use 2026-11-15, 2026-11-15 14:00, 15.11.2026, tomorrow or 3d", s)
}

// Horizon reads the end of a window: a delay from now (30d) or a date.
func Horizon(s string, now time.Time, defaultClock string) (time.Time, error) {
	if d, err := ParseDuration(strings.TrimPrefix(strings.TrimSpace(s), "+")); err == nil {
		if d%(24*time.Hour) == 0 {
			y, mo, da := now.AddDate(0, 0, int(d/(24*time.Hour))).Date()
			return time.Date(y, mo, da, 23, 59, 59, 0, now.Location()), nil
		}
		return now.Add(d), nil
	}
	m, err := ParseMoment(s, now)
	if err != nil {
		return time.Time{}, err
	}
	if m.AllDay {
		t, _ := time.ParseInLocation("2006-01-02", m.Date, now.Location())
		return t.Add(24*time.Hour - time.Second), nil
	}
	return m.Time(now.Location(), defaultClock), nil
}

var weekdays = []string{"dim.", "lun.", "mar.", "mer.", "jeu.", "ven.", "sam."}

// Day renders a date as « mar. 06.10.2026 ».
func Day(t time.Time) string {
	return weekdays[t.Weekday()] + " " + t.Format("02.01.2006")
}

// Until says how far t is from now, in French: « dans 7 jours », « demain », « hier ».
func Until(t, now time.Time) string {
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.Date()
	a := time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC)
	b := time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC)
	days := int(b.Sub(a).Hours() / 24)
	switch {
	case days == 0:
		return "aujourd'hui"
	case days == 1:
		return "demain"
	case days == -1:
		return "hier"
	case days > 1:
		return fmt.Sprintf("dans %d jours", days)
	default:
		return fmt.Sprintf("il y a %d jours", -days)
	}
}
