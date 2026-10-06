// Package watch tells which source of a sphere changed, by watching the files
// of the ledger, office, routine and oj. Reminders and calendars live in
// EventKit, with no file to watch: they are polled.
package watch

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"

	"github.com/aclemen1/due-cli/internal/config"
)

// Root is a directory tree whose changes concern a source.
type Root struct {
	Source string
	Dir    string
	Depth  int
	Keep   func(path string) bool
}

// Polled lists the sources with nothing to watch.
func Polled(s config.Sphere) []string {
	var out []string
	for _, c := range s.Connectors {
		if !c.Off && (c.Type == "reminders" || c.Type == "calendar" || c.Type == "command") {
			out = append(out, c.Name)
		}
	}
	return out
}

// Roots are the trees to watch for a sphere.
func Roots(s config.Sphere, sphere string) []Root {
	home, _ := os.UserHomeDir()
	md := func(p string) bool { return strings.HasSuffix(p, ".md") }
	roots := []Root{{Source: "due", Dir: s.Root, Depth: 0, Keep: func(p string) bool {
		return strings.HasPrefix(filepath.Base(p), "E-") && md(p)
	}}}
	for _, c := range s.Connectors {
		if c.Off {
			continue
		}
		switch c.Type {
		case "office":
			if c.Office != "" {
				roots = append(roots, Root{c.Name, c.Office, 1, func(p string) bool { return filepath.Base(p) == "dossier.md" }})
			}
		case "routine":
			roots = append(roots,
				Root{c.Name, filepath.Join(home, ".config", "routine", "tasks"), 2, md},
				Root{c.Name, filepath.Join(home, ".local", "state", "routine", "tasks"), 2, func(p string) bool { return strings.HasSuffix(p, ".json") }})
		case "oj":
			if dir := ojRoot(c, sphere); dir != "" {
				roots = append(roots, Root{c.Name, dir, 3, func(p string) bool {
					return md(p) || strings.HasSuffix(p, ".yaml")
				}})
			}
		}
	}
	return roots
}

func ojRoot(c config.Connector, sphere string) string {
	if c.OJSphere != "" {
		sphere = c.OJSphere
	}
	p := os.Getenv("OJ_CONFIG")
	if p == "" {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, ".config", "oj", "config.yaml")
	}
	b, err := os.ReadFile(config.Expand(p))
	if err != nil {
		return ""
	}
	var cfg struct {
		Spheres map[string]struct {
			Root string `yaml:"root"`
		} `yaml:"spheres"`
	}
	if yaml.Unmarshal(b, &cfg) != nil {
		return ""
	}
	return config.Expand(cfg.Spheres[sphere].Root)
}

// Watcher sends on C the name of each source whose files changed, at most
// once per quiet period.
type Watcher struct {
	C     chan string
	fs    *fsnotify.Watcher
	roots []Root
	quiet time.Duration

	mu      sync.Mutex
	pending map[string]*time.Timer
}

// Start watches the roots; a root missing on disk is skipped.
func Start(roots []Root, quiet time.Duration) (*Watcher, error) {
	fs, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{C: make(chan string, 16), fs: fs, roots: roots, quiet: quiet, pending: map[string]*time.Timer{}}
	for _, r := range roots {
		w.add(r.Dir, r.Depth)
	}
	go w.loop()
	return w, nil
}

func (w *Watcher) add(dir string, depth int) {
	if w.fs.Add(dir) != nil || depth <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			w.add(filepath.Join(dir, e.Name()), depth-1)
		}
	}
}

func (w *Watcher) loop() {
	for {
		select {
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}
			w.handle(ev)
		case _, ok := <-w.fs.Errors:
			if !ok {
				return
			}
		}
	}
}

func (w *Watcher) handle(ev fsnotify.Event) {
	for _, r := range w.roots {
		rel, err := filepath.Rel(r.Dir, ev.Name)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if strings.HasPrefix(parts[0], ".") {
			continue
		}
		if ev.Has(fsnotify.Create) {
			if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() && len(parts) <= r.Depth {
				w.add(ev.Name, r.Depth-len(parts))
				w.fire(r.Source)
				continue
			}
		}
		if r.Keep == nil || r.Keep(ev.Name) {
			w.fire(r.Source)
		}
	}
}

func (w *Watcher) fire(source string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if t, ok := w.pending[source]; ok {
		t.Reset(w.quiet)
		return
	}
	w.pending[source] = time.AfterFunc(w.quiet, func() {
		w.mu.Lock()
		delete(w.pending, source)
		w.mu.Unlock()
		select {
		case w.C <- source:
		default:
		}
	})
}

func (w *Watcher) Close() error { return w.fs.Close() }
