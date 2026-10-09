// Package tui is due's terminal interface: the ledger first, then every
// source; a detail panel that follows the selection; a form to add and edit;
// live updates from the files of the sources.
package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/aclemen1/due-cli/internal/actions"
	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/connect"
	"github.com/aclemen1/due-cli/internal/spec"
	"github.com/aclemen1/due-cli/internal/watch"
	"github.com/aclemen1/due-cli/internal/when"
)

func init() {
	spec.Register(&spec.Action{
		Category: "setup", Name: "tui", Top: true,
		Summary:  "Open the terminal interface: the ledgers, every source, the detail of a line, a form to add and edit.",
		Params:   []spec.Param{{Name: "sphere", Kind: spec.String, Help: "Show only this sphere. Defaults to $DUE_SPHERE, else every sphere."}},
		Effects:  []string{"Runs until q; every change goes through the same actions as the CLI."},
		Examples: []string{"due tui", "due tui --sphere pro"},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, err := config.Load(ctx.Config)
			if err != nil {
				return nil, err
			}
			spheres, err := actions.ReadSpheres(ctx, cfg)
			if err != nil {
				return nil, err
			}
			m := newModel(ctx.Config, cfg, spheres)
			var roots []watch.Root
			for _, sp := range spheres {
				roots = append(roots, watch.Roots(cfg.Spheres[sp], sp)...)
			}
			if w, err := watch.Start(roots, 300*time.Millisecond); err == nil {
				m.watcher = w
				defer w.Close()
			}
			var domains []string
			for _, sp := range spheres {
				for _, d := range watch.MacosDomains(cfg.Spheres[sp]) {
					if !contains(domains, d) {
						domains = append(domains, d)
					}
				}
			}
			if len(domains) > 0 {
				if mw, err := watch.StartMacos("", domains); err == nil {
					m.macos = mw
					defer mw.Close()
				}
			}
			m.restore()
			m.bin, _ = ownBinary()
			m.signals = startSignals()
			if s := reloadedStatus(); s != "" {
				m.setStatus(s, false)
			}
			_, err = tea.NewProgram(m).Run()
			if err == nil && m.reloading {
				if m.watcher != nil {
					m.watcher.Close()
				}
				if m.macos != nil {
					m.macos.Close()
				}
				return spec.Streamed{}, execAgain(m.bin.path)
			}
			return spec.Streamed{}, err
		},
	})
}

type prompt int

const (
	pNone prompt = iota
	pFilter
	pSnooze
	pConfirmDrop
	pConfirmRun
	pConfirmRm
)

var horizons = []string{"7d", "30d", "90d", "365d"}

const (
	pollEvery  = 60 * time.Second
	fullEvery  = 10 * time.Minute
	statusLife = 5 * time.Second
)

type model struct {
	cfgPath string
	cfg     *config.Config
	spheres []string
	now     func() time.Time
	watcher *watch.Watcher
	macos   *watch.Macos // Reminders and Calendar changes, from macos watch
	macosOn bool         // macos watch is live: no polling of those sources

	// what is shown
	source   int // index in views(): 0 the ledger, 1 everything, then each connector
	horizon  int
	filter   string
	showDone bool
	critOnly bool
	detailOn bool
	helpOn   bool

	// data
	ledger   []connect.Item
	details  map[string]*actions.Detail
	conn     map[string][]connect.Item
	connErr  map[string]string
	loading  map[string]bool
	loadedAt time.Time
	lastFull time.Time
	ready    bool

	// list
	items   []connect.Item
	sel     int
	selKey  string
	top     int
	scroll  int
	rowItem []int // for each list row on screen: the item index, or -1

	prompt prompt
	input  textinput.Model
	form   *form

	w, h      int
	status    string
	statusErr bool
	statusAt  time.Time
	saved     []byte // the state last written

	// reload after a rebuild or SIGUSR1, once at rest
	bin          binStamp
	signals      chan os.Signal
	reloadWanted bool
	reloading    bool
	spin         int

	// geometry of the last frame, for the mouse
	listY, listH, listW, tabsY int
	tabX                       []int
}

func newModel(cfgPath string, cfg *config.Config, spheres []string) *model {
	in := textinput.New()
	in.Prompt = ""
	m := &model{cfgPath: cfgPath, cfg: cfg, spheres: spheres, now: actions.Now, input: in, w: 100, h: 30, horizon: 1,
		detailOn: true, details: map[string]*actions.Detail{}, conn: map[string][]connect.Item{},
		connErr: map[string]string{}, loading: map[string]bool{}}
	for i, x := range horizons {
		if x == cfg.Spheres[spheres[0]].Horizon {
			m.horizon = i
		}
	}
	return m
}

// conn is a connector of a sphere; its key is <sphere>/<name>.
type conn struct {
	sphere string
	c      config.Connector
}

func (c conn) key() string { return c.sphere + "/" + c.c.Name }

func (m *model) connectors() []conn {
	var out []conn
	for _, sp := range m.spheres {
		for _, c := range m.cfg.Spheres[sp].Connectors {
			if !c.Off {
				out = append(out, conn{sp, c})
			}
		}
	}
	return out
}

// keysOf are the connectors of every sphere that carry a name.
func (m *model) keysOf(name string) []string {
	var out []string
	for _, c := range m.connectors() {
		if c.c.Name == name {
			out = append(out, c.key())
		}
	}
	return out
}

func (m *model) multi() bool { return len(m.spheres) > 1 }

// views are the sources shown in turn by s: the ledger alone, everything, then each connector.
func (m *model) views() []string {
	out := []string{"registre", "toutes"}
	for _, c := range m.connectors() {
		if !contains(out, c.c.Name) {
			out = append(out, c.c.Name)
		}
	}
	return out
}

func (m *model) sourceName() string { return m.views()[m.source] }

// ledgerOnly: the ledger at any date, without connectors and horizon.
func (m *model) ledgerOnly() bool { return m.source == 0 }

func (m *model) ctx(args map[string]any) *spec.Context {
	return &spec.Context{Args: args, Config: m.cfgPath, Format: "json"}
}

func (m *model) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor, m.loadLedger(), m.loadAll(), tick(), poll(), checkBin()}
	if m.signals != nil {
		cmds = append(cmds, waitSignal(m.signals))
	}
	if m.macos != nil {
		cmds = append(cmds, waitMacos(m.macos))
	}
	if m.watcher != nil {
		cmds = append(cmds, waitWatch(m.watcher))
	}
	return tea.Batch(cmds...)
}

func (m *model) setStatus(s string, isErr bool) {
	m.status, m.statusErr, m.statusAt = s, isErr, m.now()
}

func (m *model) current() (connect.Item, bool) {
	if m.sel < 0 || m.sel >= len(m.items) {
		return connect.Item{}, false
	}
	return m.items[m.sel], true
}

func key(it connect.Item) string { return it.Sphere + "\x00" + it.Source + "\x00" + it.ID }

func (m *model) selectIndex(i int) {
	if len(m.items) == 0 {
		m.sel = 0 // keep selKey: the line may come with the next read
		return
	}
	i = max(0, min(i, len(m.items)-1))
	if i != m.sel {
		m.scroll = 0
	}
	m.sel, m.selKey = i, key(m.items[i])
}

// Update handles a message, then keeps the state for the next start.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	if m.ready {
		m.persist()
	}
	return next, cmd
}

func (m *model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.input.SetWidth(max(10, m.w-30))
		if m.form != nil {
			m.form.resize(m.w)
		}
	case tea.BackgroundColorMsg:
		darkBackground = msg.IsDark()
	case macosMsg:
		return m, m.onMacos(watch.MacosEvent(msg))
	case binCheckMsg:
		return m, m.onBinCheck()
	case signalMsg:
		m.reloadWanted = true
		if cmd := m.maybeReload(); cmd != nil {
			return m, cmd
		}
		return m, waitSignal(m.signals)
	case tickMsg:
		if cmd := m.maybeReload(); cmd != nil {
			return m, cmd
		}
		m.spin++
		if m.status != "" && !m.statusErr && m.now().Sub(m.statusAt) > statusLife {
			m.status = ""
		}
		return m, tick()
	case pollMsg:
		var cmds []tea.Cmd
		if m.now().Sub(m.lastFull) > fullEvery {
			cmds = append(cmds, m.loadLedger(), m.loadAll())
		} else {
			for _, c := range m.connectors() {
				if m.macosOn && (c.c.Type == "reminders" || c.c.Type == "calendar") {
					continue
				}
				if c.c.Type == "reminders" || c.c.Type == "calendar" || c.c.Type == "command" {
					cmds = append(cmds, m.loadConn(c.key()))
				}
			}
		}
		return m, tea.Batch(append(cmds, poll())...)
	case watchMsg:
		var cmd tea.Cmd
		if strings.HasSuffix(msg.source, "/due") {
			cmd = m.loadLedger()
		} else {
			cmd = m.loadConn(msg.source)
		}
		return m, tea.Batch(cmd, waitWatch(m.watcher))
	case ledgerMsg:
		m.loading["due"] = false
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
			return m, nil
		}
		m.ledger, m.details, m.loadedAt, m.ready = msg.items, msg.details, m.now(), true
		m.apply()
	case connMsg:
		m.loading[msg.name] = false
		if msg.err != nil {
			m.connErr[msg.name] = msg.err.Error()
		} else {
			delete(m.connErr, msg.name)
			m.conn[msg.name] = msg.items
		}
		m.loadedAt = m.now()
		m.apply()
	case doneMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
			return m, nil
		}
		if msg.status != "" {
			m.setStatus(msg.status, false)
		}
		if msg.select_ != "" {
			m.selKey = msg.select_
		}
		return m, m.loadLedger()
	case tea.MouseMsg:
		if m.form == nil && m.prompt == pNone {
			return m, m.mouse(msg)
		}
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.form != nil {
			return m, m.keyForm(msg)
		}
		if m.prompt != pNone {
			return m, m.keyPrompt(msg)
		}
		if m.statusErr {
			m.status, m.statusErr = "", false
		}
		if msg.String() == "q" {
			m.persist()
			return m, tea.Quit
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

func (m *model) setView(i int) tea.Cmd {
	m.source = (i + len(m.views())) % len(m.views())
	m.top, m.scroll = 0, 0
	m.apply()
	m.persist()
	return nil
}

func (m *model) keyList(k tea.KeyPressMsg) tea.Cmd {
	page := max(1, m.listH-2)
	switch s := k.String(); s {
	case "up", "k":
		m.selectIndex(m.sel - 1)
	case "down", "j":
		m.selectIndex(m.sel + 1)
	case "pgup", "ctrl+b":
		m.selectIndex(m.sel - page)
	case "pgdown", "ctrl+f", "space":
		m.selectIndex(m.sel + page)
	case "home", "g":
		m.selectIndex(0)
	case "end", "G":
		m.selectIndex(len(m.items) - 1)
	case "J", "ctrl+d":
		m.scroll += 3
	case "K", "ctrl+u":
		m.scroll = max(0, m.scroll-3)
	case "esc":
		switch {
		case m.helpOn:
			m.helpOn = false
		case m.filter != "":
			m.filter = ""
			m.apply()
		case m.critOnly:
			m.critOnly = false
			m.apply()
		case !m.ledgerOnly():
			return m.setView(0)
		}
	case "?":
		m.helpOn = !m.helpOn
	case "tab":
		m.detailOn = !m.detailOn
		m.persist()
	case "/":
		return m.ask(pFilter, "titre, détail, source…", m.filter)
	case "s", "right", "l":
		return m.setView(m.source + 1)
	case "S", "left", "h":
		return m.setView(m.source - 1)
	case "H":
		m.horizon = (m.horizon + 1) % len(horizons)
		m.persist()
		if m.ledgerOnly() {
			m.setStatus("horizon "+horizons[m.horizon]+" (vue toutes et connecteurs)", false)
		}
		return m.loadAll()
	case "c":
		m.critOnly = !m.critOnly
		m.apply()
		if m.critOnly {
			m.setStatus("lignes critiques seulement (c pour tout revoir)", false)
		}
	case "f":
		m.showDone = !m.showDone
		m.persist()
		return m.loadLedger()
	case "r":
		m.setStatus("relecture de toutes les sources", false)
		return tea.Batch(m.loadLedger(), m.loadAll())
	case "a":
		m.form = newForm(m, nil)
		return m.form.focus()
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		n, _ := strconv.Atoi(s)
		if n <= len(m.views()) {
			return m.setView(n - 1)
		}
	default:
		return m.keyEntry(s)
	}
	return nil
}

// keyEntry handles the actions on the selected line.
func (m *model) keyEntry(key string) tea.Cmd {
	it, ok := m.current()
	if !ok {
		return nil
	}
	if key == "o" || key == "enter" {
		return m.open(it)
	}
	if !strings.Contains("dxzeERD", key) || len(key) != 1 {
		return nil
	}
	if it.Type != "due" {
		m.setStatus("cette ligne vient de "+it.Source+" : elle se modifie dans "+it.Type+" (o pour l'ouvrir)", true)
		return nil
	}
	switch key {
	case "d":
		if it.State != "open" {
			return m.act("reopen", map[string]any{"id": it.ID, "sphere": it.Sphere}, it.ID+" rouverte")
		}
		return m.act("done", map[string]any{"id": it.ID, "sphere": it.Sphere}, it.ID+" faite")
	case "x":
		return m.ask(pConfirmDrop, "o pour abandonner « "+it.Title+" »", "")
	case "D":
		return m.ask(pConfirmRm, "o pour supprimer définitivement « "+it.Title+" »", "")
	case "R":
		return m.ask(pConfirmRun, "o pour exécuter maintenant l'action de « "+it.Title+" »", "")
	case "z":
		return m.ask(pSnooze, "1d, 7d, 2w, ou une date", "7d")
	case "e":
		if d := m.details[it.ID]; d != nil {
			m.form = newForm(m, d)
			return m.form.focus()
		}
	case "E":
		return m.editFile(it)
	}
	return nil
}

// open shows a line in its own tool: the dossier's session for office.
func (m *model) open(it connect.Item) tea.Cmd {
	switch it.Type {
	case "due":
		m.detailOn = true
		return nil
	case "office":
		for _, c := range m.connectors() {
			if c.c.Name == it.Source && c.sphere == it.Sphere {
				dir := c.c.Office
				return func() tea.Msg {
					args := []string{"goto", it.ID}
					if dir != "" {
						args = append(args, "--office", dir)
					}
					out, err := exec.Command("office", args...).CombinedOutput()
					if err != nil {
						return doneMsg{err: errorf("office goto %s : %s", it.ID, strings.TrimSpace(string(out)))}
					}
					return doneMsg{status: "dossier " + it.ID + " ouvert"}
				}
			}
		}
	}
	m.detailOn = true
	m.setStatus("rien à ouvrir pour une ligne de "+it.Type+" : voir le détail", false)
	return nil
}

func (m *model) editFile(it connect.Item) tea.Cmd {
	id := it.ID
	s := m.cfg.Spheres[it.Sphere]
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
		m.apply()
		m.selectIndex(0)
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
	case pSnooze:
		if v == "" {
			return nil
		}
		args := map[string]any{"id": it.ID, "sphere": it.Sphere}
		if _, err := when.ParseDuration(v); err == nil {
			args["by"] = v
		} else {
			args["to"] = v
		}
		return m.act("snooze", args, it.ID+" reportée")
	case pConfirmDrop:
		if yes {
			return m.act("drop", map[string]any{"id": it.ID, "sphere": it.Sphere}, it.ID+" abandonnée")
		}
	case pConfirmRm:
		if yes {
			return m.act("rm", map[string]any{"id": it.ID, "sphere": it.Sphere}, it.ID+" supprimée")
		}
	case pConfirmRun:
		if yes {
			return m.act("run", map[string]any{"id": it.ID, "sphere": it.Sphere}, it.ID+" exécutée")
		}
	}
	return nil
}

// mouse: a click selects a row or a view tab, the wheel moves the list or
// scrolls the detail.
func (m *model) mouse(ev tea.MouseMsg) tea.Cmd {
	mm := ev.Mouse()
	switch ev.(type) {
	case tea.MouseWheelMsg:
		step := 1
		if mm.Button == tea.MouseWheelUp {
			step = -1
		}
		if mm.X < m.listW && mm.Y >= m.listY && mm.Y < m.listY+m.listH {
			m.selectIndex(m.sel + step)
		} else {
			m.scroll = max(0, m.scroll+step*2)
		}
	case tea.MouseClickMsg:
		if mm.Button != tea.MouseLeft {
			return nil
		}
		if mm.Y == m.tabsY {
			for i := len(m.tabX) - 1; i >= 0; i-- {
				if mm.X >= m.tabX[i] {
					return m.setView(i)
				}
			}
			return nil
		}
		row := mm.Y - m.listY
		if mm.X < m.listW && row >= 0 && row < len(m.rowItem) && m.rowItem[row] >= 0 {
			if m.rowItem[row] == m.sel {
				m.detailOn = true
			}
			m.selectIndex(m.rowItem[row])
		}
	}
	return nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
