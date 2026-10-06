package tui

import (
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
		err     error
	}
	connMsg struct {
		name  string
		items []connect.Item
		err   error
	}
	doneMsg struct {
		status  string
		select_ string // an entry to select once the ledger is read again
		err     error
	}
)

func errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }

func tick() tea.Cmd { return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }
func poll() tea.Cmd { return tea.Tick(pollEvery, func(time.Time) tea.Msg { return pollMsg{} }) }

func waitWatch(w *watch.Watcher) tea.Cmd {
	return func() tea.Msg { return watchMsg{<-w.C} }
}

// loadLedger reads every entry of the ledgers, with its detail; ids are unique across spheres.
func (m *model) loadLedger() tea.Cmd {
	m.loading["due"] = true
	ctx := m.ctx(map[string]any{})
	spheres := m.spheres
	return func() tea.Msg {
		out := ledgerMsg{details: map[string]*actions.Detail{}}
		for _, sp := range spheres {
			l, err := actions.OpenLedger(ctx, m.cfg, sp)
			if err != nil {
				return ledgerMsg{err: err}
			}
			entries, err := l.List()
			if err != nil {
				return ledgerMsg{err: err}
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
	return func() tea.Msg {
		items, err := connect.One(c.c, w)
		for i := range items {
			items[i].Sphere = c.sphere
		}
		judge.Annotate(items)
		return connMsg{name: key, items: items, err: err}
	}
}

func (m *model) loadAll() tea.Cmd {
	m.lastFull = m.now()
	var cmds []tea.Cmd
	for _, c := range m.connectors() {
		cmds = append(cmds, m.loadConn(c.key()))
	}
	return tea.Batch(cmds...)
}

func (m *model) busy() bool {
	for _, b := range m.loading {
		if b {
			return true
		}
	}
	return false
}

// act runs an action of the ledger, as `due <name>` would; args carry the sphere it writes to.
func (m *model) act(name string, args map[string]any, status string) tea.Cmd {
	return func() tea.Msg {
		a := spec.Find("due", name)
		parsed, err := spec.ArgsFrom(a, args)
		if err != nil {
			return doneMsg{err: err}
		}
		res, err := a.Run(&spec.Context{Args: parsed, Config: m.cfgPath, Format: "json"})
		if err != nil {
			return doneMsg{err: err}
		}
		out := doneMsg{status: status}
		switch r := res.(type) {
		case trigger.Fired:
			out.status += " : " + r.Result
		case *ledger.Entry:
			out.select_ = r.ID
		}
		return out
	}
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
	m.items = out
	sel := -1
	for i, it := range out {
		if key(it) == m.selKey {
			sel = i
		}
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
	Selected string `json:"selected"`
}

func (m *model) statePath() string {
	return filepath.Join(config.StateDir(), "tui-"+strings.Join(m.spheres, "+")+".json")
}

func (m *model) persist() {
	s := savedState{View: m.sourceName(), Horizon: horizons[m.horizon], ShowDone: m.showDone, DetailOn: m.detailOn, Selected: m.selKey}
	b, _ := json.Marshal(s)
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
}
