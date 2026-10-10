package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/aclemen1/due-cli/internal/connect"
	"github.com/aclemen1/due-cli/internal/judge"
	"github.com/aclemen1/due-cli/internal/ledger"
)

// column of the table: a fixed width, or the one that takes what is left.
type column struct {
	title string
	width int
	flex  bool
	// group: a value equal to the row above is left blank. The first group
	// column starts a group; the next ones are blanked only inside it.
	group bool
}

// cell is the plain text of a cell and its style.
type cell struct {
	text  string
	style lipgloss.Style
}

func (m *model) columns() []column {
	var cols []column
	if m.ledgerOnly() {
		cols = append(cols, column{title: "Période", group: true})
	}
	cols = append(cols,
		column{title: "Date", group: true},
		column{title: "Heure"},
	)
	if m.multi() {
		cols = append(cols, column{title: "Sph", group: true})
	}
	if m.ledgerOnly() {
		cols = append(cols, column{title: "Id"})
	} else {
		cols = append(cols, column{title: "Source", group: true})
	}
	cols = append(cols,
		column{title: "Titre", flex: true},
		column{title: "Enjeu"},
		column{title: "Détail", flex: true},
	)
	return cols
}

func (m *model) cells(it connect.Item) []cell {
	now := m.now()
	done := (it.Type == "due" && it.State != ledger.Open) || it.State == "acked"
	urg := urgency(it.At, now, it.Late)
	if done {
		urg = sMuted
	}
	var out []cell
	if m.ledgerOnly() {
		out = append(out, cell{period(it, now), sSection})
	}
	date := shortDay(it.At, now)
	out = append(out, cell{date, urg})
	clock := ""
	if !it.AllDay {
		clock = it.At.Format("15:04")
	}
	out = append(out, cell{clock, sMuted})
	if m.multi() {
		c := cOther
		switch it.Sphere {
		case "perso":
			c = cPerso
		case "pro":
			c = cPro
		}
		out = append(out, cell{m.cfg.Spheres[it.Sphere].Prefix, lipgloss.NewStyle().Foreground(c).Bold(true)})
	}
	if m.ledgerOnly() {
		out = append(out, cell{it.ID, sKey})
	} else {
		out = append(out, cell{it.Source, sourceStyle(it)})
	}
	title := cell{it.Title, sText}
	if done {
		title.style = sMuted.Strikethrough(true)
	} else if it.Late {
		title.style = lipgloss.NewStyle().Foreground(cStopped)
	}
	out = append(out, title)
	stake := cell{"", sMuted}
	if !done && judge.IsCritical(it, m.cfg.Judge.Threshold) {
		stake = cell{"⚠ " + judge.NatureLabels[it.Nature], sErr}
		if it.Nature == "" || it.Nature == "none" {
			stake.text = "⚠ critique"
		}
	}
	out = append(out, stake)
	detail := it.Detail
	if it.Type == "due" {
		detail = m.entryTail(it)
	}
	out = append(out, cell{detail, sMuted})
	return out
}

// widths sizes the columns on the lines shown: fixed ones to their longest
// value, the title to what is left, capped, then the detail.
func (m *model) widths(cols []column, rows [][]cell, w int) []int {
	ws := make([]int, len(cols))
	for i, c := range cols {
		ws[i] = ansi.StringWidth(c.title)
	}
	for _, r := range rows {
		for i, c := range r {
			if !cols[i].flex {
				ws[i] = max(ws[i], ansi.StringWidth(c.text))
			}
		}
	}
	for i, c := range cols {
		if c.title == "Enjeu" && ws[i] == ansi.StringWidth(c.title) {
			ws[i] = 0 // no critical line: the column disappears
		}
	}
	used := 1
	for i, c := range cols {
		if !c.flex && ws[i] > 0 {
			used += ws[i] + 2
		}
	}
	left := max(10, w-used-2)
	title, detail := -1, -1
	for i, c := range cols {
		if c.title == "Titre" {
			title = i
		}
		if c.title == "Détail" {
			detail = i
		}
	}
	longest := 0
	for _, r := range rows {
		longest = max(longest, ansi.StringWidth(r[title].text))
	}
	if left < 50 {
		ws[title], ws[detail] = left, 0
		return ws
	}
	ws[title] = min(max(longest, 12), left*65/100)
	ws[detail] = left - ws[title] - 2
	return ws
}

func renderRow(cs []cell, ws []int) string {
	var b strings.Builder
	b.WriteString(" ")
	for i, c := range cs {
		if ws[i] <= 0 {
			continue
		}
		b.WriteString(pad(c.style.Render(trunc(c.text, ws[i])), ws[i]))
		if i < len(cs)-1 {
			b.WriteString("  ")
		}
	}
	return b.String()
}

// list renders the lines as a table; a value equal to the one above is left
// blank, inside its day (and, in the ledger, its period).
func (m *model) list(w, h, top int) []string {
	m.listY, m.listW, m.listH = top, w, h
	m.rowItem = nil
	if len(m.items) == 0 {
		switch {
		case !m.ready || (m.reading() && !m.ledgerOnly()):
			return []string{"  " + sMuted.Render(spinner[m.spin%len(spinner)]+" lecture des sources…")}
		case m.filter != "":
			return []string{"", "  " + sMuted.Render("Rien ne correspond à « "+m.filter+" ». esc efface le filtre.")}
		case m.critOnly:
			return []string{"", "  " + sMuted.Render("Aucune ligne critique ici. c montre toutes les lignes.")}
		case m.ledgerOnly():
			return []string{"", "  " + sText.Render("Le registre est vide."),
				"  " + sMuted.Render("Il garde les dates à ne pas rater : délai de contrat, de signature, fin de garantie. Ce qui est à faire va dans task."),
				"  " + sKey.Render("a") + sMuted.Render(" ajoute une échéance · ") + sKey.Render("s") + sMuted.Render(" montre les autres sources")}
		default:
			return []string{"", "  " + sMuted.Render("Rien d'échu dans cette fenêtre. H élargit l'horizon.")}
		}
	}
	cols := m.columns()
	all := make([][]cell, len(m.items))
	for i, it := range m.items {
		all[i] = m.cells(it)
	}
	tableW := w
	if len(m.items) > h-2 {
		tableW = w - 1 // room for the scrollbar
	}
	ws := m.widths(cols, all, tableW)

	// Blank what repeats: a group column inside the same leading values.
	shown := make([][]cell, len(all))
	for i := range all {
		shown[i] = append([]cell(nil), all[i]...)
		if i == 0 {
			continue
		}
		same := true
		for k, c := range cols {
			if !c.group {
				continue
			}
			if same && all[i][k].text == all[i-1][k].text {
				shown[i][k].text = ""
				continue
			}
			same = false
		}
	}

	head := make([]cell, len(cols))
	rule := make([]cell, len(cols))
	for i, c := range cols {
		head[i] = cell{c.title, lipgloss.NewStyle().Bold(true).Foreground(cMuted)}
		rule[i] = cell{strings.Repeat("─", max(0, ws[i])), sMuted}
	}
	fixed := []string{renderRow(head, ws), renderRow(rule, ws)}

	var rows []string
	var owners []int
	selRow := 0
	future := m.firstFuture()
	for i := range m.items {
		if i == future {
			rows, owners = append(rows, m.nowRule(tableW)), append(owners, -1)
			// After the rule, the first line shows its values again.
			shown[i] = all[i]
		}
		line := renderRow(shown[i], ws)
		if i == m.sel {
			selRow = len(rows)
			line = selectLine(line, tableW)
		}
		rows, owners = append(rows, line), append(owners, i)
	}
	if future < 0 && m.isPast(m.items[len(m.items)-1]) {
		rows, owners = append(rows, m.nowRule(tableW)), append(owners, -1)
	}
	body := h - len(fixed)
	if selRow < m.top {
		m.top = selRow
	}
	if selRow >= m.top+body {
		m.top = selRow - body + 1
	}
	m.top = max(0, min(m.top, len(rows)-body))
	end := min(len(rows), m.top+body)
	// The first line on screen shows all its values: the ones above are hidden.
	for r := m.top; r < end; r++ {
		if i := owners[r]; i >= 0 {
			line := renderRow(all[i], ws)
			if i == m.sel {
				line = selectLine(line, tableW)
			}
			rows[r] = line
			break
		}
	}
	m.listY = top + len(fixed)
	m.rowItem = owners[m.top:end]
	out := rows[m.top:end]
	if len(rows) > body {
		out = withScrollbar(out, w, m.top, len(rows), body)
	}
	return append(fixed, out...)
}
