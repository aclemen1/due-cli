package ledger

import "time"

// Pending returns the instant of an open entry to fire at now, if any, and the
// earlier ones that came unseen: they are skipped, so a long sleep of the Mac
// fires only the latest. A notice that precedes the entry's creation never
// fires; the term does, once. Without an action, the term fires nothing.
func (l *Ledger) Pending(e *Entry, now time.Time) (fire *Instant, skip []Instant) {
	if e.State != Open {
		return nil, nil
	}
	created, err := time.Parse(time.RFC3339, e.Created)
	if err != nil {
		created = time.Time{}
	}
	var due []Instant
	for _, i := range e.Instants(l.Loc(), l.Clock) {
		if i.When.After(now) || e.HasFired(i) {
			continue
		}
		if i.Kind == "term" {
			if e.Do == "" {
				continue
			}
		} else if !i.When.After(created) {
			continue
		}
		due = append(due, i)
	}
	if len(due) == 0 {
		return nil, nil
	}
	last := due[len(due)-1]
	return &last, due[:len(due)-1]
}

// Record notes an instant in the entry's firings.
func (l *Ledger) Record(e *Entry, i Instant, result, log string) {
	e.Fired = append(e.Fired, Firing{Instant: i.When.Format(time.RFC3339), Kind: i.Kind, At: l.Now().Format(time.RFC3339), Result: result, Log: log})
}

// Settle replaces the result of a recorded instant.
func (e *Entry) Settle(i Instant, result string) bool {
	s := i.When.Format(time.RFC3339)
	for k := range e.Fired {
		if e.Fired[k].Instant == s && e.Fired[k].Kind == i.Kind {
			e.Fired[k].Result = result
			return true
		}
	}
	return false
}
