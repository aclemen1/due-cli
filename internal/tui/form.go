package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/aclemen1/due-cli/internal/actions"
	"github.com/aclemen1/due-cli/internal/when"
)

var (
	doValues = []string{"", "tell", "agent", "command"}
	doLabels = []string{"aucune", "message", "agent", "commande"}
)

// form adds an entry, or edits one when edit is set.
type form struct {
	m      *model
	edit   *actions.Detail
	fields map[string]*textinput.Model
	focusI int
	do     int
	err    string
	w      int
}

func newForm(m *model, edit *actions.Detail) *form {
	f := &form{m: m, edit: edit, fields: map[string]*textinput.Model{}}
	place := map[string]string{
		"title": "ce qui arrive à échéance", "at": "15.11.2026, 2026-11-15 14:00, demain, 3w",
		"notice": "30d,7d,1d (facultatif)", "run": "commande shell", "ref": "office:P-0040 (facultatif)",
		"body": "message ou prompt (facultatif)",
	}
	for name, ph := range place {
		in := textinput.New()
		in.Prompt = ""
		in.Placeholder = ph
		f.fields[name] = &in
	}
	if edit != nil {
		e := edit.Entry
		at := e.Moment()
		t, _ := time.Parse("2006-01-02", at.Date)
		v := t.Format("02.01.2006")
		if !at.AllDay {
			v += " " + at.Clock
		}
		f.fields["title"].SetValue(e.Title)
		f.fields["at"].SetValue(v)
		f.fields["notice"].SetValue(strings.Join(e.Notice, ","))
		f.fields["run"].SetValue(e.Run)
		f.fields["ref"].SetValue(e.Ref)
		f.fields["body"].SetValue(e.Body)
		for i, d := range doValues {
			if d == e.Do {
				f.do = i
			}
		}
	}
	f.resize(m.w)
	return f
}

func (f *form) order() []string {
	o := []string{"title", "at", "notice", "do"}
	if doValues[f.do] == "command" {
		o = append(o, "run")
	}
	return append(o, "ref", "body")
}

func (f *form) current() string { return f.order()[f.focusI] }

func (f *form) resize(w int) {
	f.w = min(max(40, w-4), 84)
	for _, in := range f.fields {
		in.SetWidth(f.w - 16)
	}
}

func (f *form) focus() tea.Cmd {
	for name, in := range f.fields {
		if name == f.current() {
			in.CursorEnd()
			defer in.Focus()
		} else {
			in.Blur()
		}
	}
	return textinput.Blink
}

func (f *form) move(step int) tea.Cmd {
	n := len(f.order())
	f.focusI = (f.focusI + step + n) % n
	return f.focus()
}

func (m *model) keyForm(k tea.KeyPressMsg) tea.Cmd {
	f := m.form
	switch k.String() {
	case "esc":
		m.form = nil
		return nil
	case "tab", "down":
		return f.move(1)
	case "shift+tab", "up":
		return f.move(-1)
	case "ctrl+s":
		return f.save()
	case "enter":
		if f.focusI == len(f.order())-1 {
			return f.save()
		}
		return f.move(1)
	}
	if f.current() == "do" {
		switch k.String() {
		case "left", "h":
			f.do = (f.do + len(doValues) - 1) % len(doValues)
		case "right", "l", "space":
			f.do = (f.do + 1) % len(doValues)
		}
		return nil
	}
	in := f.fields[f.current()]
	updated, cmd := in.Update(k)
	*in = updated
	f.err = ""
	return cmd
}

func (f *form) val(name string) string { return strings.TrimSpace(f.fields[name].Value()) }

// notices reads the notice field, or says what is wrong.
func (f *form) notices() ([]string, error) {
	var out []string
	for _, n := range strings.Split(f.val("notice"), ",") {
		if n = strings.TrimSpace(n); n == "" {
			continue
		}
		if _, err := when.ParseDuration(n); err != nil {
			return nil, fmt.Errorf("préavis %q : un nombre suivi de min, h, d ou w, p. ex. 7d", n)
		}
		out = append(out, n)
	}
	return out, nil
}

func (f *form) save() tea.Cmd {
	title := f.val("title")
	if title == "" {
		f.err, f.focusI = "il faut un titre", 0
		return f.focus()
	}
	if _, err := when.ParseMoment(f.val("at"), f.m.now()); err != nil {
		f.err, f.focusI = "date illisible : 15.11.2026, 2026-11-15 14:00, demain ou 3w", 1
		return f.focus()
	}
	ns, err := f.notices()
	if err != nil {
		f.err, f.focusI = err.Error(), 2
		return f.focus()
	}
	do := doValues[f.do]
	if do == "command" && f.val("run") == "" {
		f.err = "une action « commande » demande la commande à lancer"
		f.focusI = 4
		return f.focus()
	}
	args := map[string]any{"title": title, "at": f.val("at"), "ref": f.val("ref"), "body": f.val("body")}
	if do == "command" {
		args["run"] = f.val("run")
	}
	m := f.m
	m.form = nil
	if f.edit == nil {
		if len(ns) > 0 {
			args["notice"] = []string{strings.Join(ns, ",")}
		}
		if do != "" {
			args["do"] = do
		}
		return m.act("add", args, "échéance ajoutée")
	}
	args["id"] = f.edit.ID
	if len(ns) == 0 {
		args["notice"] = []string{"none"}
	} else {
		args["notice"] = []string{strings.Join(ns, ",")}
	}
	if do == "" {
		args["do"] = "none"
	} else {
		args["do"] = do
		if do != "command" {
			args["run"] = ""
		}
	}
	return m.act("edit", args, f.edit.ID+" modifiée")
}

func (f *form) footer() string {
	if f.err != "" {
		return sErr.Render("✗ " + f.err)
	}
	if f.current() == "do" {
		return helpLine("← →", "choisir l'action", "tab", "champ suivant", "ctrl+s", "enregistrer", "esc", "annuler")
	}
	return helpLine("tab ↓", "champ suivant", "↑", "précédent", "↵", "suivant (enregistre au dernier)", "ctrl+s", "enregistrer", "esc", "annuler")
}

// preview says how the date and notices are understood.
func (f *form) preview(name string) string {
	now := f.m.now()
	switch name {
	case "at":
		if f.val("at") == "" {
			return ""
		}
		m, err := when.ParseMoment(f.val("at"), now)
		if err != nil {
			return sErr.Render("date illisible")
		}
		t := m.Time(now.Location(), f.m.cfg.DefaultTime)
		s := when.Day(t)
		if m.AllDay {
			s += " (" + f.m.cfg.DefaultTime + ")"
		} else {
			s += " " + t.Format("15:04")
		}
		return urgency(t, now, false).Render("→ " + s + " · " + when.Until(t, now))
	case "notice":
		ns, err := f.notices()
		if err != nil {
			return sErr.Render(err.Error())
		}
		m, err := when.ParseMoment(f.val("at"), now)
		if len(ns) == 0 || err != nil {
			return ""
		}
		term := m.Time(now.Location(), f.m.cfg.DefaultTime)
		var days []string
		for _, n := range ns {
			t, _ := when.Before(term, n)
			s := t.Format("02.01 15:04")
			if t.Before(now) {
				s += " (passé)"
			}
			days = append(days, s)
		}
		return sMuted.Render("→ message les " + strings.Join(days, " · "))
	case "ref":
		if strings.HasPrefix(f.val("ref"), "office:") {
			return sMuted.Render("→ messages et prompt vont à ce dossier")
		}
		return sMuted.Render("→ sans dossier : messages et prompt vont au desk")
	case "body":
		switch doValues[f.do] {
		case "tell":
			return sMuted.Render("→ ajouté au message du terme")
		case "agent":
			return sMuted.Render("→ prompt envoyé à la session du dossier au terme")
		}
	}
	return ""
}

func (f *form) view(w, h int) []string {
	labels := map[string]string{"title": "Titre", "at": "Date", "notice": "Préavis", "do": "Au terme",
		"run": "Commande", "ref": "Dossier", "body": "Message"}
	if doValues[f.do] == "agent" {
		labels["body"] = "Prompt"
	}
	var lines []string
	for i, name := range f.order() {
		label := fmt.Sprintf("%-10s", labels[name])
		ls := sMuted
		if i == f.focusI {
			ls = sKey
		}
		var value string
		if name == "do" {
			var opts []string
			for j, l := range doLabels {
				if j == f.do {
					opts = append(opts, lipgloss.NewStyle().Bold(true).Foreground(cText).Background(cSel).Render(" "+l+" "))
				} else {
					opts = append(opts, sMuted.Render(" "+l+" "))
				}
			}
			value = strings.Join(opts, " ")
		} else {
			value = f.fields[name].View()
		}
		lines = append(lines, ls.Render(label)+"  "+value)
		if p := f.preview(name); p != "" {
			lines = append(lines, strings.Repeat(" ", 12)+p)
		}
		lines = append(lines, "")
	}
	title := " Nouvelle échéance "
	if f.edit != nil {
		title = " Modifier " + f.edit.ID + " "
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).Padding(0, 2).Width(f.w).
		Render(strings.Join(lines, "\n"))
	left := max(0, (w-f.w)/2)
	out := []string{"", strings.Repeat(" ", left+1) + sTitle.Render(title)}
	for _, l := range strings.Split(box, "\n") {
		out = append(out, strings.Repeat(" ", left)+l)
	}
	return out
}
