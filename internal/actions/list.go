package actions

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/connect"
	"github.com/aclemen1/due-cli/internal/ledger"
	"github.com/aclemen1/due-cli/internal/spec"
	"github.com/aclemen1/due-cli/internal/when"
)

// Listing is the unified list of a sphere.
type Listing struct {
	Sphere string         `json:"sphere"`
	Until  time.Time      `json:"until"`
	Items  []connect.Item `json:"items"`
	Errors []SourceError  `json:"errors,omitempty"`
}

type SourceError struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

// Query selects what List gathers.
type Query struct {
	Until   string   // horizon: 30d or a date; empty: the sphere's
	From    string   // start; empty keeps what is late
	Sources []string // connector names, due for the ledger; empty: all
	All     bool     // done and dropped entries of the ledger too
	Search  string
}

// List gathers the ledger and the connectors of a sphere.
func List(cfg *config.Config, l *ledger.Ledger, q Query) (*Listing, error) {
	now := l.Now()
	s := cfg.Spheres[l.Sphere]
	horizon := q.Until
	if horizon == "" {
		horizon = s.Horizon
	}
	until, err := when.Horizon(horizon, now, cfg.DefaultTime)
	if err != nil {
		return nil, spec.UserError("--until: %v", err)
	}
	w := connect.Window{Until: until, Now: now, Sphere: l.Sphere}
	if q.From != "" {
		m, err := when.ParseMoment(q.From, now)
		if err != nil {
			return nil, spec.UserError("--from: %v", err)
		}
		w.From = m.Time(now.Location(), "00:00")
	}
	wanted := func(name string) bool { return len(q.Sources) == 0 || contains(q.Sources, name) }
	for _, name := range q.Sources {
		if name == "due" {
			continue
		}
		found := false
		for _, c := range s.Connectors {
			found = found || c.Name == name
		}
		if !found {
			return nil, spec.UserError("no source %q in %s; sources: %s", name, l.Sphere, strings.Join(SourceNames(s), ", "))
		}
	}
	out := &Listing{Sphere: l.Sphere, Until: until, Items: []connect.Item{}}
	if wanted("due") {
		entries, err := l.List()
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.State != ledger.Open && !q.All {
				continue
			}
			it := ItemOf(l, e)
			if it.At.After(until) || (!w.From.IsZero() && it.At.Before(w.From)) {
				continue
			}
			out.Items = append(out.Items, it)
		}
	}
	var conns []config.Connector
	for _, c := range s.Connectors {
		if !c.Off && wanted(c.Name) {
			conns = append(conns, c)
		}
	}
	for _, r := range connect.Fetch(conns, w) {
		if r.Err != nil {
			out.Errors = append(out.Errors, SourceError{r.Name, r.Err.Error()})
			continue
		}
		out.Items = append(out.Items, r.Items...)
	}
	if q.Search != "" {
		needle := strings.ToLower(q.Search)
		var kept []connect.Item
		for _, it := range out.Items {
			if strings.Contains(strings.ToLower(it.Title+" "+it.Detail+" "+it.ID), needle) {
				kept = append(kept, it)
			}
		}
		out.Items = kept
		if out.Items == nil {
			out.Items = []connect.Item{}
		}
	}
	connect.Sort(out.Items)
	return out, nil
}

// SourceNames lists due and the sphere's connectors.
func SourceNames(s config.Sphere) []string {
	names := []string{"due"}
	for _, c := range s.Connectors {
		names = append(names, c.Name)
	}
	return names
}

// ItemOf turns a ledger entry into a line of the list.
func ItemOf(l *ledger.Ledger, e *ledger.Entry) connect.Item {
	m := e.Moment()
	at := m.Time(l.Loc(), "00:00")
	if !m.AllDay {
		at = m.Time(l.Loc(), l.Clock)
	}
	var parts []string
	if e.Do != "" {
		parts = append(parts, "do "+e.Do)
	}
	if len(e.Notice) > 0 {
		parts = append(parts, "notice "+strings.Join(e.Notice, ","))
	}
	if e.State != ledger.Open {
		parts = append([]string{e.State}, parts...)
	}
	it := connect.Item{Source: "due", Type: "due", ID: e.ID, Title: e.Title, At: at, AllDay: m.AllDay,
		Detail: strings.Join(parts, " · "), Ref: e.Ref, State: e.State, Do: e.Do}
	now := l.Now()
	if e.State == ledger.Open {
		if m.AllDay {
			y, mo, d := at.Date()
			it.Late = time.Date(y, mo, d, 23, 59, 59, 0, at.Location()).Before(now)
		} else {
			it.Late = at.Before(now)
		}
	}
	return it
}

func registerList() {
	spec.Register(&spec.Action{
		Category: "due", Name: "ls", Top: true,
		Summary: "List what falls due in a sphere: the ledger and every connector, by date.",
		Discussion: "Lines from connectors (reminders, calendar, office, routine, oj, command) are read live from their tool, never copied: " +
			"change them in that tool. Late lines come first. A connector that fails is reported in errors; the others still list.",
		Params: []spec.Param{
			{Name: "until", Kind: spec.String, Help: "End of the window: 7d, 30d, 2w, or a date. Defaults to the sphere's horizon (30d)."},
			{Name: "from", Kind: spec.String, Help: "Start of the window; by default late lines are kept."},
			{Name: "source", Kind: spec.StringList, Help: "Keep these sources: due (the ledger) or a connector's name. Repeatable."},
			{Name: "all", Kind: spec.Bool, Help: "Keep done and dropped entries of the ledger."},
			{Name: "search", Kind: spec.String, Help: "Keep lines whose title or detail contain this text."},
			sphereParam(),
		},
		Examples: []string{"due ls --sphere perso", "due ls --until 7d --sphere perso --format text", "due ls --source due --all --sphere perso"},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, l, err := Open(ctx)
			if err != nil {
				return nil, err
			}
			var sources []string
			for _, s := range ctx.List("source") {
				for _, x := range strings.Split(s, ",") {
					if x = strings.TrimSpace(x); x != "" {
						sources = append(sources, x)
					}
				}
			}
			res, err := List(cfg, l, Query{Until: ctx.Str("until"), From: ctx.Str("from"), Sources: sources, All: ctx.Bool("all"), Search: ctx.Str("search")})
			if err != nil {
				return nil, err
			}
			if ctx.Format == "text" {
				for _, e := range res.Errors {
					ctx.Warn(e.Source + ": " + e.Error)
				}
			}
			return res, nil
		},
		Text: textListing,
	})
}

func textListing(w io.Writer, v any) {
	res, ok := v.(*Listing)
	if !ok {
		return
	}
	if len(res.Items) == 0 {
		fmt.Fprintf(w, "Nothing falls due in %s until %s.\n", res.Sphere, when.Day(res.Until))
		return
	}
	day := ""
	for _, it := range res.Items {
		d := when.Day(it.At)
		if d != day {
			if day != "" {
				fmt.Fprintln(w)
			}
			fmt.Fprintln(w, d)
			day = d
		}
		clock := "     "
		if !it.AllDay {
			clock = it.At.Format("15:04")
		}
		mark := " "
		if it.Late {
			mark = "!"
		}
		line := fmt.Sprintf("%s %s  %-10s %s", mark, clock, it.Source, it.Title)
		if it.Type == "due" {
			line = fmt.Sprintf("%s %s  %-10s %s  %s", mark, clock, it.Source, it.ID, it.Title)
		}
		if it.Detail != "" {
			line += "  · " + it.Detail
		}
		fmt.Fprintln(w, line)
	}
}
