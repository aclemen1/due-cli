package actions

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/connect"
	"github.com/aclemen1/due-cli/internal/judge"
	"github.com/aclemen1/due-cli/internal/ledger"
	"github.com/aclemen1/due-cli/internal/spec"
	"github.com/aclemen1/due-cli/internal/when"
)

// Listing is the unified list of one sphere or more.
type Listing struct {
	Spheres []string       `json:"spheres"`
	Until   time.Time      `json:"until"`
	Items   []connect.Item `json:"items"`
	Errors  []SourceError  `json:"errors,omitempty"`
}

type SourceError struct {
	Sphere string `json:"sphere"`
	Source string `json:"source"`
	Error  string `json:"error"`
}

// Query selects what List gathers.
type Query struct {
	Until    string   // horizon: 30d or a date; empty: the sphere's
	From     string   // start; empty keeps what is late
	Sources  []string // connector names, due for the ledger; empty: all
	All      bool     // done and dropped entries of the ledger too
	Critical bool     // only the lines the judge finds critical
	Waiting  string   // only the office lines waiting for at least this long, e.g. 30d
	Ref      string   // only the lines of this ref, e.g. oj:RDIR-17 or office:P-0040
	Search   string
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
	out := &Listing{Spheres: []string{l.Sphere}, Until: until, Items: []connect.Item{}}
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
			out.Errors = append(out.Errors, SourceError{l.Sphere, r.Name, r.Err.Error()})
			continue
		}
		out.Items = append(out.Items, r.Items...)
	}
	if acks := l.AckSet(); len(acks) > 0 {
		kept := []connect.Item{}
		for _, it := range out.Items {
			if it.Type != "due" && acks[ledger.AckKey(it.Source, it.ID, it.At.Format(time.RFC3339))] {
				if !q.All {
					continue
				}
				it.State, it.Late = "acked", false
			}
			kept = append(kept, it)
		}
		out.Items = kept
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
	for i := range out.Items {
		out.Items[i].Sphere = l.Sphere
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
	it := connect.Item{Sphere: l.Sphere, Source: "due", Type: "due", ID: e.ID, Title: e.Title, At: at, AllDay: m.AllDay,
		Detail: strings.Join(parts, " · "), Ref: e.FirstRef(), State: e.State, Do: e.Do}
	if len(e.Refs) > 1 {
		it.Refs = e.Refs[1:]
	}
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
		Summary: "List what falls due: the ledgers and every connector, by date, in every sphere unless --sphere narrows it.",
		Discussion: "Lines from connectors (reminders, calendar, office, routine, oj, command) are read live from their tool, never copied: " +
			"change them in that tool. Each line names its sphere. A connector that fails is reported in errors; the others still list.",
		Params: []spec.Param{
			{Name: "until", Kind: spec.String, Help: "End of the window: 7d, 30d, 2w, or a date. Defaults to the sphere's horizon (30d)."},
			{Name: "from", Kind: spec.String, Help: "Start of the window; by default late lines are kept."},
			{Name: "source", Kind: spec.StringList, Help: "Keep these sources: due (the ledger) or a connector's name. Repeatable."},
			{Name: "all", Kind: spec.Bool, Help: "Keep done and dropped entries of the ledger, and the lines taken off with due ack (state acked)."},
			{Name: "critical", Kind: spec.Bool, Help: "Keep the lines the judge finds critical if forgotten (due assess)."},
			{Name: "ref", Kind: spec.String, Help: "Keep the lines of this ref, e.g. oj:RDIR-17, office:P-0040, task:PT-0007."},
			{Name: "waiting-since", Kind: spec.String, Help: "Keep the office dossiers waiting for at least this long, e.g. 30d. Waits without a deadline are not lines of due: use office ls --waiting-since."},
			{Name: "search", Kind: spec.String, Help: "Keep lines whose title or detail contain this text."},
			readSphereParam(),
		},
		Examples: []string{"due ls", "due ls --until 7d --format text", "due ls --source due --all --sphere pro", "due ls --source office --waiting-since 30d", "due ls --ref office:P-0040 --until 365d"},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, err := config.Load(ctx.Config)
			if err != nil {
				return nil, err
			}
			spheres, err := ReadSpheres(ctx, cfg)
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
			res, err := ListAll(ctx, cfg, spheres, Query{Until: ctx.Str("until"), From: ctx.Str("from"), Sources: sources, All: ctx.Bool("all"), Critical: ctx.Bool("critical"), Waiting: ctx.Str("waiting-since"), Ref: ctx.Str("ref"), Search: ctx.Str("search")})
			if err != nil {
				return nil, err
			}
			if ctx.Format == "text" {
				for _, e := range res.Errors {
					ctx.Warn(e.Sphere + "/" + e.Source + ": " + e.Error)
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
		fmt.Fprintf(w, "Nothing falls due in %s until %s.\n", strings.Join(res.Spheres, ", "), when.Day(res.Until))
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
		if it.Critical != nil && *it.Critical >= 0.5 {
			mark = "⚠"
		}
		src := it.Source
		if len(res.Spheres) > 1 {
			src = it.Sphere + "/" + it.Source
		}
		line := fmt.Sprintf("%s %s  %-14s %s", mark, clock, src, it.Title)
		if it.Type == "due" {
			line = fmt.Sprintf("%s %s  %-14s %s  %s", mark, clock, src, it.ID, it.Title)
		}
		if it.Detail != "" {
			line += "  · " + it.Detail
		}
		fmt.Fprintln(w, line)
	}
}

// ListAll gathers several spheres at once and merges them by date. A source
// named in the query must exist in one of them at least.
func ListAll(ctx *spec.Context, cfg *config.Config, spheres []string, q Query) (*Listing, error) {
	for _, name := range q.Sources {
		found := name == "due"
		var all []string
		for _, sp := range spheres {
			for _, n := range SourceNames(cfg.Spheres[sp]) {
				found = found || n == name
				if !contains(all, n) {
					all = append(all, n)
				}
			}
		}
		if !found {
			return nil, spec.UserError("no source %q in %s; sources: %s", name, strings.Join(spheres, ", "), strings.Join(all, ", "))
		}
	}
	results := make([]*Listing, len(spheres))
	errs := make([]error, len(spheres))
	var wg sync.WaitGroup
	for i, sp := range spheres {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := OpenLedger(ctx, cfg, sp)
			if err != nil {
				errs[i] = err
				return
			}
			results[i], errs[i] = List(cfg, l, q)
		}()
	}
	wg.Wait()
	out := &Listing{Spheres: spheres, Items: []connect.Item{}}
	for i, r := range results {
		if errs[i] != nil {
			return nil, errs[i]
		}
		out.Items = append(out.Items, r.Items...)
		out.Errors = append(out.Errors, r.Errors...)
		if r.Until.After(out.Until) {
			out.Until = r.Until
		}
	}
	judge.Annotate(out.Items)
	if q.Ref != "" {
		kept := []connect.Item{}
		for _, it := range out.Items {
			ok := strings.EqualFold(it.Ref, q.Ref)
			for _, r := range it.Refs {
				ok = ok || strings.EqualFold(r, q.Ref)
			}
			if ok {
				kept = append(kept, it)
			}
		}
		out.Items = kept
	}
	if q.Waiting != "" {
		d, err := when.ParseDuration(q.Waiting)
		if err != nil {
			return nil, spec.UserError("--waiting-since: %v", err)
		}
		kept := []connect.Item{}
		for _, it := range out.Items {
			if it.Since != nil && !it.Since.After(Now().Add(-d)) {
				kept = append(kept, it)
			}
		}
		out.Items = kept
	}
	if q.Critical {
		kept := []connect.Item{}
		for _, it := range out.Items {
			if judge.IsCritical(it, cfg.Judge.Threshold) {
				kept = append(kept, it)
			}
		}
		out.Items = kept
	}
	connect.Sort(out.Items)
	return out, nil
}
