package tui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/aclemen1/due-cli/internal/connect"
	"github.com/aclemen1/due-cli/internal/ledger"
	"github.com/aclemen1/due-cli/internal/when"
)

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// sideFrom is the width from which the detail sits at the right of the list.
const sideFrom = 110

func (m *model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *model) render() string {
	w, h := max(m.w, 40), max(m.h, 10)
	lines := m.header(w)
	m.tabsY = 1
	foot := m.footer(w)
	bodyH := h - len(lines) - 2
	sep := sMuted.Render(strings.Repeat("─", w))

	var body []string
	switch {
	case m.form != nil:
		body = m.form.view(w, bodyH)
		m.listH, m.rowItem = 0, nil
	case m.helpOn:
		body = m.help(w)
		m.listH, m.rowItem = 0, nil
	default:
		body = m.panes(w, bodyH, len(lines))
	}
	lines = append(lines, body...)
	for len(lines) < h-2 {
		lines = append(lines, "")
	}
	lines = append(lines[:h-2], sep, foot)
	return block(lines, w, h)
}

// panes lays out the list and the detail: side by side when wide, stacked
// when tall enough, the list alone otherwise or when the detail is off.
func (m *model) panes(w, h, top int) []string {
	_, hasSel := m.current()
	detail := m.detailOn && hasSel
	switch {
	case detail && w >= sideFrom:
		lw := w * 54 / 100
		dw := w - lw - 3
		left := m.list(lw, h, top)
		right := m.detail(dw, h)
		out := make([]string, h)
		bar := sMuted.Render(" │ ")
		for i := range out {
			l, r := "", ""
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			out[i] = pad(l, lw) + bar + pad(r, dw)
		}
		return out
	case detail && h >= 18:
		lh := h * 55 / 100
		out := m.list(w, lh, top)
		for len(out) < lh {
			out = append(out, "")
		}
		out = append(out, sMuted.Render(strings.Repeat("┄", w)))
		return append(out, m.detail(w, h-lh-1)...)
	default:
		return m.list(w, h, top)
	}
}

func (m *model) header(w int) []string {
	open, late, next := m.counts()
	var tags []string
	for _, sp := range m.spheres {
		tags = append(tags, sphereTag(sp))
	}
	left := sTitle.Render("due") + sMuted.Render(" · ") + strings.Join(tags, sMuted.Render(" + "))
	var facts []string
	facts = append(facts, sText.Render(plural(open, "échéance ouverte", "échéances ouvertes")))
	if late > 0 {
		facts = append(facts, sErr.Render(fmt.Sprintf("%d en retard", late)))
	}
	if next != nil {
		facts = append(facts, sMuted.Render("prochaine : ")+sText.Render(trunc(next.Title, 28))+" "+urgency(next.At, m.now(), false).Render(relShort(next.At, m.now())))
	}
	left += sMuted.Render("  ") + strings.Join(facts, sMuted.Render(" · "))
	right := ""
	if m.busy() {
		right = sWarn.Render(spinner[m.spin%len(spinner)]) + sMuted.Render(" lecture")
	} else if !m.loadedAt.IsZero() {
		right = sMuted.Render("à jour " + ago(m.loadedAt, m.now()))
	}
	if n := len(m.connErr); n > 0 {
		right = sErr.Render(fmt.Sprintf("%d source(s) en erreur", n)) + sMuted.Render(" · ") + right
	}
	gap := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	line1 := left + strings.Repeat(" ", max(1, gap)) + right

	// The views, as tabs: s and the digits move among them.
	var tabs []string
	m.tabX = nil
	x := 0
	counts := 0
	for i, v := range m.views() {
		counts += len(v) + 5 + len(m.badge(i)) + 1
	}
	withCounts := counts+30 <= w
	for i, v := range m.views() {
		label := fmt.Sprintf(" %d %s ", i+1, v)
		if n := m.badge(i); n != "" && withCounts {
			label = fmt.Sprintf(" %d %s %s ", i+1, v, n)
		}
		st := sMuted
		if i == m.source {
			st = lipgloss.NewStyle().Bold(true).Foreground(cText).Background(cSel)
		} else if m.failed(v) {
			st = lipgloss.NewStyle().Foreground(cStopped)
		}
		m.tabX = append(m.tabX, x)
		tabs = append(tabs, st.Render(label))
		x += ansi.StringWidth(label) + 1
	}
	line2 := strings.Join(tabs, " ")
	scope := ""
	switch {
	case m.ledgerOnly() && m.showDone:
		scope = "toutes les dates · faites comprises"
	case m.ledgerOnly():
		scope = "toutes les dates"
	default:
		until, _ := when.Horizon(horizons[m.horizon], m.now(), m.cfg.DefaultTime)
		scope = "jusqu'au " + when.Day(until) + " (" + horizons[m.horizon] + ")"
	}
	if m.filter != "" {
		scope = "filtre « " + m.filter + " » · " + scope
	}
	if gap = w - ansi.StringWidth(line2) - ansi.StringWidth(scope); gap >= 2 {
		line2 += strings.Repeat(" ", gap) + sMuted.Render(scope)
	}
	return []string{line1, line2, sMuted.Render(strings.Repeat("─", w))}
}

func (m *model) badge(view int) string {
	switch view {
	case 0:
		open, _, _ := m.counts()
		return fmt.Sprint(open)
	case 1:
		return ""
	}
	n, pending := 0, false
	for _, k := range m.keysOf(m.views()[view]) {
		n += len(m.conn[k])
		pending = pending || (m.loading[k] && m.conn[k] == nil)
	}
	if pending && n == 0 {
		return "…"
	}
	return fmt.Sprint(n)
}

func (m *model) footer(w int) string {
	if m.prompt != pNone {
		label := map[prompt]string{pFilter: "chercher", pSnooze: "reporter de", pConfirmDrop: "abandonner ?",
			pConfirmRun: "exécuter ?", pConfirmRm: "supprimer ?"}[m.prompt]
		return sKey.Render(label+" › ") + m.input.View()
	}
	if m.status != "" {
		if m.statusErr {
			return sErr.Render("✗ " + m.status)
		}
		return sOK.Render("✓ " + m.status)
	}
	if m.form != nil {
		return m.form.footer()
	}
	if m.helpOn {
		return helpLine("esc", "fermer l'aide", "q", "quitter")
	}
	it, ok := m.current()
	switch {
	case ok && it.Type == "due":
		return helpLine("a", "ajouter", "e", "modifier", "d", "faite", "z", "reporter", "R", "exécuter", "s", "vue suivante", "/", "chercher", "?", "aide", "q", "quitter")
	case ok && it.Type == "office":
		return helpLine("a", "ajouter", "o", "ouvrir le dossier", "s", "vue suivante", "esc", "registre", "/", "chercher", "?", "aide", "q", "quitter")
	default:
		return helpLine("a", "ajouter", "s", "vue suivante", "H", "horizon", "esc", "registre", "/", "chercher", "?", "aide", "q", "quitter")
	}
}

func (m *model) help(w int) []string {
	col := func(k, v string) string { return "  " + sKey.Render(fmt.Sprintf("%-14s", k)) + sText.Render(v) }
	out := []string{
		sSection.Render("Se déplacer"),
		col("↑ ↓  j k", "ligne précédente, suivante"),
		col("pgup pgdn", "page"),
		col("g  G", "début, fin"),
		col("J K", "faire défiler le détail"),
		col("souris", "clic : choisir ; molette : défiler"),
		"",
		sSection.Render("Vues"),
		col("s  S  ← →", "vue suivante, précédente : registre, toutes, chaque source"),
		col("1 … 9", "aller à une vue"),
		col("esc", "efface le filtre, puis revient au registre"),
		col("H", "horizon des vues toutes et sources : 7, 30, 90, 365 jours"),
		col("f", "montrer aussi les échéances faites et abandonnées"),
		col("tab", "montrer ou masquer le détail"),
		col("/", "chercher dans les titres et détails"),
		col("r", "relire toutes les sources"),
		"",
		sSection.Render("Registre"),
		col("a", "ajouter une échéance"),
		col("e  E", "modifier (formulaire, ou fichier dans $EDITOR)"),
		col("d", "faite, ou rouvrir"),
		col("z", "reporter (7d, 2w, une date)"),
		col("R", "exécuter l'action maintenant"),
		col("x  D", "abandonner, supprimer"),
		col("o  ↵", "ouvrir la ligne (dossier d'office)"),
		"",
		sMuted.Render("  Le registre garde ce qu'aucun autre outil ne porte : contrat, garantie, délai légal."),
		sMuted.Render("  Les autres lignes se lisent ici et se modifient dans leur outil."),
		sMuted.Render("  Mise à jour : à chaque changement des fichiers d'office, routine, oj et du registre ;"),
		sMuted.Render("  rappels et agendas toutes les minutes."),
	}
	if len(m.connErr) > 0 {
		out = append(out, "", sSection.Render("Sources en erreur"))
		for name, e := range m.connErr {
			out = append(out, "  "+sErr.Render(name)+sMuted.Render(" : "+e))
		}
	}
	return out
}

// ---------------------------------------------------------------- list

type group struct {
	title string
	items []int
}

// groups: by period in the ledger (dates may be years away), by day elsewhere.
func (m *model) groups() []group {
	now := m.now()
	var out []group
	add := func(title string, i int) {
		if len(out) == 0 || out[len(out)-1].title != title {
			out = append(out, group{title: title})
		}
		out[len(out)-1].items = append(out[len(out)-1].items, i)
	}
	for i, it := range m.items {
		if m.ledgerOnly() {
			add(period(it, now), i)
		} else {
			add(when.Day(it.At)+" · "+when.Until(it.At, now), i)
		}
	}
	return out
}

func period(it connect.Item, now time.Time) string {
	if it.Type == "due" && it.State != ledger.Open {
		return "Faites et abandonnées"
	}
	if it.Late {
		return "En retard"
	}
	days := daysBetween(now, it.At)
	switch {
	case days <= 0:
		return "Aujourd'hui"
	case days <= 7:
		return "Cette semaine"
	case days <= 31:
		return "Ce mois-ci"
	case days <= 92:
		return "Dans les 3 mois"
	case days <= 366:
		return "Dans l'année"
	default:
		return "Plus tard"
	}
}

func (m *model) list(w, h, top int) []string {
	m.listY, m.listW, m.listH = top, w, h
	m.rowItem = nil
	if len(m.items) == 0 {
		var msg []string
		switch {
		case !m.ready || (m.busy() && !m.ledgerOnly()):
			msg = []string{"  " + sMuted.Render(spinner[m.spin%len(spinner)]+" lecture des sources…")}
		case m.filter != "":
			msg = []string{"", "  " + sMuted.Render("Rien ne correspond à « "+m.filter+" ». esc efface le filtre.")}
		case m.ledgerOnly():
			msg = []string{"", "  " + sText.Render("Le registre est vide."),
				"  " + sMuted.Render("Il garde ce qu'aucun autre outil ne porte : fin de contrat, garantie, délai légal."),
				"  " + sKey.Render("a") + sMuted.Render(" ajoute une échéance · ") + sKey.Render("s") + sMuted.Render(" montre les autres sources")}
		default:
			msg = []string{"", "  " + sMuted.Render("Rien d'échu dans cette fenêtre. H élargit l'horizon.")}
		}
		return msg
	}
	var rows []string
	var owners []int
	selRow := 0
	future := m.firstFuture()
	nowLine := func() {
		rows, owners = append(rows, m.nowRule(w)), append(owners, -1)
	}
	for gi, g := range m.groups() {
		if gi > 0 {
			rows, owners = append(rows, ""), append(owners, -1)
		}
		if g.items[0] == future {
			nowLine()
			rows, owners = append(rows, ""), append(owners, -1)
		}
		title := sSection.Render(g.title)
		if m.ledgerOnly() {
			title += sMuted.Render(fmt.Sprintf("  %d", len(g.items)))
		}
		rows, owners = append(rows, " "+title), append(owners, -1)
		for k, i := range g.items {
			if i == future && k > 0 {
				nowLine()
			}
			line := m.row(m.items[i], w)
			if i == m.sel {
				selRow = len(rows)
				line = selectLine(line, w)
			}
			rows, owners = append(rows, line), append(owners, i)
		}
	}
	if future < 0 && len(m.items) > 0 && m.isPast(m.items[len(m.items)-1]) {
		nowLine()
	}
	// Keep the selection visible, with its group title when it fits.
	if selRow-2 < m.top {
		m.top = max(0, selRow-2)
	}
	if selRow >= m.top+h {
		m.top = selRow - h + 1
	}
	m.top = max(0, min(m.top, len(rows)-h))
	end := min(len(rows), m.top+h)
	m.rowItem = owners[m.top:end]
	out := rows[m.top:end]
	if len(rows) > h {
		out = withScrollbar(out, w, m.top, len(rows), h)
	}
	return out
}

func withScrollbar(lines []string, w, top, total, h int) []string {
	thumb := max(1, h*h/total)
	pos := (h - thumb) * top / max(1, total-h)
	for i := range lines {
		ch := sMuted.Render("│")
		if i >= pos && i < pos+thumb {
			ch = sKey.Render("┃")
		}
		lines[i] = pad(lines[i], w-1) + ch
	}
	return lines
}

func (m *model) row(it connect.Item, w int) string {
	now := m.now()
	done := it.Type == "due" && it.State != ledger.Open
	urg := urgency(it.At, now, it.Late)
	if done {
		urg = sMuted
	}
	dot := urg.Render("●")
	if it.Late && !done {
		dot = sErr.Render("!")
	}
	var date string
	if m.ledgerOnly() {
		date = shortDay(it.At, now)
		if !it.AllDay {
			date += " " + it.At.Format("15:04")
		}
		date = fmt.Sprintf("%-17s", date)
	} else if it.AllDay {
		date = "     "
	} else {
		date = it.At.Format("15:04")
	}
	rel := fmt.Sprintf("%-11s", relShort(it.At, now))
	title := sText.Render(it.Title)
	if done {
		title = sMuted.Strikethrough(true).Render(it.Title)
	}
	var tail string
	if it.Type == "due" {
		tail = m.entryTail(it)
	} else {
		tail = it.Detail
	}
	src := ""
	if !m.ledgerOnly() {
		src = sourceStyle(it).Render(fmt.Sprintf("%-9s", trunc(it.Source, 9))) + " "
	}
	line := fmt.Sprintf(" %s %s %s %s%s", dot, m.sphereMark(it.Sphere), sMuted.Render(date), src, title)
	if m.ledgerOnly() {
		line = fmt.Sprintf(" %s %s%s %s %s", dot, m.sphereMark(it.Sphere), sText.Render(date), urg.Render(rel), title)
	}
	if tail != "" {
		room := w - ansi.StringWidth(line) - 3
		if room > 6 {
			line += "  " + sMuted.Render(trunc(tail, room))
		}
	}
	return line
}

// entryTail sums up an entry: its action and its next notice.
func (m *model) entryTail(it connect.Item) string {
	d := m.details[it.ID]
	if d == nil {
		return ""
	}
	var parts []string
	if d.Do != "" {
		parts = append(parts, "→ "+d.Do)
	}
	for _, u := range d.Upcoming {
		if strings.HasPrefix(u.Kind, "notice") {
			parts = append(parts, "préavis "+u.When.Format("02.01"))
			break
		}
	}
	if d.Ref != "" {
		parts = append(parts, d.Ref)
	}
	return strings.Join(parts, " · ")
}

// ---------------------------------------------------------------- detail

func (m *model) detail(w, h int) []string {
	it, ok := m.current()
	if !ok {
		return nil
	}
	var out []string
	if it.Type == "due" {
		out = m.entryDetail(it, w)
	} else {
		out = m.lineDetail(it, w)
	}
	m.scroll = max(0, min(m.scroll, len(out)-h))
	return out[m.scroll:min(len(out), m.scroll+h)]
}

func field(name, value string) string {
	return " " + sMuted.Render(fmt.Sprintf("%-9s", name)) + value
}

func wrap(s string, w int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		out = append(out, strings.Split(lipgloss.NewStyle().Width(max(10, w)).Render(para), "\n")...)
	}
	return out
}

func (m *model) lineDetail(it connect.Item, w int) []string {
	now := m.now()
	when_ := when.Day(it.At)
	if !it.AllDay {
		when_ += " " + it.At.Format("15:04")
	}
	out := []string{
		" " + sBold.Render(trunc(it.Title, w-2)),
		" " + urgency(it.At, now, it.Late).Render(when_+" · "+when.Until(it.At, now)),
		"",
		field("source", sourceStyle(it).Render(it.Source)+sMuted.Render(" ("+it.Type+")")),
		field("sphère", sphereTag(it.Sphere)),
		field("id", sText.Render(it.ID)),
	}
	if it.Detail != "" {
		for i, l := range wrap(it.Detail, w-11) {
			name := ""
			if i == 0 {
				name = "détail"
			}
			out = append(out, field(name, sText.Render(l)))
		}
	}
	if it.Ref != "" {
		out = append(out, field("réf.", sText.Render(it.Ref)))
	}
	out = append(out, "", " "+sMuted.Render("Cette ligne vient de "+it.Type+" : elle se modifie dans cet outil."))
	if it.Type == "office" {
		out = append(out, " "+sKey.Render("o")+sMuted.Render(" ouvre le dossier "+it.ID+"."))
	}
	return out
}

func (m *model) entryDetail(it connect.Item, w int) []string {
	d := m.details[it.ID]
	if d == nil {
		return []string{" " + sMuted.Render("lecture…")}
	}
	e := d.Entry
	now := m.now()
	urg := urgency(d.Term, now, it.Late)
	day := when.Day(d.Term)
	if !e.Moment().AllDay {
		day += " " + d.Term.Format("15:04")
	}
	state := map[string]string{ledger.Open: "ouverte", ledger.Done: "faite", ledger.Dropped: "abandonnée"}[e.State]
	out := []string{
		" " + sKey.Render(e.ID) + "  " + sBold.Render(trunc(e.Title, w-12)),
		" " + urg.Render(day+" · "+when.Until(d.Term, now)) + sMuted.Render("  ·  "+state+"  ·  ") + sphereTag(d.Sphere),
	}
	if e.Ref != "" {
		out = append(out, " "+sMuted.Render("réf. ")+sText.Render(e.Ref))
	}
	out = append(out, "", " "+sSection.Render("Calendrier"))
	created, _ := time.Parse(time.RFC3339, e.Created)
	for _, in := range e.Instants(now.Location(), m.cfg.DefaultTime) {
		mark, st, note := sMuted.Render("○"), sText, ""
		fired := false
		for _, f := range e.Fired {
			if f.Kind == in.Kind && f.Instant == in.When.Format(time.RFC3339) {
				switch {
				case f.Result == "ok":
					mark, note = sOK.Render("✓"), "envoyé"
				case f.Result == "skipped":
					mark, note = sMuted.Render("–"), "sauté (Mac en veille)"
				case f.Result == "running":
					mark, note = sWarn.Render("…"), "en cours"
				default:
					mark, note = sErr.Render("✗"), f.Result
				}
				fired = true
				st = sMuted
			}
		}
		label := "préavis " + strings.TrimPrefix(in.Kind, "notice ")
		if in.Kind == "term" {
			label = "terme"
			if e.Do != "" {
				note = strings.TrimSpace("→ " + e.Do + "  " + note)
			} else if note == "" {
				note = "sans action"
			}
		} else if note == "" && !in.When.After(created) {
			mark, note = sMuted.Render("–"), "avant la création"
		}
		if !fired && e.State != ledger.Open {
			mark, st = sMuted.Render("–"), sMuted
		}
		if note == "" && in.When.After(now) {
			note = relShort(in.When, now)
		}
		out = append(out, fmt.Sprintf(" %s %s %s  %s", mark, st.Render(fmt.Sprintf("%-12s", label)),
			st.Render(shortDay(in.When, now)+" "+in.When.Format("15:04")), sMuted.Render(note)))
	}
	for _, f := range e.Fired {
		if !strings.HasSuffix(f.Kind, "(by hand)") {
			continue
		}
		at, _ := time.Parse(time.RFC3339, f.At)
		mark := sOK.Render("✓")
		if f.Result != "ok" {
			mark = sErr.Render("✗")
		}
		kind := "terme"
		if strings.HasPrefix(f.Kind, "notice") {
			kind = "préavis"
		}
		out = append(out, fmt.Sprintf(" %s %s %s  %s", mark, sText.Render(fmt.Sprintf("%-12s", kind)),
			sText.Render(shortDay(at, now)+" "+at.Format("15:04")), sMuted.Render("à la main, "+f.Result)))
	}
	if e.Do == "command" && e.Run != "" {
		out = append(out, "", " "+sSection.Render("Commande"), " "+sText.Render(trunc(e.Run, w-2)))
	}
	if e.Body != "" {
		title := "Message"
		if e.Do == "agent" {
			title = "Prompt"
		}
		out = append(out, "", " "+sSection.Render(title))
		for _, l := range wrap(e.Body, w-3) {
			out = append(out, "  "+sText.Render(l))
		}
	}
	if len(e.Log) > 0 {
		out = append(out, "", " "+sSection.Render("Historique"))
		for i := len(e.Log) - 1; i >= 0 && i >= len(e.Log)-8; i-- {
			l := e.Log[i]
			at, _ := time.Parse(time.RFC3339, l.At)
			out = append(out, " "+sMuted.Render(at.Format("02.01 15:04")+"  ")+sText.Render(trunc(l.What, w-16)))
		}
	}
	return out
}

// ---------------------------------------------------------------- words

func daysBetween(now, t time.Time) int {
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.In(now.Location()).Date()
	a := time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC)
	b := time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC)
	return int(b.Sub(a).Hours() / 24)
}

// relShort: « aujourd'hui », « demain », « dans 8 j », « dans 5 sem », « dans 4 mois », « il y a 3 j ».
func relShort(t, now time.Time) string {
	d := daysBetween(now, t)
	abs := d
	if abs < 0 {
		abs = -abs
	}
	var s string
	switch {
	case d == 0:
		return "aujourd'hui"
	case d == 1:
		return "demain"
	case d == -1:
		return "hier"
	case abs < 21:
		s = fmt.Sprintf("%d j", abs)
	case abs < 75:
		s = fmt.Sprintf("%d sem", (abs+3)/7)
	case abs < 548:
		s = fmt.Sprintf("%d mois", (abs+15)/30)
	default:
		s = fmt.Sprintf("%d ans", (abs+182)/365)
	}
	if d < 0 {
		return "il y a " + s
	}
	return "dans " + s
}

// shortDay: « jeu. 15.10 » this year, « lun. 01.03.2028 » otherwise.
func shortDay(t, now time.Time) string {
	s := when.Day(t)
	if t.Year() == now.Year() {
		s = s[:len(s)-5]
	}
	return s
}

func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 5*time.Second:
		return "à l'instant"
	case d < time.Minute:
		return fmt.Sprintf("il y a %d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("il y a %d min", int(d.Minutes()))
	default:
		return "à " + t.Format("15:04")
	}
}

func trunc(s string, n int) string {
	r := []rune(s)
	if n <= 1 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// urgency colours a date: late red, within 7 days amber, within 30 accent, later grey.
func urgency(t, now time.Time, late bool) lipgloss.Style {
	d := daysBetween(now, t)
	var c color.Color
	switch {
	case late || d < 0:
		c = cStopped
	case d <= 7:
		c = cWorking
	case d <= 31:
		c = cAccent
	default:
		c = cMuted
	}
	return lipgloss.NewStyle().Foreground(c)
}

func sphereTag(s string) string {
	c := color.Color(cOther)
	switch s {
	case "perso":
		c = cPerso
	case "pro":
		c = cPro
	}
	return lipgloss.NewStyle().Foreground(c).Bold(true).Render(s)
}

func sourceStyle(it connect.Item) lipgloss.Style {
	switch it.Type {
	case "due":
		return lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	case "reminders":
		return lipgloss.NewStyle().Foreground(cPerso)
	case "calendar":
		return lipgloss.NewStyle().Foreground(cOther)
	case "office":
		return lipgloss.NewStyle().Foreground(cPro)
	case "routine", "oj":
		return lipgloss.NewStyle().Foreground(cWorking)
	}
	return sMuted
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// failed says whether a view's connector failed in a sphere.
func (m *model) failed(view string) bool {
	for _, k := range m.keysOf(view) {
		if _, bad := m.connErr[k]; bad {
			return true
		}
	}
	return false
}

// sphereMark is the sphere's prefix in its colour, shown when several spheres are.
func (m *model) sphereMark(sphere string) string {
	if !m.multi() {
		return ""
	}
	c := color.Color(cOther)
	switch sphere {
	case "perso":
		c = cPerso
	case "pro":
		c = cPro
	}
	return lipgloss.NewStyle().Foreground(c).Bold(true).Render(fmt.Sprintf("%-2s", m.cfg.Spheres[sphere].Prefix)) + " "
}

// isPast: a line whose moment is over (a date alone ends with its day). Done
// and dropped entries are not placed in time.
func (m *model) isPast(it connect.Item) bool {
	if it.Type == "due" && it.State != ledger.Open {
		return false
	}
	now := m.now()
	if it.AllDay {
		y, mo, d := it.At.Date()
		return time.Date(y, mo, d, 23, 59, 59, 0, it.At.Location()).Before(now)
	}
	return it.At.Before(now)
}

// firstFuture is the first line still to come after a past one, or -1 when
// nothing is past or nothing is to come.
func (m *model) firstFuture() int {
	past := false
	for i, it := range m.items {
		if it.Type == "due" && it.State != ledger.Open {
			continue
		}
		if m.isPast(it) {
			past = true
			continue
		}
		if past {
			return i
		}
		return -1
	}
	return -1
}

// nowRule separates what is over from what is to come.
func (m *model) nowRule(w int) string {
	label := " maintenant · " + shortDay(m.now(), m.now()) + " " + m.now().Format("15:04") + " "
	left := 2
	right := max(0, w-left-ansi.StringWidth(label)-1)
	st := lipgloss.NewStyle().Foreground(cStopped)
	return " " + st.Render(strings.Repeat("─", left)) + lipgloss.NewStyle().Foreground(cStopped).Bold(true).Render(label) + st.Render(strings.Repeat("─", right))
}
