// Package tui is due's terminal interface: the unified list of a sphere,
// the detail of a line, and the changes of the ledger.
package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/aclemen1/due-cli/internal/actions"
	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/connect"
	"github.com/aclemen1/due-cli/internal/spec"
	"github.com/aclemen1/due-cli/internal/trigger"
	"github.com/aclemen1/due-cli/internal/when"
)

func init() {
	spec.Register(&spec.Action{
		Category: "setup", Name: "tui", Top: true,
		Summary:  "Open the terminal interface on a sphere: the unified list, the detail of a line, the changes of the ledger.",
		Params:   []spec.Param{{Name: "sphere", Kind: spec.String, Help: "Sphere to show. Defaults to $DUE_SPHERE."}},
		Effects:  []string{"Runs until q; every change goes through the same actions as the CLI."},
		Examples: []string{"due tui --sphere perso"},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, err := config.Load(ctx.Config)
			if err != nil {
				return nil, err
			}
			sphere, err := actions.SphereOf(ctx, cfg)
			if err != nil {
				return nil, err
			}
			_, err = tea.NewProgram(newModel(ctx.Config, cfg, sphere)).Run()
			return spec.Streamed{}, err
		},
	})
}

type view int

const (
	vList view = iota
	vDetail
)

type prompt int

const (
	pNone prompt = iota
	pFilter
	pAddTitle
	pAddAt
	pAddNotice
	pSnooze
	pConfirmDrop
	pConfirmRun
	pConfirmRm
)

var horizons = []string{"7d", "30d", "90d", "365d"}

type model struct {
	cfgPath string
	cfg     *config.Config
	sphere  string
	now     func() time.Time

	view    view
	listing *actions.Listing
	loading bool
	items   []connect.Item // after the source and text filters
	sel     int
	top     int

	horizon  int
	source   int // index in views(): 0 the ledger, 1 everything, then each connector
	filter   string
	showDone bool

	detail     *actions.Detail
	detailItem connect.Item
	scroll     int

	prompt prompt
	input  textinput.Model
	draft  map[string]any

	w, h      int
	status    string
	statusErr bool
	helpOn    bool
}

func newModel(cfgPath string, cfg *config.Config, sphere string) *model {
	in := textinput.New()
	in.Prompt = ""
	m := &model{cfgPath: cfgPath, cfg: cfg, sphere: sphere, now: actions.Now, input: in, w: 100, h: 30, horizon: 1}
	for i, x := range horizons {
		if x == cfg.Spheres[sphere].Horizon {
			m.horizon = i
		}
	}
	return m
}

type loadedMsg struct {
	listing *actions.Listing
	err     error
}

type doneMsg struct {
	status string
	err    error
}

type detailMsg struct {
	d   *actions.Detail
	err error
}

// views are the sources shown in turn by s: the ledger alone, everything, then each connector.
func (m *model) views() []string {
	return append([]string{"registre", "toutes"}, actions.SourceNames(m.cfg.Spheres[m.sphere])[1:]...)
}

func (m *model) sourceName() string { return m.views()[m.source] }

// ledgerOnly: the ledger at any date, without connectors and horizon.
func (m *model) ledgerOnly() bool { return m.source == 0 }

func (m *model) ctx(args map[string]any) *spec.Context {
	args["sphere"] = m.sphere
	return &spec.Context{Args: args, Config: m.cfgPath, Format: "json"}
}

func (m *model) load() tea.Cmd {
	m.loading = true
	q := actions.Query{Until: horizons[m.horizon]}
	if m.ledgerOnly() {
		q = actions.Query{Until: "36500d", Sources: []string{"due"}, All: m.showDone}
	}
	ctx := m.ctx(map[string]any{})
	return func() tea.Msg {
		cfg, l, err := actions.Open(ctx)
		if err != nil {
			return loadedMsg{err: err}
		}
		res, err := actions.List(cfg, l, q)
		return loadedMsg{listing: res, err: err}
	}
}

// act runs an action of the ledger, as `due <name>` would.
func (m *model) act(name string, args map[string]any, status string) tea.Cmd {
	args["sphere"] = m.sphere
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
		if f, ok := res.(trigger.Fired); ok {
			status += " : " + f.Result
		}
		return doneMsg{status: status}
	}
}

func (m *model) loadDetail(id string) tea.Cmd {
	ctx := m.ctx(map[string]any{"id": id})
	return func() tea.Msg {
		res, err := spec.Find("due", "show").Run(ctx)
		if err != nil {
			return detailMsg{err: err}
		}
		d := res.(actions.Detail)
		return detailMsg{d: &d}
	}
}

func (m *model) Init() tea.Cmd { return tea.Batch(tea.RequestBackgroundColor, m.load()) }

func (m *model) setStatus(s string, isErr bool) { m.status, m.statusErr = s, isErr }

// apply filters the listing by source and text.
func (m *model) apply() {
	m.items = nil
	if m.listing == nil {
		return
	}
	src := ""
	switch {
	case m.ledgerOnly():
		src = "due"
	case m.source > 1:
		src = m.sourceName()
	}
	needle := strings.ToLower(m.filter)
	for _, it := range m.listing.Items {
		if src != "" && it.Source != src {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(it.Title+" "+it.Detail+" "+it.ID+" "+it.Source), needle) {
			continue
		}
		m.items = append(m.items, it)
	}
	m.sel = max(0, min(m.sel, len(m.items)-1))
}

func (m *model) current() (connect.Item, bool) {
	if m.view == vDetail {
		return m.detailItem, true
	}
	if m.sel < 0 || m.sel >= len(m.items) {
		return connect.Item{}, false
	}
	return m.items[m.sel], true
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.input.SetWidth(max(10, m.w-40))
	case tea.BackgroundColorMsg:
		darkBackground = msg.IsDark()
	case loadedMsg:
		m.loading = false
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
			return m, nil
		}
		m.listing = msg.listing
		m.apply()
		if m.view == vDetail {
			for _, it := range m.listing.Items {
				if it.Source == m.detailItem.Source && it.ID == m.detailItem.ID {
					m.detailItem = it
				}
			}
		}
		if len(msg.listing.Errors) > 0 && !m.statusErr {
			var names []string
			for _, e := range msg.listing.Errors {
				names = append(names, e.Source)
			}
			m.setStatus("sources en erreur : "+strings.Join(names, ", ")+" (? pour le détail)", true)
		}
	case detailMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
			return m, nil
		}
		m.detail = msg.d
	case doneMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
			return m, nil
		}
		m.setStatus(msg.status, false)
		cmds := []tea.Cmd{m.load()}
		if m.view == vDetail && m.detailItem.Type == "due" {
			cmds = append(cmds, m.loadDetail(m.detailItem.ID))
		}
		return m, tea.Batch(cmds...)
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.prompt != pNone {
			return m, m.keyPrompt(msg)
		}
		m.status, m.statusErr = "", false
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "?":
			m.helpOn = !m.helpOn
			return m, nil
		}
		if m.view == vDetail {
			return m, m.keyDetail(msg)
		}
		return m, m.keyList(msg)
	}
	return m, nil
}

func (m *model) ask(p prompt, placeholder, value string) tea.Cmd {
	m.prompt = p
	m.input.Placeholder = placeholder
	m.input.SetValue(value)
	m.input.CursorEnd()
	return m.input.Focus()
}

func (m *model) keyList(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "up", "k":
		m.sel = max(0, m.sel-1)
	case "down", "j":
		m.sel = max(0, min(len(m.items)-1, m.sel+1))
	case "pgup":
		m.sel = max(0, m.sel-m.bodyHeight())
	case "pgdown":
		m.sel = max(0, min(len(m.items)-1, m.sel+m.bodyHeight()))
	case "home", "g":
		m.sel = 0
	case "end", "G":
		m.sel = max(0, len(m.items)-1)
	case "esc":
		switch {
		case m.filter != "":
			m.filter = ""
		case !m.ledgerOnly():
			m.source, m.sel = 0, 0
			return m.load()
		}
		m.apply()
	case "/":
		return m.ask(pFilter, "texte à chercher", m.filter)
	case "h":
		if m.ledgerOnly() {
			m.setStatus("le registre montre toutes les dates ; s pour la vue avec horizon", false)
			return nil
		}
		m.horizon = (m.horizon + 1) % len(horizons)
		return m.load()
	case "f":
		if !m.ledgerOnly() {
			return nil
		}
		m.showDone = !m.showDone
		return m.load()
	case "s":
		was := m.ledgerOnly()
		m.source = (m.source + 1) % len(m.views())
		m.sel = 0
		if was != m.ledgerOnly() {
			return m.load()
		}
		m.apply()
	case "r":
		return m.load()
	case "a":
		m.draft = map[string]any{}
		return m.ask(pAddTitle, "titre de l'échéance", "")
	case "enter", "right", "l":
		it, ok := m.current()
		if !ok {
			return nil
		}
		m.view, m.scroll, m.detail, m.detailItem = vDetail, 0, nil, it
		if it.Type == "due" {
			return m.loadDetail(it.ID)
		}
	default:
		return m.keyEntry(k)
	}
	return nil
}

// keyEntry handles the changes of a ledger entry, from the list or the detail.
func (m *model) keyEntry(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	if !strings.Contains("dxzeRD", key) || len(key) != 1 {
		return nil
	}
	it, ok := m.current()
	if !ok {
		return nil
	}
	if it.Type != "due" {
		m.setStatus("ligne de "+it.Source+" : elle se modifie dans "+it.Type, true)
		return nil
	}
	switch key {
	case "d":
		if it.State != "open" {
			return m.act("reopen", map[string]any{"id": it.ID}, it.ID+" rouverte")
		}
		return m.act("done", map[string]any{"id": it.ID}, it.ID+" faite")
	case "x":
		return m.ask(pConfirmDrop, "o pour abandonner "+it.ID, "")
	case "D":
		return m.ask(pConfirmRm, "o pour supprimer le fichier de "+it.ID, "")
	case "R":
		return m.ask(pConfirmRun, "o pour exécuter maintenant l'action de "+it.ID, "")
	case "z":
		return m.ask(pSnooze, "report : 1d, 7d, ou une date", "1d")
	case "e":
		return m.editFile(it.ID)
	}
	return nil
}

func (m *model) editFile(id string) tea.Cmd {
	s := m.cfg.Spheres[m.sphere]
	path := filepath.Join(s.Root, id+".md")
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command("sh", "-c", editor+` "$1"`, "sh", path)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return doneMsg{err: err}
		}
		if s.VCS == "jj" {
			c := exec.Command("jj", "commit", "-m", "edit "+id+" in $EDITOR")
			c.Dir = s.Root
			_ = c.Run()
		}
		return doneMsg{status: id + " modifiée"}
	})
}

func (m *model) keyDetail(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc", "left":
		m.view = vList
	case "up", "k":
		m.scroll = max(0, m.scroll-1)
	case "down", "j":
		m.scroll++
	default:
		return m.keyEntry(k)
	}
	return nil
}

func (m *model) keyPrompt(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		if m.prompt == pFilter {
			m.filter = ""
			m.apply()
		}
		m.prompt = pNone
		m.input.Blur()
		return nil
	case "enter":
		v := strings.TrimSpace(m.input.Value())
		p := m.prompt
		m.prompt = pNone
		m.input.Blur()
		return m.submit(p, v)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	if m.prompt == pFilter {
		m.filter = m.input.Value()
		m.sel = 0
		m.apply()
	}
	return cmd
}

func (m *model) submit(p prompt, v string) tea.Cmd {
	yes := strings.EqualFold(v, "o") || strings.EqualFold(v, "oui") || strings.EqualFold(v, "y")
	it, _ := m.current()
	switch p {
	case pFilter:
		m.filter = v
		m.apply()
	case pAddTitle:
		if v == "" {
			return nil
		}
		m.draft["title"] = v
		return m.ask(pAddAt, "date : 2026-11-15, 15.11.2026 14:00, demain, 3d", "")
	case pAddAt:
		if v == "" {
			return nil
		}
		if _, err := when.ParseMoment(v, m.now()); err != nil {
			m.setStatus(err.Error(), true)
			return m.ask(pAddAt, "date : 2026-11-15, 15.11.2026 14:00, demain, 3d", v)
		}
		m.draft["at"] = v
		return m.ask(pAddNotice, "préavis, facultatif : 7d,1d", "")
	case pAddNotice:
		if v != "" {
			m.draft["notice"] = []string{v}
		}
		return m.act("add", m.draft, "échéance ajoutée")
	case pSnooze:
		if v == "" {
			return nil
		}
		args := map[string]any{"id": it.ID}
		if _, err := when.ParseDuration(v); err == nil {
			args["by"] = v
		} else {
			args["to"] = v
		}
		return m.act("snooze", args, it.ID+" reportée")
	case pConfirmDrop:
		if yes {
			return m.act("drop", map[string]any{"id": it.ID}, it.ID+" abandonnée")
		}
	case pConfirmRm:
		if yes {
			m.view = vList
			return m.act("rm", map[string]any{"id": it.ID}, it.ID+" supprimée")
		}
	case pConfirmRun:
		if yes {
			return m.act("run", map[string]any{"id": it.ID}, it.ID+" exécutée")
		}
	}
	return nil
}

func (m *model) bodyHeight() int {
	h := m.h - 3
	if m.helpOn {
		h -= len(m.helpLines()) + 1
	}
	return max(3, h)
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
