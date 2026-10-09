package watch

import (
	"bufio"
	"encoding/json"
	"os/exec"
	"strings"

	"github.com/aclemen1/due-cli/internal/config"
)

// MacosEvent is a line of `macos watch`: ready, changed (re-read those
// domains) or reset (re-read everything); end when the stream stops.
type MacosEvent struct {
	Event   string   `json:"event"`
	Domains []string `json:"domains"`
	End     bool     `json:"-"`
}

// Macos follows Reminders and Calendar through `macos watch`; its stdin is a
// pipe, so it dies with due.
type Macos struct {
	C   chan MacosEvent
	cmd *exec.Cmd
}

// MacosDomains are the EventKit domains the sphere's connectors read.
func MacosDomains(s config.Sphere) []string {
	var out []string
	for _, c := range s.Connectors {
		if c.Off {
			continue
		}
		d := map[string]string{"reminders": "reminders", "calendar": "calendar"}[c.Type]
		if d != "" && !strings.Contains(strings.Join(out, ","), d) {
			out = append(out, d)
		}
	}
	return out
}

func StartMacos(bin string, domains []string) (*Macos, error) {
	if bin == "" {
		bin = "macos"
	}
	cmd := exec.Command(bin, "watch", "--domains", strings.Join(domains, ","), "--debounce", "1s")
	if _, err := cmd.StdinPipe(); err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	m := &Macos{C: make(chan MacosEvent, 8), cmd: cmd}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			var ev MacosEvent
			if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Event != "" {
				m.C <- ev
			}
		}
		_ = cmd.Wait()
		m.C <- MacosEvent{End: true}
	}()
	return m, nil
}

func (m *Macos) Close() {
	if m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
	}
}
