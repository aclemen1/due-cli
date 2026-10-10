package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aclemen1/tuikit"
	"github.com/aclemen1/tuikit/complete"

	"github.com/aclemen1/due-cli/internal/config"

	"github.com/aclemen1/due-cli/internal/actions"
	"github.com/aclemen1/due-cli/internal/connect"
)

// Every input but the / filter is a tuikit modal: the entry form (c, E), the
// snooze (z) and the confirmations (x, R, #).

var (
	doLabels = []string{"aucune", "message", "agent", "commande"}
	doValues = map[string]string{"aucune": "", "message": "tell", "agent": "agent", "commande": "command"}
	viaLabel = []string{"auto", "mail", "tell", "push"}
)

func doLabel(do string) string {
	for l, v := range doValues {
		if v == do {
			return l
		}
	}
	return "aucune"
}

func (m *model) openModal(title string, c tuikit.Content) {
	m.modal = tuikit.NewModal(title, c).SetSize(m.w, m.h)
}

// openEntryForm adds an entry, or edits one when edit is set.
func (m *model) openEntryForm(edit *actions.Detail) {
	var fields []*tuikit.Field
	if edit == nil && m.multi() {
		fields = append(fields, tuikit.Choice("sphere", "Sphère", m.spheres...).Required().
			Help("selon la nature de l'échéance : pro pour le travail, perso sinon ; jamais par défaut"))
	}
	title := tuikit.Text("title", "Titre").Required().Help("une date à ne pas rater ; ce qui est à faire va dans task")
	at := tuikit.Date("at", "Date").Clock(m.cfg.DefaultTime).Required().Help("une date seule prend " + m.cfg.DefaultTime)
	notice := tuikit.Durations("notice", "Préavis").From("at").Help("délais avant le terme, p. ex. 30d, 7d, 1d")
	do := tuikit.Choice("do", "Au terme", doLabels...).Help("message : vous écrire ; agent : relancer le dossier de la réf. ; commande : lancer une commande")
	via := tuikit.Choice("via", "Canal", viaLabel...).Help("auto : e-mail si 7 jours ou plus, Telegram plus près, Pushover si critique")
	run := tuikit.TextArea("run", "Commande").ShowIf("do", "commande")
	refs := tuikit.Refs("refs", "Réfs", m.refs).Help("ce que l'échéance cite : office:P-…, task:PT-…, contact:… ; entrée ou virgule ajoute")
	body := tuikit.TextArea("body", "Message ou prompt")
	label := "Nouvelle échéance"
	if edit != nil {
		e := edit.Entry
		label = "Modifier " + e.ID
		title.Default(e.Title)
		at.Default(e.At)
		notice.Default(strings.Join(e.Notice, ","))
		do.Default(doLabel(e.Do))
		if e.Via != "" {
			via.Default(e.Via)
		}
		run.Default(e.Run)
		refs.Default(strings.Join(e.Refs, ","))
		body.Default(e.Body)
	}
	fields = append(fields, title, at, notice, do, via, run, refs, body)
	m.editing = edit
	m.openModal(label, tuikit.NewForm("entry", fields...).Now(m.now))
}

// durations writes delays the way due reads them: 30d, 2h, 45min.
func durations(ds []time.Duration) string {
	var out []string
	for _, d := range ds {
		switch {
		case d%(24*time.Hour) == 0:
			out = append(out, fmt.Sprintf("%dd", int(d/(24*time.Hour))))
		case d%time.Hour == 0:
			out = append(out, fmt.Sprintf("%dh", int(d/time.Hour)))
		default:
			out = append(out, fmt.Sprintf("%dmin", int(d/time.Minute)))
		}
	}
	return strings.Join(out, ",")
}

// refs completes a reference from the sources of the configuration: those
// shared by the TUIs, then due's own.
func (m *model) refs(q string) []tuikit.Item {
	seen := map[string]bool{}
	var out []tuikit.Item
	for _, c := range m.completers {
		for _, it := range c.Complete(q) {
			if !seen[it.Value] {
				seen[it.Value] = true
				out = append(out, it)
			}
		}
	}
	if len(out) > 30 {
		out = out[:30]
	}
	return out
}

// newCompleters starts the sources of the configuration in the background and
// names those missing from the shared file.
func newCompleters(cfg *config.Config) ([]*complete.Completer, []string) {
	var out []*complete.Completer
	var missing []string
	names := append(append([]string{}, cfg.Complete["refs"]...), cfg.Refs.Sources...)
	if len(names) > 0 {
		// A source missing from the shared file leaves the others working.
		var uses complete.Uses
		for _, n := range names {
			uses = append(uses, complete.Use{Source: n})
		}
		c, absent := complete.FromConfig(uses)
		if c != nil {
			out = append(out, c)
		}
		missing = absent
	}
	var own []complete.Source
	for _, s := range cfg.Refs.Complete {
		own = append(own, complete.Source{Run: s.Run, Prefix: s.Prefix, Value: s.Value, Label: s.Label})
	}
	if len(own) > 0 {
		out = append(out, complete.New(own...))
	}
	return out, missing
}

// done reads the answer of a modal.
func (m *model) done(msg tuikit.DoneMsg) tea.Cmd {
	v := msg.Values
	it := m.target
	switch msg.ID {
	case "entry":
		args := map[string]any{"title": v.String("title"), "at": v.String("at"), "ref": v.Strings("refs"), "body": v.String("body")}
		do := doValues[v.String("do")]
		if do == "command" {
			args["run"] = v.String("run")
		}
		ns := durations(v.Durations("notice"))
		if m.editing == nil {
			args["sphere"] = v.String("sphere")
			if !m.multi() {
				args["sphere"] = m.spheres[0]
			}
			if ns != "" {
				args["notice"] = []string{ns}
			}
			if do != "" {
				args["do"] = do
			}
			if via := v.String("via"); via != "" && via != "auto" {
				args["via"] = via
			}
			return m.act("add", args, "échéance ajoutée")
		}
		e := m.editing.Entry
		args["id"], args["sphere"] = e.ID, m.editing.Sphere
		if ns == "" {
			ns = "none"
		}
		if len(v.Strings("refs")) == 0 {
			args["ref"] = []string{"none"}
		}
		args["notice"] = []string{ns}
		args["do"] = do
		if do == "" {
			args["do"] = "none"
		} else if do != "command" {
			args["run"] = ""
		}
		if via := v.String("via"); via != "" {
			args["via"] = via
		}
		return m.act("edit", args, e.ID+" modifiée")
	case "snooze":
		return m.act("snooze", map[string]any{"id": it.ID, "sphere": it.Sphere, "to": v.String("to")}, it.ID+" reportée")
	case "drop":
		return m.act("drop", map[string]any{"id": it.ID, "sphere": it.Sphere}, it.ID+" abandonnée")
	case "run":
		return m.act("run", map[string]any{"id": it.ID, "sphere": it.Sphere}, it.ID+" exécutée")
	case "rm":
		return m.act("rm", map[string]any{"id": it.ID, "sphere": it.Sphere}, it.ID+" supprimée")
	case "note":
		text, cfg := v.String("note"), m.cfg
		if text == "" {
			return nil
		}
		return func() tea.Msg {
			if err := actions.AddNote(cfg, "due:"+it.ID, it.Sphere, text); err != nil {
				return doneMsg{err: err}
			}
			return doneMsg{status: "note ajoutée à " + it.ID}
		}
	}
	return nil
}

func (m *model) openSnooze(it connect.Item) {
	m.target = it
	f := tuikit.NewForm("snooze", tuikit.Date("to", "Reporter au").Clock(m.cfg.DefaultTime).Required().
		Help("une date, ou un délai : +7j, 2sem, vendredi prochain")).Now(m.now)
	m.openModal("Reporter « "+trunc(it.Title, 40)+" »", f)
}

func (m *model) openConfirm(id string, it connect.Item, question string) {
	m.target = it
	m.openModal("Confirmer", tuikit.NewConfirm(id, question))
}

func (m *model) openConfirmRm(it connect.Item) {
	m.target = it
	m.openModal("Supprimer", tuikit.NewConfirmTyped("rm", "Supprimer définitivement « "+it.Title+" » ? Recopiez son id.", it.ID))
}
