// Package ledger keeps due's own entries: one Markdown file per entry in the
// sphere's directory, front matter for the fields, body for the message or prompt.
package ledger

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/spec"
	"github.com/aclemen1/due-cli/internal/when"
)

const (
	Open    = "open"
	Done    = "done"
	Dropped = "dropped"
)

var Dos = []string{"tell", "agent", "command"}

type LogEntry struct {
	At   string `yaml:"at" json:"at"`
	By   string `yaml:"by" json:"by"`
	What string `yaml:"what" json:"what"`
}

// Firing is an instant of an entry that came: a notice or the term itself.
type Firing struct {
	Instant string `yaml:"instant" json:"instant"` // RFC 3339
	Kind    string `yaml:"kind" json:"kind"`       // notice 7d, term
	At      string `yaml:"at" json:"at"`           // when it fired
	Result  string `yaml:"result" json:"result"`   // running, ok, failed: …, skipped
	Log     string `yaml:"log,omitempty" json:"log,omitempty"`
}

type Entry struct {
	ID      string     `yaml:"id" json:"id"`
	Title   string     `yaml:"title" json:"title"`
	At      string     `yaml:"at" json:"at"` // 2026-11-15 or 2026-11-15T14:00
	Notice  []string   `yaml:"notice,omitempty" json:"notice,omitempty"`
	Do      string     `yaml:"do,omitempty" json:"do,omitempty"`
	Run     string     `yaml:"run,omitempty" json:"run,omitempty"`
	Cwd     string     `yaml:"cwd,omitempty" json:"cwd,omitempty"`
	Ref     string     `yaml:"ref,omitempty" json:"ref,omitempty"`
	State   string     `yaml:"state" json:"state"`
	Created string     `yaml:"created" json:"created"`
	Fired   []Firing   `yaml:"fired,omitempty" json:"fired,omitempty"`
	Log     []LogEntry `yaml:"log,omitempty" json:"log,omitempty"`
	Body    string     `yaml:"-" json:"body,omitempty"`
	File    string     `yaml:"-" json:"file"`
}

// Moment is the entry's date, with or without a time.
func (e *Entry) Moment() when.Moment {
	if d, c, ok := strings.Cut(e.At, "T"); ok {
		return when.Moment{Date: d, Clock: c}
	}
	return when.Moment{Date: e.At, AllDay: true}
}

// Instant is a moment at which an entry fires.
type Instant struct {
	When time.Time
	Kind string // notice 7d, term
}

// Instants are the notices, then the term, in time order.
func (e *Entry) Instants(loc *time.Location, clock string) []Instant {
	term := e.Moment().Time(loc, clock)
	var out []Instant
	for _, n := range e.Notice {
		t, err := when.Before(term, n)
		if err != nil {
			continue
		}
		out = append(out, Instant{t, "notice " + n})
	}
	out = append(out, Instant{term, "term"})
	sort.SliceStable(out, func(i, j int) bool { return out[i].When.Before(out[j].When) })
	return out
}

// HasFired says whether an instant was recorded, fired or skipped.
func (e *Entry) HasFired(i Instant) bool {
	s := i.When.Format(time.RFC3339)
	for _, f := range e.Fired {
		if f.Instant == s && f.Kind == i.Kind {
			return true
		}
	}
	return false
}

type Ledger struct {
	Sphere  string
	Prefix  string // ids are <Prefix>E-0001
	Root    string
	VCS     string
	By      string
	Clock   string // default time of a date alone
	Actions config.Actions
	Now     func() time.Time
	Warn    func(string)
}

// OpenLedger opens a configured sphere.
func OpenLedger(cfg *config.Config, sphere, by string) (*Ledger, error) {
	s, ok := cfg.Spheres[sphere]
	if !ok {
		return nil, spec.UserError("unknown sphere %q; configured: %s. Example: due init --sphere perso --root ~/due/perso", sphere, strings.Join(cfg.Names(), ", "))
	}
	if _, err := os.Stat(s.Root); err != nil {
		return nil, spec.UserError("the ledger of %s is missing at %s. Run: due init --sphere %s --root %s", sphere, s.Root, sphere, s.Root)
	}
	if by == "" {
		by = "user"
	}
	return &Ledger{Sphere: sphere, Prefix: s.Prefix, Root: s.Root, VCS: s.VCS, By: by, Clock: cfg.DefaultTime, Actions: s.Actions, Now: time.Now,
		Warn: func(m string) { fmt.Fprintln(os.Stderr, "due: warning: "+m) }}, nil
}

// Init creates the ledger directory and, with a VCS, its repository.
func Init(root, vcs string) error {
	if err := os.MkdirAll(filepath.Join(root, ".due"), 0o755); err != nil {
		return err
	}
	ignore := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(ignore); os.IsNotExist(err) {
		if err := os.WriteFile(ignore, []byte(".due/\n"), 0o644); err != nil {
			return err
		}
	}
	switch vcs {
	case "jj":
		if _, err := os.Stat(filepath.Join(root, ".jj")); os.IsNotExist(err) {
			if out, err := runIn(root, "jj", "git", "init"); err != nil {
				return fmt.Errorf("jj git init: %v: %s", err, out)
			}
		}
	case "git":
		if _, err := os.Stat(filepath.Join(root, ".git")); os.IsNotExist(err) {
			if out, err := runIn(root, "git", "init", "-q"); err != nil {
				return fmt.Errorf("git init: %v: %s", err, out)
			}
		}
	}
	return nil
}

func runIn(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (l *Ledger) Loc() *time.Location { return l.Now().Location() }

// Write runs fn under the ledger's lock, then commits with msg.
func (l *Ledger) Write(msg string, fn func() error) error {
	return l.WriteAs(func() (string, error) { return msg, fn() })
}

// WriteAs is Write with a message known once fn has run; an empty one commits nothing.
func (l *Ledger) WriteAs(fn func() (string, error)) error {
	if err := os.MkdirAll(filepath.Join(l.Root, ".due"), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(l.Root, ".due", "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return spec.Locked("cannot lock the ledger of %s: %v", l.Sphere, err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	msg, err := fn()
	if err != nil {
		return err
	}
	if msg != "" {
		l.commit(msg)
	}
	return nil
}

func (l *Ledger) commit(msg string) {
	var out string
	var err error
	switch l.VCS {
	case "jj":
		out, err = runIn(l.Root, "jj", "commit", "-m", msg)
	case "git":
		if out, err = runIn(l.Root, "git", "add", "-A"); err == nil {
			out, err = runIn(l.Root, "git", "commit", "-q", "--allow-empty", "-m", msg)
		}
	default:
		return
	}
	if err != nil && l.Warn != nil {
		l.Warn(fmt.Sprintf("%s commit failed in %s: %v: %s", l.VCS, l.Root, err, out))
	}
}

// Note appends a line to the entry's history.
func (l *Ledger) Note(e *Entry, what string) {
	e.Log = append(e.Log, LogEntry{At: l.Now().Format(time.RFC3339), By: l.By, What: what})
}

var numRe = regexp.MustCompile(`^(?:([A-Z]{1,3})E-)?0*(\d+)$`)

// NormID accepts PE-0007, pe-7 or 7 in the ledger of prefix P; an id of another sphere is refused.
func (l *Ledger) NormID(s string) (string, error) {
	m := numRe.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(s)))
	if m == nil || m[2] == "0" {
		return "", spec.UserError("entry id %q: expected %sE-0007 or 7", s, l.Prefix)
	}
	if m[1] != "" && m[1] != l.Prefix {
		return "", spec.UserError("entry %s is not in sphere %s, whose ids start with %sE-", strings.ToUpper(s), l.Sphere, l.Prefix)
	}
	n, _ := strconv.Atoi(m[2])
	return l.ID(n), nil
}

// ID is the id of the nth entry of the ledger.
func (l *Ledger) ID(n int) string { return fmt.Sprintf("%sE-%04d", l.Prefix, n) }

func (l *Ledger) glob() string { return filepath.Join(l.Root, l.Prefix+"E-*.md") }

func (l *Ledger) path(id string) string { return filepath.Join(l.Root, id+".md") }

// List reads every entry, in time order.
func (l *Ledger) List() ([]*Entry, error) {
	files, err := filepath.Glob(l.glob())
	if err != nil {
		return nil, err
	}
	var out []*Entry
	for _, f := range files {
		e, err := read(f)
		if err != nil {
			if l.Warn != nil {
				l.Warn(err.Error())
			}
			continue
		}
		out = append(out, e)
	}
	loc := l.Loc()
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Moment().Time(loc, l.Clock), out[j].Moment().Time(loc, l.Clock)
		if !a.Equal(b) {
			return a.Before(b)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Get reads one entry.
func (l *Ledger) Get(id string) (*Entry, error) {
	id, err := l.NormID(id)
	if err != nil {
		return nil, err
	}
	e, err := read(l.path(id))
	if os.IsNotExist(err) {
		return nil, spec.NotFound("no entry %s in %s. List them with: due ls --sphere %s --source due", id, l.Sphere, l.Sphere)
	}
	return e, err
}

// NextID is one more than the highest id in the ledger.
func (l *Ledger) NextID() string {
	files, _ := filepath.Glob(l.glob())
	max := 0
	for _, f := range files {
		if m := numRe.FindStringSubmatch(strings.TrimSuffix(filepath.Base(f), ".md")); m != nil {
			if n, _ := strconv.Atoi(m[2]); n > max {
				max = n
			}
		}
	}
	return l.ID(max + 1)
}

// Save writes the entry's file.
func (l *Ledger) Save(e *Entry) error {
	fm, err := yaml.Marshal(e)
	if err != nil {
		return err
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n")
	if body := strings.TrimSpace(e.Body); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	e.File = l.path(e.ID)
	tmp := e.File + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, e.File)
}

// Remove deletes the entry's file.
func (l *Ledger) Remove(id string) error { return os.Remove(l.path(id)) }

func read(path string) (*Entry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := string(b)
	if !strings.HasPrefix(s, "---\n") {
		return nil, fmt.Errorf("%s: no front matter", path)
	}
	fm, body, ok := strings.Cut(s[4:], "\n---")
	if !ok {
		return nil, fmt.Errorf("%s: front matter not closed", path)
	}
	e := &Entry{}
	if err := yaml.Unmarshal([]byte(fm), e); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	body = strings.TrimPrefix(body, "\n")
	e.Body = strings.TrimSpace(body)
	e.File = path
	if e.State == "" {
		e.State = Open
	}
	return e, nil
}
