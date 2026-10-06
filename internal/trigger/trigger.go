// Package trigger fires due's own entries: the notices tell, the term runs
// the entry's action. `due tick`, run every minute by the launcher, calls Tick.
package trigger

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/ledger"
	"github.com/aclemen1/due-cli/internal/spec"
	"github.com/aclemen1/due-cli/internal/when"
)

// Message is what a tell says at an instant of the entry.
func Message(l *ledger.Ledger, e *ledger.Entry, i ledger.Instant, now time.Time) string {
	term := e.Moment().Time(l.Loc(), l.Clock)
	day := when.Day(term)
	if !e.Moment().AllDay {
		day += " à " + term.Format("15:04")
	}
	msg := fmt.Sprintf("%s : %s (%s)", e.Title, when.Until(term, now), day)
	if strings.HasPrefix(i.Kind, "term") && strings.TrimSpace(e.Body) != "" {
		msg += "\n\n" + strings.TrimSpace(e.Body)
	}
	return msg
}

// Command builds the process that fires an instant.
func Command(l *ledger.Ledger, e *ledger.Entry, i ledger.Instant, now time.Time) ([]string, string, error) {
	cwd := config.Expand(e.Cwd)
	if cwd == "" {
		cwd, _ = os.UserHomeDir()
	}
	term := e.Moment().Time(l.Loc(), l.Clock)
	prompt := strings.TrimSpace(e.Body)
	if prompt == "" {
		prompt = e.Title
	}
	dossier := "desk"
	if d, ok := strings.CutPrefix(e.Ref, "office:"); ok && d != "" {
		dossier = d
	}
	vars := map[string]string{
		"{dossier}": dossier,
		"{id}":      e.ID, "{title}": e.Title, "{sphere}": l.Sphere, "{ref}": e.Ref, "{cwd}": cwd,
		"{at}": term.Format(time.RFC3339), "{when}": when.Until(term, now), "{kind}": i.Kind,
		"{message}": Message(l, e, i, now), "{prompt}": prompt,
	}
	do := e.Do
	if !strings.HasPrefix(i.Kind, "term") {
		do = "tell"
	}
	var tmpl []string
	switch do {
	case "tell":
		tmpl = l.Actions.Tell
	case "agent":
		tmpl = l.Actions.Agent
	case "command":
		if strings.TrimSpace(e.Run) == "" {
			return nil, "", spec.UserError("%s has do: command but no run", e.ID)
		}
		return []string{l.Actions.Shell, "-lc", e.Run}, cwd, nil
	default:
		return nil, "", spec.UserError("%s: unknown action %q", e.ID, do)
	}
	if len(tmpl) == 0 {
		return nil, "", spec.UserError("no %s command for sphere %s: set spheres.%s.actions.%s in %s", do, l.Sphere, l.Sphere, do, config.Path(""))
	}
	argv := make([]string, len(tmpl))
	for k, a := range tmpl {
		for v, x := range vars {
			a = strings.ReplaceAll(a, v, x)
		}
		argv[k] = config.Expand(a)
	}
	return argv, cwd, nil
}

// LogPath is where the output of a firing goes.
func LogPath(sphere, id string, now time.Time) string {
	return filepath.Join(config.StateDir(), "runs", sphere, id+"-"+now.UTC().Format("20060102T150405Z")+".log")
}

// Execute runs the firing's command, its output in logPath.
func Execute(l *ledger.Ledger, e *ledger.Entry, i ledger.Instant, now time.Time, logPath string) error {
	argv, cwd, err := Command(l, e, i, now)
	if err != nil {
		return err
	}
	timeout, err := when.ParseDuration(l.Actions.Timeout)
	if err != nil {
		timeout = 10 * time.Minute
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	out, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer out.Close()
	fmt.Fprintf(out, "# %s %s (%s) at %s\n# %s\n", e.ID, i.Kind, l.Sphere, now.Format(time.RFC3339), strings.Join(argv, " "))
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Env = append(os.Environ(), "DUE_ID="+e.ID, "DUE_SPHERE="+l.Sphere, "DUE_KIND="+i.Kind, "DUE_TITLE="+e.Title)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("timeout after %s", timeout)
	}
	return err
}

// Fired is one firing done by Tick or Fire.
type Fired struct {
	Sphere string `json:"sphere"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Result string `json:"result"`
	Log    string `json:"log,omitempty"`
}

type job struct {
	e   *ledger.Entry
	i   ledger.Instant
	log string
}

// Tick fires what came in every sphere. A tick already running makes it return at once.
func Tick(cfg *config.Config, now time.Time, open func(sphere string) (*ledger.Ledger, error)) ([]Fired, []string, error) {
	if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
		return nil, nil, err
	}
	lock, err := os.OpenFile(filepath.Join(config.StateDir(), "tick.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, nil, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, []string{"another tick is running"}, nil
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if _, err := os.Stat(StopFile()); err == nil {
		return nil, []string{"stopped: remove " + StopFile() + " or run `due launcher start`"}, nil
	}
	var fired []Fired
	var warnings []string
	for _, sphere := range cfg.Names() {
		l, err := open(sphere)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", sphere, err))
			continue
		}
		var jobs []job
		err = l.WriteAs(func() (string, error) {
			entries, err := l.List()
			if err != nil {
				return "", err
			}
			var ids []string
			for _, e := range entries {
				fire, skip := l.Pending(e, now)
				if fire == nil {
					continue
				}
				for _, s := range skip {
					l.Record(e, s, "skipped", "")
				}
				lp := LogPath(sphere, e.ID, now)
				l.Record(e, *fire, "running", lp)
				if err := l.Save(e); err != nil {
					return "", err
				}
				jobs = append(jobs, job{e, *fire, lp})
				ids = append(ids, e.ID+" "+fire.Kind)
			}
			if len(ids) == 0 {
				return "", nil
			}
			return "fire " + strings.Join(ids, ", "), nil
		})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", sphere, err))
			continue
		}
		for _, j := range jobs {
			fired = append(fired, settle(l, j, now))
		}
	}
	return fired, warnings, nil
}

// Fire runs an instant of an entry now, by hand, and records it.
func Fire(l *ledger.Ledger, id, kind string, now time.Time) (Fired, error) {
	var j job
	err := l.Write("", func() error {
		e, err := l.Get(id)
		if err != nil {
			return err
		}
		i := ledger.Instant{When: now, Kind: kind}
		if kind == "term" {
			i.When = e.Moment().Time(l.Loc(), l.Clock)
			if e.Do == "" {
				return spec.Conflict("%s has no action to run; give one with: due edit %s --do tell", e.ID, e.ID)
			}
		}
		j = job{e, i, LogPath(l.Sphere, e.ID, now)}
		return nil
	})
	if err != nil {
		return Fired{}, err
	}
	j.i.Kind += " (by hand)"
	return settle(l, j, now), nil
}

// settle runs a job outside the ledger's lock (the action may call due), then records its result.
func settle(l *ledger.Ledger, j job, now time.Time) Fired {
	result := "ok"
	if err := Execute(l, j.e, j.i, now, j.log); err != nil {
		result = "failed: " + err.Error()
	}
	_ = l.WriteAs(func() (string, error) {
		e, err := l.Get(j.e.ID)
		if err != nil {
			return "", err
		}
		if !e.Settle(j.i, result) {
			l.Record(e, j.i, result, j.log)
		}
		l.Note(e, j.i.Kind+": "+result)
		if err := l.Save(e); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s: %s", e.ID, j.i.Kind, result), nil
	})
	return Fired{Sphere: l.Sphere, ID: j.e.ID, Title: j.e.Title, Kind: j.i.Kind, Result: result, Log: j.log}
}

// StopFile, when present, keeps every tick from firing.
func StopFile() string { return filepath.Join(config.StateDir(), "stopped") }
