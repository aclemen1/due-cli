package tui

import (
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/aclemen1/tuikit"
)

// The background work of the TUI (the reads of the ledger and of the sources, every write,
// office goto) runs under tuikit.Busy: a spinner in the header while it runs, its end a few
// seconds, a failure in red until the list (!) is opened.

// afterJobs holds what a job hands to the TUI once Busy has recorded its end.
type afterJobs struct {
	mu   sync.Mutex
	msgs []tea.Msg
}

func (a *afterJobs) push(msg tea.Msg) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.msgs = append(a.msgs, msg)
}

func (a *afterJobs) take() []tea.Msg {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.msgs
	a.msgs = nil
	return out
}

// loadFail is a read that failed: an error for Busy, then the TUI forgets the read under way.
type loadFail struct {
	name string // "due" for the ledger, else the key of a connector
	err  error
}

func (f loadFail) Error() string { return f.err.Error() }

// run starts fn under label; fn returns the end text, the message for the TUI once it is done
// (nil for none), or an error.
func (m *model) run(label string, fn func() (string, tea.Msg, error)) tea.Cmd {
	a := m.after
	return m.busy.Run(label, func() (string, error) {
		text, then, err := fn()
		if then != nil {
			a.push(then)
		}
		return text, err
	})
}

// busyUpdate takes the messages of Busy, then the messages of the jobs that ended.
func (m *model) busyUpdate(msg tea.Msg) (tea.Cmd, bool) {
	cmd, ok := m.busy.Update(msg)
	if !ok {
		return nil, false
	}
	cmds := []tea.Cmd{cmd}
	for _, x := range m.after.take() {
		cmds = append(cmds, func() tea.Msg { return x })
	}
	return tea.Batch(cmds...), true
}

// openBusy opens the list of the recent jobs (!).
func (m *model) openBusy() {
	m.modal = tuikit.NewModal("Travaux", m.busy.List()).SetSize(m.w, m.h)
}
