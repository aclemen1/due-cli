package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/aclemen1/due-cli/internal/actions"
	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/connect"
	"github.com/aclemen1/due-cli/internal/judge"
	"github.com/aclemen1/due-cli/internal/ledger"
	"github.com/aclemen1/due-cli/internal/spec"
	"github.com/aclemen1/due-cli/internal/trigger"
	"github.com/aclemen1/due-cli/internal/watch"
	"github.com/aclemen1/due-cli/internal/when"
)

type (
	tickMsg   struct{}
	pollMsg   struct{}
	watchMsg  struct{ source string }
	ledgerMsg struct {
		items   []connect.Item
		details map[string]*actions.Detail
		acks    map[string]bool // <sphere>\x00<ack key>
		err     error
	}
	connMsg struct {
		name  string
		items []connect.Item
		err   error
	}
	doneMsg struct {
		select_ string // an entry to select once the ledger is read again
	}
)

func errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }

func tick() tea.Cmd { return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }
func poll() tea.Cmd { return tea.Tick(pollEvery, func(time.Time) tea.Msg { return pollMsg{} }) }

func waitWatch(w *watch.Watcher) tea.Cmd {
	return func() tea.Msg { return watchMsg{<-w.C} }
}

// loadLedger reads the ledgers under Busy.
func (m *model) loadLedger() tea.Cmd {
	read := m.readLedger()
	return m.busy.Wrap("relecture du registre", func() tea.Msg {
		msg := read()
		if l, ok := msg.(ledgerMsg); ok && l.err != nil {
			return loadFail{name: "due", err: l.err}
		}
		return msg
	})
}

// readLedger reads every entry of the ledgers, with its detail; ids are unique across spheres.
func (m *model) readLedger() tea.Cmd {
	m.loading["due"] = true
	ctx := m.ctx(map[string]any{})
	spheres := m.spheres
	return func() tea.Msg {
		out := ledgerMsg{details: map[string]*actions.Detail{}, acks: map[string]bool{}}
		for _, sp := range spheres {
			l, err := actions.OpenLedger(ctx, m.cfg, sp)
			if err != nil {
				return ledgerMsg{err: err}
			}
			entries, err := l.List()
			if err != nil {
				return ledgerMsg{err: err}
			}
			for k := range l.AckSet() {
				out.acks[sp+"\x00"+k] = true
			}
			for _, e := range entries {
				d := actions.DetailOf(l, e)
				out.details[e.ID] = &d
				out.items = append(out.items, actions.ItemOf(l, e))
			}
		}
		judge.Annotate(out.items)
		return out
	}
}

// loadConn reads one connector, by its key <sphere>/<name>, over the horizon.
func (m *model) loadConn(key string) tea.Cmd {
	var c *conn
	for _, x := range m.connectors() {
		if x.key() == key {
			x := x
			c = &x
		}
	}
	if c == nil {
		return nil
	}
	m.loading[key] = true
	now := m.now()
	until, _ := when.Horizon(horizons[m.horizon], now, m.cfg.DefaultTime)
	w := connect.Window{Until: until, Now: now, Sphere: c.sphere}
	label := "relecture " + c.c.Name
	if m.multi() {
		label = "relecture " + key
	}
	prev := m.connErr[key] // the failure already shown for this source, if any
	return m.busy.Wrap(label, func() tea.Msg {
		items, err := connect.One(c.c, w)
		if err != nil {
			// A failure shows once per outage: the same error again only keeps the source counted in error.
			if err.Error() == prev {
				return connMsg{name: key, err: err}
			}
			return loadFail{name: key, err: err}
		}
		for i := range items {
			items[i].Sphere = c.sphere
		}
		judge.Annotate(items)
		return connMsg{name: key, items: items}
	})
}

func (m *model) loadAll() tea.Cmd {
	m.lastFull = m.now()
	var cmds []tea.Cmd
	for _, c := range m.connectors() {
		cmds = append(cmds, m.loadConn(c.key()))
	}
	return tea.Batch(cmds...)
}

// reading: a read of the ledger or of a source is under way.
func (m *model) reading() bool {
	for _, b := range m.loading {
		if b {
			return true
		}
	}
	return false
}

// actLabels name the job of an action, by the id of its line.
var actLabels = map[string]string{"ack": "retirer la ligne", "unack": "rendre la ligne", "done": "clore", "reopen": "rouvrir",
	"edit": "modifier", "snooze": "reporter", "drop": "abandonner", "run": "exécuter", "rm": "supprimer"}

// act runs an action of the ledger under Busy, as `due <name>` would; args carry the sphere it
// writes to; status is the end shown.
func (m *model) act(name string, args map[string]any, status string) tea.Cmd {
	label := actLabels[name] + " " + fmt.Sprint(args["id"])
	if name == "add" {
		label = "ajouter « " + fmt.Sprint(args["title"]) + " »"
	}
	cfgPath := m.cfgPath
	return m.run(label, func() (string, tea.Msg, error) {
		a := spec.Find("due", name)
		parsed, err := spec.ArgsFrom(a, args)
		if err != nil {
			return "", nil, err
		}
		res, err := a.Run(&spec.Context{Args: parsed, Config: cfgPath, Format: "json"})
		if err != nil {
			return "", nil, err
		}
		out, text := doneMsg{}, status
		switch r := res.(type) {
		case trigger.Fired:
			text += " : " + r.Result
		case *ledger.Entry:
			out.select_ = fmt.Sprint(args["sphere"]) + "\x00due\x00" + r.ID
		}
		return text, out, nil
	})
}

// apply rebuilds the visible lines from the data, keeping the selection.
func (m *model) apply() {
	var out []connect.Item
	now := m.now()
	until, _ := when.Horizon(horizons[m.horizon], now, m.cfg.DefaultTime)
	switch {
	case m.ledgerOnly():
		for _, it := range m.ledger {
			if it.State == ledger.Open || m.showDone {
				out = append(out, it)
			}
		}
	case m.source == 1:
		for _, it := range m.ledger {
			if it.State == ledger.Open && !it.At.After(until) {
				out = append(out, it)
			}
		}
		for _, c := range m.connectors() {
			out = append(out, m.conn[c.key()]...)
		}
	default:
		for _, k := range m.keysOf(m.sourceName()) {
			out = append(out, m.conn[k]...)
		}
	}
	// Lines taken off with due ack: hidden, or shown as acked with f.
	var unacked []connect.Item
	for _, it := range out {
		if it.Type != "due" && m.acks[it.Sphere+"\x00"+ledger.AckKey(it.Source, it.ID, it.At.Format(time.RFC3339))] {
			if !m.showDone {
				continue
			}
			it.State, it.Late = "acked", false
		}
		unacked = append(unacked, it)
	}
	out = unacked
	if m.sphereOnly != "" {
		var kept []connect.Item
		for _, it := range out {
			if it.Sphere == m.sphereOnly {
				kept = append(kept, it)
			}
		}
		out = kept
	}
	if m.critOnly {
		var kept []connect.Item
		for _, it := range out {
			if judge.IsCritical(it, m.cfg.Judge.Threshold) {
				kept = append(kept, it)
			}
		}
		out = kept
	}
	if m.filter != "" {
		needle := strings.ToLower(m.filter)
		var kept []connect.Item
		for _, it := range out {
			if strings.Contains(strings.ToLower(it.Title+" "+it.Detail+" "+it.ID+" "+it.Source), needle) {
				kept = append(kept, it)
			}
		}
		out = kept
	}
	if m.ledgerOnly() {
		sort.SliceStable(out, func(i, j int) bool {
			oi, oj := out[i].State == ledger.Open, out[j].State == ledger.Open
			if oi != oj {
				return oi
			}
			return out[i].At.Before(out[j].At)
		})
	} else {
		connect.Sort(out)
	}
	if m.sortBy > 0 || m.sortRev {
		by := sorts[m.sortBy]
		sort.SliceStable(out, func(i, j int) bool {
			var a, b string
			switch by {
			case "titre":
				a, b = strings.ToLower(out[i].Title), strings.ToLower(out[j].Title)
			case "source":
				a, b = out[i].Source, out[j].Source
			default:
				return out[i].At.Before(out[j].At) != m.sortRev
			}
			if a == b {
				return false
			}
			return (a < b) != m.sortRev
		})
	}
	m.items = out
	sel := -1
	for i, it := range out {
		if key(it) == m.selKey {
			sel = i
		}
	}
	if sel < 0 && m.selKey != "" && m.reading() {
		// The remembered line may be in a source still loading: wait for it.
		m.sel = max(0, min(m.sel, len(out)-1))
		return
	}
	if sel < 0 && m.selKey == "" {
		sel = m.firstFuture()
	}
	if sel < 0 {
		sel = min(m.sel, len(out)-1)
	}
	m.sel = -1
	m.selectIndex(sel)
}

// counts for the header.
func (m *model) counts() (open, late int, next *connect.Item) {
	for i, it := range m.ledger {
		if it.State != ledger.Open {
			continue
		}
		open++
		if it.Late {
			late++
		} else if next == nil || it.At.Before(next.At) {
			next = &m.ledger[i]
		}
	}
	return
}

type savedState struct {
	View     string `json:"view"`
	Horizon  string `json:"horizon"`
	ShowDone bool   `json:"show_done"`
	DetailOn bool   `json:"detail_on"`
	CritOnly bool   `json:"critical_only"`
	Filter   string `json:"filter,omitempty"`
	Selected string `json:"selected"`
}

func (m *model) statePath() string {
	return filepath.Join(config.StateDir(), "tui-"+strings.Join(m.spheres, "+")+".json")
}

// persist writes the state when it changed, so a restart, even after ctrl+c
// or a closed pane, comes back to the same view, filters and line.
func (m *model) persist() {
	s := savedState{View: m.sourceName(), Horizon: horizons[m.horizon], ShowDone: m.showDone, DetailOn: m.detailOn,
		CritOnly: m.critOnly, Filter: m.filter, Selected: m.selKey}
	b, _ := json.Marshal(s)
	if bytes.Equal(b, m.saved) {
		return
	}
	m.saved = b
	_ = os.MkdirAll(config.StateDir(), 0o755)
	_ = os.WriteFile(m.statePath(), b, 0o644)
}

func (m *model) restore() {
	b, err := os.ReadFile(m.statePath())
	if err != nil {
		return
	}
	var s savedState
	if json.Unmarshal(b, &s) != nil {
		return
	}
	for i, v := range m.views() {
		if v == s.View {
			m.source = i
		}
	}
	for i, h := range horizons {
		if h == s.Horizon {
			m.horizon = i
		}
	}
	m.showDone, m.detailOn, m.selKey = s.ShowDone, s.DetailOn, s.Selected
	m.critOnly, m.filter = s.CritOnly, s.Filter
	m.saved = b
}

type notesMsg map[string][]actions.Note

// loadNotes fetches the notes of the entries, after the ledger: they never hold it up.
func (m *model) loadNotes() tea.Cmd {
	if len(m.cfg.Notes.Ls) == 0 {
		return nil
	}
	var ids []string
	for _, it := range m.ledger {
		ids = append(ids, it.ID)
	}
	cfg := m.cfg
	return func() tea.Msg {
		out := notesMsg{}
		for _, id := range ids {
			if notes, err := actions.NotesOf(cfg, "due:"+id); err == nil {
				out[id] = notes
			}
		}
		return out
	}
}
