package actions

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/ledger"
	"github.com/aclemen1/due-cli/internal/spec"
	"github.com/aclemen1/due-cli/internal/when"
)

func idParam() spec.Param {
	return spec.Param{Name: "id", Kind: spec.String, Positional: true, Required: true, Help: "Entry id, e.g. PE-0007 (P: perso), UE-0007 (U: pro), or 7 with --sphere."}
}

func notices(ctx *spec.Context) ([]string, error) {
	var out []string
	for _, v := range ctx.List("notice") {
		for _, n := range strings.Split(v, ",") {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			if _, err := when.ParseDuration(n); err != nil {
				return nil, spec.UserError("--notice: %v", err)
			}
			out = append(out, n)
		}
	}
	return out, nil
}

func body(ctx *spec.Context) (string, error) {
	b := ctx.Str("body")
	if b == "-" && ctx.Stdin != nil {
		raw, err := io.ReadAll(ctx.Stdin)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(raw)), nil
	}
	return b, nil
}

func checkAction(do, run string) error {
	if do == "command" && strings.TrimSpace(run) == "" {
		return spec.UserError("--do command needs --run. Example: due add \"Sauvegarde\" --at 2026-11-01 --do command --run 'restic backup ~/Documents'")
	}
	if do != "command" && run != "" {
		return spec.UserError("--run goes with --do command")
	}
	return nil
}

// change runs f on an entry under the lock and saves it.
func change(ctx *spec.Context, verb string, f func(l *ledger.Ledger, e *ledger.Entry) error) (any, error) {
	_, l, err := Open(ctx)
	if err != nil {
		return nil, err
	}
	var out *ledger.Entry
	err = l.WriteAs(func() (string, error) {
		e, err := l.Get(ctx.Str("id"))
		if err != nil {
			return "", err
		}
		if err := f(l, e); err != nil {
			return "", err
		}
		if err := l.Save(e); err != nil {
			return "", err
		}
		out = e
		return fmt.Sprintf("%s %s: %s", verb, e.ID, e.Title), nil
	})
	return out, err
}

func registerEntries() {
	entryEffects := []string{"Writes the entry's file in the sphere's ledger and commits it."}
	spec.Register(&spec.Action{
		Category: "due", Name: "add", Top: true,
		Summary: "Add a date not to miss to the ledger (contract or signature deadline, notice period): notices before it and what fires at the term. Something to do goes to task instead.",
		Discussion: "The ledger holds dates that must not be missed and call for no action by themselves: a contract or signature deadline, a notice period, a warranty end. Anything to do is a task (task add), not an entry; a deadline that calls for an action keeps its date here and its action in task, linked by --ref task:<id>. A date already in a calendar, office or a routine stays there and is not added. A date alone (2026-11-15) fires at the default time (09:00). Each notice tells at its delay before the term; " +
			"the term runs the action: tell (a message to Alain), agent (the body as a prompt), command (--run in a shell). " +
			"Without --do, the entry is only listed and its notices still tell. The launcher (due launcher install) fires them.",
		Params: []spec.Param{
			{Name: "title", Kind: spec.String, Positional: true, Required: true, Help: "What falls due."},
			{Name: "at", Kind: spec.String, Required: true, Help: "Date: 2026-11-15, 2026-11-15 14:00, 15.11.2026, tomorrow, 3d."},
			{Name: "notice", Kind: spec.StringList, Help: "Delays before the term, repeatable or comma-separated: 7d,1d,2h."},
			{Name: "do", Kind: spec.String, Enum: ledger.Dos, Help: "Action at the term."},
			{Name: "via", Kind: spec.String, Enum: ledger.Vias, Help: "Channel of the notices and of a tell: mail, tell or push. Default: mail for a notice a week ahead or more, tell closer, push for a critical term."},
			{Name: "run", Kind: spec.String, Help: "Shell command of --do command."},
			{Name: "cwd", Kind: spec.String, Help: "Directory of the action. Defaults to the home directory."},
			{Name: "ref", Kind: spec.String, Help: "What the entry belongs to, e.g. office:P-0040."},
			{Name: "body", Kind: spec.String, Help: "Message or prompt; - reads stdin."},
			writeSphereParam(),
		},
		Effects: entryEffects,
		Examples: []string{
			`due add "Fin du contrat de maintenance" --at 2027-03-31 --notice 60d,14d --ref task:UT-0007 --sphere pro`,
			`due add "Expiration du passeport" --at 2026-12-01 --notice 30d,7d --sphere perso`,
			`due add "Délai de résiliation de l'abonnement" --at 15.11.2026 --notice 7d,1d --do tell --sphere perso`,
			`due add "Fin du délai de réponse de la gérance" --at 2026-10-20 --do agent --ref office:P-0007 --body "Le délai est échu : dis à Alain si la réponse est arrivée." --sphere perso`,
		},
		Run: func(ctx *spec.Context) (any, error) {
			_, l, err := Open(ctx)
			if err != nil {
				return nil, err
			}
			now := Now()
			m, err := when.ParseMoment(ctx.Str("at"), now)
			if err != nil {
				return nil, spec.UserError("--at: %v", err)
			}
			ns, err := notices(ctx)
			if err != nil {
				return nil, err
			}
			if err := checkAction(ctx.Str("do"), ctx.Str("run")); err != nil {
				return nil, err
			}
			b, err := body(ctx)
			if err != nil {
				return nil, err
			}
			title := strings.TrimSpace(ctx.Str("title"))
			if title == "" {
				return nil, spec.UserError("the title is empty")
			}
			e := &ledger.Entry{Title: title, At: m.String(), Notice: ns, Do: ctx.Str("do"), Via: ctx.Str("via"), Run: ctx.Str("run"),
				Cwd: ctx.Str("cwd"), Ref: ctx.Str("ref"), State: ledger.Open, Created: now.Format(time.RFC3339), Body: b}
			err = l.WriteAs(func() (string, error) {
				e.ID = l.NextID()
				l.Note(e, "created")
				if err := l.Save(e); err != nil {
					return "", err
				}
				return fmt.Sprintf("add %s: %s", e.ID, e.Title), nil
			})
			if err != nil {
				return nil, err
			}
			return e, nil
		},
		Text: textEntry,
	})
	spec.Register(&spec.Action{
		Category: "due", Name: "show", Top: true,
		Summary:    "Show an entry of the ledger: fields, notices to come, firings, history.",
		Discussion: "The id's prefix names its sphere (PE-0007 perso, UE-0007 pro); --sphere is needed only for a bare number.",
		Params:     []spec.Param{idParam(), readSphereParam()},
		Examples:   []string{"due show PE-0007", "due show 7 --sphere perso"},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, err := config.Load(ctx.Config)
			if err != nil {
				return nil, err
			}
			sphere, ok := cfg.SphereOfID(ctx.Str("id"))
			if !ok {
				spheres, err := ReadSpheres(ctx, cfg)
				if err != nil {
					return nil, err
				}
				if len(spheres) != 1 {
					return nil, spec.UserError("%s names no sphere: give its full id (e.g. PE-0007) or --sphere", ctx.Str("id"))
				}
				sphere = spheres[0]
			}
			if err := checkSphere(ctx, cfg, sphere); err != nil {
				return nil, err
			}
			l, err := OpenLedger(ctx, cfg, sphere)
			if err != nil {
				return nil, err
			}
			e, err := l.Get(ctx.Str("id"))
			if err != nil {
				return nil, err
			}
			return DetailOf(l, e), nil
		},
		Text: textDetail,
	})
	spec.Register(&spec.Action{
		Category: "due", Name: "edit", Top: true,
		Summary:    "Change an entry: title, date, notices, action, body.",
		Discussion: "Only the options given change. --notice replaces the notices (--notice none clears them); --do none removes the action. A new date makes its notices and term fire again.",
		Params: []spec.Param{
			idParam(),
			{Name: "title", Kind: spec.String, Help: "New title."},
			{Name: "at", Kind: spec.String, Help: "New date."},
			{Name: "notice", Kind: spec.StringList, Help: "New notices, or none."},
			{Name: "do", Kind: spec.String, Enum: append([]string{"none"}, ledger.Dos...), Help: "New action, or none."},
			{Name: "via", Kind: spec.String, Enum: append([]string{"auto"}, ledger.Vias...), Help: "New channel, or auto for the default by attention."},
			{Name: "run", Kind: spec.String, Help: "New command of --do command."},
			{Name: "cwd", Kind: spec.String, Help: "New directory of the action."},
			{Name: "ref", Kind: spec.String, Help: "New ref; empty clears it."},
			{Name: "body", Kind: spec.String, Help: "New message or prompt; - reads stdin."},
			writeSphereParam(),
		},
		Effects:  entryEffects,
		Examples: []string{"due edit 7 --at 2026-11-20 --sphere perso", "due edit PE-0007 --notice 14d,2d --do tell --sphere perso"},
		Run: func(ctx *spec.Context) (any, error) {
			return change(ctx, "edit", func(l *ledger.Ledger, e *ledger.Entry) error {
				var what []string
				if _, ok := ctx.Args["title"]; ok {
					e.Title = strings.TrimSpace(ctx.Str("title"))
					what = append(what, "title")
				}
				if _, ok := ctx.Args["at"]; ok {
					m, err := when.ParseMoment(ctx.Str("at"), l.Now())
					if err != nil {
						return spec.UserError("--at: %v", err)
					}
					what = append(what, "at "+e.At+" → "+m.String())
					e.At = m.String()
				}
				if _, ok := ctx.Args["notice"]; ok {
					if l := ctx.List("notice"); len(l) == 1 && l[0] == "none" {
						e.Notice = nil
					} else {
						ns, err := notices(ctx)
						if err != nil {
							return err
						}
						e.Notice = ns
					}
					what = append(what, "notice "+strings.Join(e.Notice, ","))
				}
				if _, ok := ctx.Args["do"]; ok {
					e.Do = ctx.Str("do")
					if e.Do == "none" {
						e.Do, e.Run = "", ""
					}
					what = append(what, "do "+ctx.Str("do"))
				}
				if _, ok := ctx.Args["via"]; ok {
					e.Via = ctx.Str("via")
					if e.Via == "auto" {
						e.Via = ""
					}
					what = append(what, "via "+ctx.Str("via"))
				}
				if _, ok := ctx.Args["run"]; ok {
					e.Run = ctx.Str("run")
					what = append(what, "run")
				}
				if err := checkAction(e.Do, e.Run); err != nil {
					return err
				}
				if _, ok := ctx.Args["cwd"]; ok {
					e.Cwd = ctx.Str("cwd")
					what = append(what, "cwd")
				}
				if _, ok := ctx.Args["ref"]; ok {
					e.Ref = ctx.Str("ref")
					what = append(what, "ref")
				}
				if _, ok := ctx.Args["body"]; ok {
					b, err := body(ctx)
					if err != nil {
						return err
					}
					e.Body = b
					what = append(what, "body")
				}
				if len(what) == 0 {
					return spec.UserError("nothing to change. Example: due edit %s --at 2026-11-20", e.ID)
				}
				l.Note(e, "edited: "+strings.Join(what, "; "))
				return nil
			})
		},
		Text: textEntry,
	})
	spec.Register(&spec.Action{
		Category: "due", Name: "snooze", Top: true,
		Summary: "Move an entry later: to a date, or by a delay from its current date.",
		Params: []spec.Param{
			idParam(),
			{Name: "to", Kind: spec.String, Help: "New date, e.g. 2026-11-20 or 3d (from today)."},
			{Name: "by", Kind: spec.String, Help: "Delay added to the current date, e.g. 7d."},
			writeSphereParam(),
		},
		Effects:  entryEffects,
		Examples: []string{"due snooze 7 --by 7d --sphere perso", "due snooze 7 --to 2026-11-20 --sphere perso"},
		Run: func(ctx *spec.Context) (any, error) {
			return change(ctx, "snooze", func(l *ledger.Ledger, e *ledger.Entry) error {
				var m when.Moment
				switch {
				case ctx.Str("to") != "" && ctx.Str("by") == "":
					var err error
					if m, err = when.ParseMoment(ctx.Str("to"), l.Now()); err != nil {
						return spec.UserError("--to: %v", err)
					}
				case ctx.Str("by") != "" && ctx.Str("to") == "":
					d, err := when.ParseDuration(ctx.Str("by"))
					if err != nil {
						return spec.UserError("--by: %v", err)
					}
					m = e.Moment()
					if d%(24*time.Hour) == 0 {
						t, _ := time.Parse("2006-01-02", m.Date)
						m.Date = t.AddDate(0, 0, int(d/(24*time.Hour))).Format("2006-01-02")
					} else {
						t := m.Time(l.Loc(), l.Clock).Add(d)
						m = when.Moment{Date: t.Format("2006-01-02"), Clock: t.Format("15:04")}
					}
				default:
					return spec.UserError("give --to or --by. Example: due snooze %s --by 7d", e.ID)
				}
				l.Note(e, "snoozed: "+e.At+" → "+m.String())
				e.At = m.String()
				if e.State != ledger.Open {
					e.State = ledger.Open
				}
				return nil
			})
		},
		Text: textEntry,
	})
	for _, s := range []struct{ name, state, summary string }{
		{"done", ledger.Done, "Mark an entry done: it stops firing and leaves the list."},
		{"drop", ledger.Dropped, "Drop an entry without doing it: it stops firing and leaves the list."},
		{"reopen", ledger.Open, "Open a done or dropped entry again."},
	} {
		spec.Register(&spec.Action{
			Category: "due", Name: s.name, Top: true, Summary: s.summary,
			Params:   []spec.Param{idParam(), {Name: "note", Kind: spec.String, Help: "What happened, kept in the history."}, writeSphereParam()},
			Effects:  entryEffects,
			Examples: []string{fmt.Sprintf("due %s 7 --sphere perso", s.name)},
			Run: func(ctx *spec.Context) (any, error) {
				return change(ctx, s.name, func(l *ledger.Ledger, e *ledger.Entry) error {
					if e.State == s.state {
						return spec.Conflict("%s is already %s", e.ID, s.state)
					}
					e.State = s.state
					what := s.state
					if n := strings.TrimSpace(ctx.Str("note")); n != "" {
						what += ": " + n
					}
					l.Note(e, what)
					return nil
				})
			},
			Text: textEntry,
		})
	}
	spec.Register(&spec.Action{
		Category: "due", Name: "rm", Top: true, Destructive: true,
		Summary:  "Delete an entry's file. Prefer done or drop, which keep its history.",
		Params:   []spec.Param{idParam(), writeSphereParam()},
		Effects:  []string{"Deletes the entry's file and commits; the VCS keeps the old version."},
		Examples: []string{"due rm 7 --sphere perso"},
		Run: func(ctx *spec.Context) (any, error) {
			_, l, err := Open(ctx)
			if err != nil {
				return nil, err
			}
			var id string
			err = l.WriteAs(func() (string, error) {
				e, err := l.Get(ctx.Str("id"))
				if err != nil {
					return "", err
				}
				id = e.ID
				return fmt.Sprintf("rm %s: %s", e.ID, e.Title), l.Remove(e.ID)
			})
			if err != nil {
				return nil, err
			}
			return map[string]string{"removed": id}, nil
		},
	})
}

// Detail is an entry with its instants to come.
type Detail struct {
	*ledger.Entry
	Sphere   string     `json:"sphere"`
	Term     time.Time  `json:"term"`
	Upcoming []Upcoming `json:"upcoming"`
}

type Upcoming struct {
	Kind string    `json:"kind"`
	When time.Time `json:"when"`
}

// DetailOf is an entry with its instants to come.
func DetailOf(l *ledger.Ledger, e *ledger.Entry) Detail {
	d := Detail{Entry: e, Sphere: l.Sphere, Term: e.Moment().Time(l.Loc(), l.Clock), Upcoming: []Upcoming{}}
	if e.State == ledger.Open {
		now := l.Now()
		for _, i := range e.Instants(l.Loc(), l.Clock) {
			if i.When.After(now) && !e.HasFired(i) && (i.Kind != "term" || e.Do != "") {
				d.Upcoming = append(d.Upcoming, Upcoming{i.Kind, i.When})
			}
		}
	}
	return d
}

func describeAt(e *ledger.Entry) string {
	m := e.Moment()
	t, _ := time.Parse("2006-01-02", m.Date)
	s := when.Day(t)
	if !m.AllDay {
		s += " " + m.Clock
	}
	return s
}

func textEntry(w io.Writer, v any) {
	e, ok := v.(*ledger.Entry)
	if !ok {
		return
	}
	fmt.Fprintf(w, "%s  %s  %s  [%s]\n", e.ID, describeAt(e), e.Title, e.State)
}

func textDetail(w io.Writer, v any) {
	d, ok := v.(Detail)
	if !ok {
		return
	}
	e := d.Entry
	fmt.Fprintf(w, "%s  %s\n", e.ID, e.Title)
	fmt.Fprintf(w, "  date     %s (%s)\n", describeAt(e), when.Until(d.Term, Now()))
	fmt.Fprintf(w, "  state    %s\n", e.State)
	if len(e.Notice) > 0 {
		fmt.Fprintf(w, "  notice   %s\n", strings.Join(e.Notice, ", "))
	}
	if e.Do != "" {
		act := e.Do
		if e.Run != "" {
			act += ": " + e.Run
		}
		fmt.Fprintf(w, "  do       %s\n", act)
	}
	if e.Cwd != "" {
		fmt.Fprintf(w, "  cwd      %s\n", e.Cwd)
	}
	if e.Ref != "" {
		fmt.Fprintf(w, "  ref      %s\n", e.Ref)
	}
	for _, u := range d.Upcoming {
		fmt.Fprintf(w, "  next     %s %s (%s)\n", when.Day(u.When), u.When.Format("15:04"), u.Kind)
	}
	if e.Body != "" {
		fmt.Fprintf(w, "\n%s\n", e.Body)
	}
	if len(e.Fired) > 0 {
		fmt.Fprintln(w, "\nFired:")
		for _, f := range e.Fired {
			fmt.Fprintf(w, "  %s  %-12s %s\n", f.Instant, f.Kind, f.Result)
		}
	}
	if len(e.Log) > 0 {
		fmt.Fprintln(w, "\nHistory:")
		for _, h := range e.Log {
			fmt.Fprintf(w, "  %s  %-10s %s\n", h.At, h.By, h.What)
		}
	}
}
