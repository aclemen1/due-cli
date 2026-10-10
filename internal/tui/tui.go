// Package tui is due's terminal interface: the ledger first, then every
// source; a detail panel that follows the selection; a form to add and edit;
// live updates from the files of the sources.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/aclemen1/tuikit"
	"github.com/aclemen1/tuikit/complete"

	"github.com/aclemen1/due-cli/internal/actions"
	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/connect"
	"github.com/aclemen1/due-cli/internal/ledger"
	"github.com/aclemen1/due-cli/internal/spec"
	"github.com/aclemen1/due-cli/internal/watch"
)

func init() {
	spec.Register(&spec.Action{
		Category: "setup", Name: "tui", Top: true,
		Summary: "Open the terminal interface: the ledgers, every source, the detail of a line, a form to add and edit.",
		Params: []spec.Param{
			{Name: "sphere", Kind: spec.String, Help: "Show only this sphere. Defaults to $DUE_SPHERE, else every sphere."},
			{Name: "select", Kind: spec.String, Help: "Open on this line, selected and visible: an entry (PE-0003) or a line of a connector (01M44DR8KNBNK912RHEY2FBQS3#0). The TUI moves to the view that holds it; an unknown id opens it as usual, with a message."},
		},
		Effects:  []string{"Runs until q; every change goes through the same actions as the CLI."},
		Examples: []string{"due tui", "due tui --sphere pro", "due tui --select PE-0003"},
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
			var missing []string
			m.completers, missing = newCompleters(cfg)
			if len(missing) > 0 {
				m.setStatus("complétion des réfs : source(s) absente(s) de refs.yaml : "+strings.Join(missing, ", "), true)
			}
			var roots []watch.Root
			for _, sp := range spheres {
				roots = append(roots, watch.Roots(cfg.Spheres[sp], sp)...)
			}
			for _, d := range cfg.Notes.Dirs {
				roots = append(roots, watch.Root{Source: spheres[0] + "/due", Dir: config.Expand(d), Depth: 0, Keep: func(p string) bool { return strings.HasSuffix(p, ".md") }})
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
			m.pendingSelect = strings.TrimSpace(ctx.Str("select"))
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
	source        int // index in views(): 0 the ledger, 1 everything, then each connector
	horizon       int
	filter        string
	showDone      bool
	critOnly      bool
	sphereOnly    string // one sphere among those shown, or all
	sortBy        int    // index in sorts
	sortRev       bool
	pendingG      bool   // g pressed once: gg goes to the top
	pendingSelect string // --select: the line to open on, once it is read
	detailOn      bool
	helpOn        bool

	// data
	ledger     []connect.Item
	details    map[string]*actions.Detail
	acks       map[string]bool
	completers []*complete.Completer // refs proposed to the Réfs field
	conn       map[string][]connect.Item
	connErr    map[string]string
	loading    map[string]bool
	loadedAt   time.Time
	lastFull   time.Time
	ready      bool

	// list
	items   []connect.Item
	sel     int
	selKey  string
	top     int
	scroll  int
	rowItem []int // for each list row on screen: the item index, or -1

	prompt  prompt
	input   textinput.Model
	modal   *tuikit.Modal   // the open input, if any: it takes every key
	editing *actions.Detail // the entry the form edits; nil adds one
	target  connect.Item    // the line a snooze or a confirmation is about

	w, h      int
	status    string // what the last key did, not a job: jobs go to busy
	statusErr bool
	statusAt  time.Time
	busy      *tuikit.Busy // the background jobs (busy.go)
	after     *afterJobs
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
		connErr: map[string]string{}, loading: map[string]bool{}, busy: tuikit.NewBusy(), after: &afterJobs{}}
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
	if cmd, ok := m.busyUpdate(msg); ok {
		return m, cmd
	}
	// An open modal takes every key, paste and click: no shortcut of the TUI fires.
	if m.modal.Open() {
		switch msg.(type) {
		case tickMsg, pollMsg, watchMsg, ledgerMsg, connMsg, doneMsg, notesMsg, macosMsg, binCheckMsg, signalMsg, error,
			tuikit.DoneMsg, tuikit.CancelMsg, tea.BackgroundColorMsg, tea.WindowSizeMsg:
		default:
			return m, m.modal.Update(msg)
		}
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.input.SetWidth(max(10, m.w-30))
		if m.modal.Open() {
			return m, m.modal.Update(msg)
		}
	case tea.BackgroundColorMsg:
		darkBackground = msg.IsDark()
		tuikit.SetDarkBackground(msg.IsDark())
	case tuikit.DoneMsg:
		m.modal = nil
		return m, m.done(msg)
	case tuikit.CancelMsg:
		m.modal = nil
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
		m.ledger, m.details, m.acks, m.loadedAt, m.ready = msg.items, msg.details, msg.acks, m.now(), true
		m.apply()
		return m, tea.Batch(m.loadNotes(), m.trySelect())
	case notesMsg:
		for id, notes := range msg {
			if d := m.details[id]; d != nil {
				d.Notes = notes
			}
		}
	case loadFail:
		if msg.name == "due" {
			m.loading["due"] = false
			return m, nil
		}
		return m.update(connMsg{name: msg.name, err: msg.err})
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
		return m, m.trySelect()
	case doneMsg:
		// The end of the job shows in busy: the ledger is read again quietly, so it stays.
		if msg.select_ != "" {
			m.selKey = msg.select_
		}
		return m, m.readLedger()
	case tea.MouseMsg:
		if !m.modal.Open() && m.prompt == pNone {
			return m, m.mouse(msg)
		}
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
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

// keyList follows the ecosystem's key convention; older keys stay where they
// clash with none of it (a, d, D, S, arrows).
func (m *model) keyList(k tea.KeyPressMsg) tea.Cmd {
	page := max(1, m.listH-2)
	s := k.String()
	if m.pendingG && s == "c" {
		m.pendingG = false
		m.critOnly = !m.critOnly
		m.apply()
		if m.critOnly {
			m.setStatus("lignes critiques seulement (g c pour tout revoir)", false)
		}
		return nil
	}
	if s != "g" {
		m.pendingG = false
	}
	switch s {
	case "up", "k":
		m.selectIndex(m.sel - 1)
	case "down", "j":
		m.selectIndex(m.sel + 1)
	case "pgup", "ctrl+b":
		m.selectIndex(m.sel - page)
	case "pgdown", "ctrl+f":
		m.selectIndex(m.sel + page)
	case "g":
		if m.pendingG {
			m.pendingG = false
			m.selectIndex(0)
		} else {
			m.pendingG = true
		}
	case "home":
		m.selectIndex(0)
	case "end", "G":
		m.selectIndex(len(m.items) - 1)
	case "J", "ctrl+d":
		m.scroll += 3
	case "K", "ctrl+u":
		m.scroll = max(0, m.scroll-3)
	case "[":
		m.selectIndex(m.groupStart(m.sel, -1))
	case "]":
		m.selectIndex(m.groupStart(m.sel, 1))
	case "esc", "h":
		switch {
		case m.helpOn:
			m.helpOn = false
		case m.filter != "":
			m.filter = ""
			m.apply()
		case m.critOnly:
			m.critOnly = false
			m.apply()
		case m.sphereOnly != "":
			m.sphereOnly = ""
			m.apply()
		case !m.ledgerOnly():
			return m.setView(0)
		}
	case "?":
		m.helpOn = !m.helpOn
	case "tab":
		m.detailOn = !m.detailOn
	case "/":
		return m.ask(pFilter, "titre, détail, source…", m.filter)
	case "right":
		return m.setView(m.source + 1)
	case "left", "S":
		return m.setView(m.source - 1)
	case "s":
		m.cycleSphere()
	case "t":
		m.sortBy = (m.sortBy + 1) % len(sorts)
		m.apply()
		m.setStatus("tri par "+sorts[m.sortBy], false)
	case "T":
		m.sortRev = !m.sortRev
		m.apply()
		m.setStatus(map[bool]string{true: "tri inversé", false: "tri normal"}[m.sortRev], false)
	case "H":
		m.horizon = (m.horizon + 1) % len(horizons)
		if m.ledgerOnly() {
			m.setStatus("horizon "+horizons[m.horizon]+" (vue toutes et connecteurs)", false)
		}
		return m.loadAll()
	case tuikit.BusyKey:
		m.openBusy()
	case "f":
		m.showDone = !m.showDone
		return m.loadLedger()
	case "r":
		m.setStatus("relecture de toutes les sources", false)
		return tea.Batch(m.loadLedger(), m.loadAll())
	case "c", "a":
		m.openEntryForm(nil)
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
	switch key {
	case "o":
		return m.open(it)
	case "enter", "l":
		m.detailOn = true
		return nil
	case "W", "*":
		m.setStatus("sans objet dans due : une échéance n'attend personne et ne prend pas d'étoile", true)
		return nil
	}
	if !contains([]string{"e", "space", "d", "x", "#", "D", "R", "z", "E", "N"}, key) {
		return nil
	}
	if it.Type != "due" {
		line := map[string]any{"id": ledger.BaseID(it.ID), "source": it.Source, "sphere": it.Sphere}
		switch {
		case (key == "space" || key == "d") && it.State == "acked":
			return m.act("unack", line, "ligne rendue")
		case key == "e" || key == "space" || key == "d":
			return m.act("ack", line, "ligne retirée (f la montre, espace la rend)")
		}
		m.setStatus("cette ligne vient de "+it.Source+" : elle se modifie dans "+it.Type+" (o pour l'ouvrir ; e la retire de due)", true)
		return nil
	}
	ids := map[string]any{"id": it.ID, "sphere": it.Sphere}
	switch key {
	case "e":
		if it.State == "open" {
			return m.act("done", ids, it.ID+" faite")
		}
		m.setStatus(it.ID+" est déjà close (espace pour la rouvrir)", false)
	case "space", "d":
		if it.State != "open" {
			return m.act("reopen", ids, it.ID+" rouverte")
		}
		return m.act("done", ids, it.ID+" faite")
	case "x":
		m.openConfirm("drop", it, "Abandonner « "+it.Title+" » ?")
	case "#", "D":
		m.openConfirmRm(it)
	case "R":
		m.openConfirm("run", it, "Exécuter maintenant l'action de « "+it.Title+" » ?")
	case "z":
		m.openSnooze(it)
	case "E":
		if d := m.details[it.ID]; d != nil {
			m.openEntryForm(d)
		}
	case "N":
		m.target = it
		m.openModal("Note sur "+it.ID+" · "+trunc(it.Title, 40), tuikit.NewEditor("note", "Gardée par note, rattachée à due:"+it.ID, ""))
	}
	return nil
}

var sorts = []string{"date", "titre", "source"}

// cycleSphere shows every sphere, then each one alone.
func (m *model) cycleSphere() {
	if !m.multi() {
		m.setStatus("une seule sphère est montrée", false)
		return
	}
	order := append([]string{""}, m.spheres...)
	for i, s := range order {
		if s == m.sphereOnly {
			m.sphereOnly = order[(i+1)%len(order)]
			break
		}
	}
	m.apply()
	if m.sphereOnly == "" {
		m.setStatus("toutes les sphères", false)
	} else {
		m.setStatus("sphère "+m.sphereOnly+" seulement", false)
	}
}

// groupStart is the first line of the next (dir 1) or previous (dir -1)
// group: a period in the ledger, a day elsewhere.
func (m *model) groupStart(i, dir int) int {
	if len(m.items) == 0 {
		return 0
	}
	label := func(j int) string {
		if m.ledgerOnly() {
			return period(m.items[j], m.now())
		}
		return m.items[j].At.Format("2006-01-02")
	}
	cur := label(i)
	j := i
	if dir > 0 {
		for j < len(m.items)-1 && label(j) == cur {
			j++
		}
		return j
	}
	// back to the start of this group, then of the previous one
	for j > 0 && label(j-1) == cur {
		j--
	}
	if j == i && j > 0 {
		j--
		prev := label(j)
		for j > 0 && label(j-1) == prev {
			j--
		}
	}
	return j
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
				return m.run("ouvrir "+it.ID+" dans office", func() (string, tea.Msg, error) {
					args := []string{"goto", it.ID}
					if dir != "" {
						args = append(args, "--office", dir)
					}
					out, err := exec.Command("office", args...).CombinedOutput()
					if err != nil {
						return "", nil, errorf("office goto %s : %s", it.ID, strings.TrimSpace(string(out)))
					}
					return "dossier " + it.ID + " ouvert", nil, nil
				})
			}
		}
	}
	m.detailOn = true
	m.setStatus("rien à ouvrir pour une ligne de "+it.Type+" : voir le détail", false)
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
		m.apply()
		m.selectIndex(0)
	}
	return cmd
}

func (m *model) submit(p prompt, v string) tea.Cmd {
	switch p {
	case pFilter:
		m.filter = v
		m.apply()
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

// trySelect opens on the line of --select once the data holding it is read:
// the ledger, or everything for a line of a connector (widening the horizon
// once if needed).
func (m *model) trySelect() tea.Cmd {
	id := m.pendingSelect
	if id == "" || !m.ready {
		return nil
	}
	// An entry id may come short, as the CLI takes it: pe-7 is PE-0007.
	if g := shortID.FindStringSubmatch(strings.ToUpper(id)); g != nil {
		n, _ := strconv.Atoi(g[2])
		id = fmt.Sprintf("%sE-%04d", g[1], n)
	}
	match := func(it connect.Item) bool {
		return strings.EqualFold(it.ID, id) || strings.EqualFold(ledger.BaseID(it.ID), id)
	}
	pick := func(it connect.Item, view int) tea.Cmd {
		m.pendingSelect = ""
		m.source, m.filter, m.critOnly, m.sphereOnly = view, "", false, ""
		if it.State != "" && it.State != ledger.Open {
			m.showDone = true
		}
		if it.Type != "due" && m.acks[it.Sphere+"\x00"+ledger.AckKey(it.Source, it.ID, it.At.Format(time.RFC3339))] {
			m.showDone = true
		}
		m.selKey, m.top = key(it), 0
		m.apply()
		m.selectIndex(m.sel)
		m.detailOn = true
		return nil
	}
	for _, it := range m.ledger {
		if match(it) {
			return pick(it, 0)
		}
	}
	for _, items := range m.conn {
		for _, it := range items {
			if match(it) {
				return pick(it, 1)
			}
		}
	}
	if m.reading() {
		return nil
	}
	if m.horizon < len(horizons)-1 && len(m.connectors()) > 0 {
		m.horizon = len(horizons) - 1
		return m.loadAll()
	}
	m.pendingSelect = ""
	m.setStatus("--select : "+id+" introuvable dans le registre et les sources", true)
	return nil
}

var shortID = regexp.MustCompile(`^([A-Z]{1,3})E-0*(\d+)$`)
