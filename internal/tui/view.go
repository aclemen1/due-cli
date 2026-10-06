package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/aclemen1/due-cli/internal/connect"
	"github.com/aclemen1/due-cli/internal/when"
)

func (m *model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m *model) render() string {
	var head string
	if m.view == vDetail {
		head = sTitle.Render("due") + sMuted.Render(" · "+m.sphere+" · ") + sBold.Render(m.detailItem.Title)
	} else if m.ledgerOnly() {
		kind := "ouvertes"
		if m.showDone {
			kind = "toutes, faites comprises"
		}
		head = sTitle.Render("due") + sMuted.Render(" · "+m.sphere+" · ") + sBold.Render("registre") +
			sMuted.Render(fmt.Sprintf(" · %d échéances %s", len(m.items), kind))
	} else {
		head = sTitle.Render("due") + sMuted.Render(" · "+m.sphere+" · ") + sBold.Render(m.sourceName()) + sMuted.Render(" · jusqu'au ")
		if m.listing != nil {
			head += sText.Render(when.Day(m.listing.Until))
		}
		head += sMuted.Render(fmt.Sprintf(" (%s) · %d lignes", horizons[m.horizon], len(m.items)))
	}
	if m.view != vDetail {
		if m.filter != "" {
			head += sMuted.Render(" · filtre : ") + sWarn.Render(m.filter)
		}
		if m.loading {
			head += sWarn.Render(" · chargement…")
		}
	}
	lines := []string{pad(head, m.w), ""}
	body := m.bodyHeight()
	if m.view == vDetail {
		lines = append(lines, m.detailLines(body)...)
	} else {
		lines = append(lines, m.listLines(body)...)
	}
	for len(lines) < body+2 {
		lines = append(lines, "")
	}
	if m.helpOn {
		lines = append(lines, "")
		lines = append(lines, m.helpLines()...)
	}
	lines = append(lines, m.footer())
	return block(lines, m.w, m.h)
}

func (m *model) footer() string {
	if m.prompt != pNone {
		label := map[prompt]string{pFilter: "chercher", pAddTitle: "nouvelle échéance", pAddAt: "date", pAddNotice: "préavis",
			pSnooze: "reporter", pConfirmDrop: "abandonner ?", pConfirmRun: "exécuter ?", pConfirmRm: "supprimer ?"}[m.prompt]
		return sKey.Render(label+" › ") + m.input.View()
	}
	if m.status != "" {
		if m.statusErr {
			return sErr.Render(m.status)
		}
		return sOK.Render(m.status)
	}
	if m.view == vDetail {
		if m.detailItem.Type == "due" {
			return helpLine("d", "faite", "z", "reporter", "e", "éditer", "R", "exécuter", "x", "abandonner", "esc", "retour", "q", "quitter")
		}
		return helpLine("esc", "retour", "q", "quitter")
	}
	if m.ledgerOnly() {
		return helpLine("↵", "détail", "a", "ajouter", "d", "faite", "z", "reporter", "f", "faites", "/", "chercher", "s", "autres sources", "?", "aide", "q", "quitter")
	}
	return helpLine("↵", "détail", "a", "ajouter", "/", "chercher", "s", "source", "h", "horizon", "esc", "registre", "?", "aide", "q", "quitter")
}

func (m *model) helpLines() []string {
	out := []string{
		sSection.Render("Aide"),
		helpLine("↑↓ j k", "se déplacer", "g G", "début, fin", "↵", "détail", "esc", "retour, efface le filtre, revient au registre"),
		helpLine("a", "ajouter au registre", "d", "faite / rouvrir", "z", "reporter", "e", "éditer le fichier", "x", "abandonner", "D", "supprimer"),
		helpLine("R", "exécuter l'action", "f", "faites aussi (registre)", "/", "chercher", "s", "registre, toutes, chaque connecteur", "h", "horizon", "r", "relire"),
		sMuted.Render("Le registre garde ce qu'aucun autre outil ne porte. Les autres lignes se lisent ici et se modifient dans leur outil. ! = en retard."),
	}
	if m.listing != nil {
		for _, e := range m.listing.Errors {
			out = append(out, sErr.Render(e.Source+" : ")+sMuted.Render(e.Error))
		}
	}
	return out
}

// listLines renders the list with a header per day, keeping the selection visible.
func (m *model) listLines(h int) []string {
	if len(m.items) == 0 {
		if m.loading {
			return []string{sMuted.Render("  chargement…")}
		}
		if m.ledgerOnly() {
			return []string{sMuted.Render("  Le registre est vide : a pour ajouter, s pour voir les autres sources.")}
		}
		return []string{sMuted.Render("  Rien d'échu dans cette fenêtre.")}
	}
	var rows []string
	selRow := 0
	day := ""
	for i, it := range m.items {
		d := when.Day(it.At)
		if d != day {
			if day != "" {
				rows = append(rows, "")
			}
			rows = append(rows, sSection.Render(d)+sMuted.Render("  "+when.Until(it.At, m.now())))
			day = d
		}
		if i == m.sel {
			selRow = len(rows)
			rows = append(rows, selectLine(m.row(it), m.w))
		} else {
			rows = append(rows, m.row(it))
		}
	}
	if selRow < m.top {
		m.top = max(0, selRow-1)
	}
	if selRow >= m.top+h {
		m.top = selRow - h + 1
	}
	m.top = max(0, min(m.top, len(rows)-h))
	end := min(len(rows), m.top+h)
	return rows[m.top:end]
}

func (m *model) row(it connect.Item) string {
	clock := "     "
	if !it.AllDay {
		clock = it.At.Format("15:04")
	}
	mark := " "
	if it.Late {
		mark = sErr.Render("!")
	}
	src := sourceStyle(it).Render(fmt.Sprintf("%-10s", trunc(it.Source, 10)))
	title := sText.Render(it.Title)
	if it.Type == "due" {
		title = sBold.Render(it.ID) + " " + title
		if it.State != "" && it.State != "open" {
			title = sMuted.Render(it.ID + " " + it.Title)
		}
	}
	s := fmt.Sprintf(" %s %s  %s  %s", mark, sMuted.Render(clock), src, title)
	if it.Detail != "" {
		s += sMuted.Render("  · " + it.Detail)
	}
	return s
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func field(name, value string) string {
	return "  " + sMuted.Render(fmt.Sprintf("%-10s", name)) + sText.Render(value)
}

func (m *model) detailLines(h int) []string {
	it := m.detailItem
	var out []string
	if it.Type != "due" {
		day := when.Day(it.At)
		if !it.AllDay {
			day += " " + it.At.Format("15:04")
		}
		out = append(out, field("source", it.Source+" ("+it.Type+")"), field("date", day+" ("+when.Until(it.At, m.now())+")"), field("id", it.ID))
		if it.Detail != "" {
			out = append(out, field("détail", it.Detail))
		}
		if it.Ref != "" {
			out = append(out, field("ref", it.Ref))
		}
		out = append(out, "", sMuted.Render("  Cette ligne vient de "+it.Type+" : elle se modifie dans cet outil."))
		return scrollLines(out, m.scroll, h)
	}
	d := m.detail
	if d == nil {
		return []string{sMuted.Render("  chargement…")}
	}
	e := d.Entry
	day := when.Day(d.Term)
	if !e.Moment().AllDay {
		day += " " + d.Term.Format("15:04")
	}
	out = append(out, field("id", e.ID), field("date", day+" ("+when.Until(d.Term, m.now())+")"), field("état", e.State))
	if len(e.Notice) > 0 {
		out = append(out, field("préavis", strings.Join(e.Notice, ", ")))
	}
	if e.Do != "" {
		act := e.Do
		if e.Run != "" {
			act += " : " + e.Run
		}
		out = append(out, field("action", act))
	}
	if e.Cwd != "" {
		out = append(out, field("cwd", e.Cwd))
	}
	if e.Ref != "" {
		out = append(out, field("ref", e.Ref))
	}
	for _, u := range d.Upcoming {
		out = append(out, field("à venir", when.Day(u.When)+" "+u.When.Format("15:04")+" · "+u.Kind))
	}
	if e.Body != "" {
		out = append(out, "", sSection.Render("Message"))
		for _, l := range strings.Split(e.Body, "\n") {
			out = append(out, "  "+sText.Render(l))
		}
	}
	if len(e.Fired) > 0 {
		out = append(out, "", sSection.Render("Déclenchements"))
		for _, f := range e.Fired {
			st := sOK
			if strings.HasPrefix(f.Result, "failed") {
				st = sErr
			} else if f.Result == "skipped" || f.Result == "running" {
				st = sMuted
			}
			out = append(out, "  "+sMuted.Render(f.Instant)+"  "+sText.Render(fmt.Sprintf("%-18s", f.Kind))+st.Render(f.Result))
		}
	}
	if len(e.Log) > 0 {
		out = append(out, "", sSection.Render("Historique"))
		for _, l := range e.Log {
			out = append(out, "  "+sMuted.Render(l.At+"  "+l.By)+"  "+sText.Render(l.What))
		}
	}
	return scrollLines(out, m.scroll, h)
}

func scrollLines(lines []string, scroll, h int) []string {
	scroll = max(0, min(scroll, len(lines)-h))
	return lines[scroll:min(len(lines), scroll+h)]
}
