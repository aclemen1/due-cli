package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/connect"
	"github.com/aclemen1/due-cli/internal/judge"
	"github.com/aclemen1/due-cli/internal/ledger"
	"github.com/aclemen1/due-cli/internal/spec"
	"github.com/aclemen1/due-cli/internal/trigger"
	"github.com/aclemen1/due-cli/internal/when"
)

// Assessment sums up a pass of the judge.
type Assessment struct {
	Lines    int      `json:"lines"`
	Judged   int      `json:"judged"`
	Cached   int      `json:"cached"`
	Critical int      `json:"critical"`
	Models   []string `json:"models,omitempty"`
	Errors   []string `json:"errors,omitempty"`
}

// Assess asks the judge about the lines of the spheres it has not seen yet.
func Assess(ctx *spec.Context, cfg *config.Config, spheres []string, until string, force bool) (Assessment, error) {
	var out Assessment
	res, err := ListAll(ctx, cfg, spheres, Query{Until: until})
	if err != nil {
		return out, err
	}
	for _, e := range res.Errors {
		out.Errors = append(out.Errors, e.Sphere+"/"+e.Source+": "+e.Error)
	}
	client, err := judge.New(cfg.Judge)
	if err != nil {
		return out, err
	}
	defer client.Close()
	cache := judge.Load()
	now := Now()
	var todo []connect.Item
	for _, it := range res.Items {
		out.Lines++
		if _, ok := cache.Get(it); ok && !force {
			out.Cached++
			continue
		}
		todo = append(todo, it)
	}
	var mu sync.Mutex
	models := map[string]bool{}
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for _, it := range todo {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			c, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			v, err := client.Judge(c, it, now)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if len(out.Errors) < 10 {
					out.Errors = append(out.Errors, it.Title+": "+err.Error())
				}
				return
			}
			cache.Put(it, v)
			out.Judged++
			models[v.Model] = true
		}()
	}
	wg.Wait()
	if err := cache.Save(now); err != nil {
		return out, err
	}
	for m := range models {
		out.Models = append(out.Models, m)
	}
	items := res.Items
	judge.Annotate(items)
	for _, it := range items {
		if judge.IsCritical(it, cfg.Judge.Threshold) {
			out.Critical++
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- alerts

type alertState map[string]string // line key + notice, or digest:<sphere> → date sent

func alertPath() string { return filepath.Join(config.StateDir(), "alerts.json") }

func loadAlerts() alertState {
	s := alertState{}
	if b, err := os.ReadFile(alertPath()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func (s alertState) save() error {
	b, _ := json.MarshalIndent(s, "", " ")
	return os.WriteFile(alertPath(), b, 0o644)
}

// Alerts says what the morning pass sends, per sphere.
type Alerts struct {
	Sent    map[string]string `json:"sent"`              // sphere → message
	Pending map[string]string `json:"pending,omitempty"` // with --dry-run
	Errors  []string          `json:"errors,omitempty"`
}

func alertLine(it connect.Item, now time.Time) string {
	day := when.Day(it.At)
	if !it.AllDay {
		day += " " + it.At.Format("15:04")
	}
	nature := ""
	if l, ok := judge.NatureLabels[it.Nature]; ok && it.Nature != "none" {
		nature = l + ", "
	}
	return fmt.Sprintf("• %s : %s, %s (%s%s)", it.Title, day, when.Until(it.At, now), nature, it.Source)
}

// RunAlerts sends the notices of the critical lines that reach one today, and
// on the digest's weekday the critical lines of the coming weeks.
func RunAlerts(ctx *spec.Context, cfg *config.Config, spheres []string, dry bool) (Alerts, error) {
	out := Alerts{Sent: map[string]string{}, Pending: map[string]string{}}
	now := Now()
	today := now.Format("2006-01-02")
	longest := 0 * time.Hour
	for _, n := range cfg.Judge.Notice {
		if d, err := when.ParseDuration(n); err == nil && d > longest {
			longest = d
		}
	}
	window := fmt.Sprintf("%dd", int(longest.Hours()/24)+1)
	if d, err := when.ParseDuration(cfg.Judge.DigestFor); err == nil && d > longest {
		window = cfg.Judge.DigestFor
	}
	if a, err := Assess(ctx, cfg, spheres, window, false); err != nil {
		out.Errors = append(out.Errors, "assess: "+err.Error())
	} else {
		out.Errors = append(out.Errors, a.Errors...)
	}
	res, err := ListAll(ctx, cfg, spheres, Query{Until: window})
	if err != nil {
		return out, err
	}
	state := loadAlerts()
	digestDay := strings.EqualFold(cfg.Judge.Digest, now.Weekday().String())
	digestUntil, _ := when.Horizon(cfg.Judge.DigestFor, now, cfg.DefaultTime)
	for _, sp := range spheres {
		l, err := OpenLedger(ctx, cfg, sp)
		if err != nil {
			out.Errors = append(out.Errors, err.Error())
			continue
		}
		var notices, digest []string
		var marks []string
		seen := map[string]bool{}
		for _, it := range res.Items {
			if it.Sphere != sp || !judge.IsCritical(it, cfg.Judge.Threshold) {
				continue
			}
			if it.Type == "due" && (it.State != ledger.Open || hasOwnNotices(l, it.ID)) {
				continue
			}
			same := strings.ToLower(it.Title) + "|" + it.At.Format("2006-01-02")
			dup := seen[same]
			seen[same] = true
			if digestDay && !dup && !it.At.After(digestUntil) {
				digest = append(digest, alertLine(it, now))
			}
			if it.Late {
				continue
			}
			for _, n := range cfg.Judge.Notice {
				d, err := when.Before(it.At, n)
				if err != nil || d.Format("2006-01-02") > today {
					continue
				}
				k := judge.Key(it) + "|" + n
				if _, sent := state[k]; sent {
					continue
				}
				marks = append(marks, k)
				if !dup {
					notices = append(notices, alertLine(it, now))
				}
				break
			}
		}
		var parts []string
		if len(notices) > 0 {
			parts = append(parts, "Échéances critiques à ne pas oublier :\n"+strings.Join(notices, "\n"))
		}
		dk := "digest:" + sp
		if digestDay && state[dk] != today && len(digest) > 0 {
			parts = append(parts, fmt.Sprintf("Échéances critiques jusqu'au %s :\n%s", when.Day(digestUntil), strings.Join(digest, "\n")))
			marks = append(marks, dk)
		}
		if len(parts) == 0 {
			continue
		}
		msg := strings.Join(parts, "\n\n")
		if dry {
			out.Pending[sp] = msg
			continue
		}
		if err := trigger.Tell(l, msg); err != nil {
			out.Errors = append(out.Errors, sp+": "+err.Error())
			continue
		}
		for _, k := range marks {
			state[k] = today
		}
		out.Sent[sp] = msg
	}
	if !dry {
		if err := state.save(); err != nil {
			return out, err
		}
	}
	return out, nil
}

func hasOwnNotices(l *ledger.Ledger, id string) bool {
	e, err := l.Get(id)
	return err == nil && len(e.Notice) > 0
}

// ---------------------------------------------------------------- daily runs

type dailyState map[string]string // job → date of its last run

func dailyPath() string { return filepath.Join(config.StateDir(), "daily.json") }

// SpawnDaily starts, detached, the nightly assessment and the morning alerts
// once a day each, as soon as their time has come (a sleeping Mac catches up once).
func SpawnDaily(cfg *config.Config, cfgFlag string, now time.Time) []string {
	if len(cfg.Judge.Providers) == 0 {
		return nil
	}
	s := dailyState{}
	if b, err := os.ReadFile(dailyPath()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	today := now.Format("2006-01-02")
	var started []string
	for _, job := range []struct{ name, at string }{{"assess", cfg.Judge.Nightly}, {"alert", cfg.Judge.Morning}} {
		t, err := time.ParseInLocation("2006-01-02 15:04", today+" "+job.at, now.Location())
		if err != nil || now.Before(t) || s[job.name] == today {
			continue
		}
		exe, err := os.Executable()
		if err != nil {
			continue
		}
		args := []string{job.name, "--format", "text"}
		if cfgFlag != "" {
			args = append(args, "--config", cfgFlag)
		}
		logf, _ := os.OpenFile(filepath.Join(config.StateDir(), job.name+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		cmd := exec.Command(exe, args...)
		cmd.Stdout, cmd.Stderr = logf, logf
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if cmd.Start() == nil {
			s[job.name] = today
			started = append(started, job.name)
			go func() { _ = cmd.Wait() }()
		}
	}
	if len(started) > 0 {
		b, _ := json.MarshalIndent(s, "", " ")
		_ = os.WriteFile(dailyPath(), b, 0o644)
	}
	return started
}

// ---------------------------------------------------------------- actions

func registerJudge() {
	spec.Register(&spec.Action{
		Category: "judge", Name: "assess", Top: true,
		Summary: "Ask the judge (jev, else OpenJev) which lines would cost dearly if forgotten: legal, financial, irreversible.",
		Discussion: "Each line not judged yet (or whose title or date changed) is sent to the providers of judge.providers, in order: " +
			"title, date, source, detail and sphere, never a body. Verdicts are kept in ~/.local/state/due/judge.json. " +
			"The launcher runs it every night at judge.nightly.",
		Params: []spec.Param{
			{Name: "until", Kind: spec.String, Help: "Lines up to this horizon. Defaults to judge.horizon (365d)."},
			{Name: "force", Kind: spec.Bool, Help: "Judge again the lines already judged."},
			readSphereParam(),
		},
		Effects:  []string{"Sends the lines to the judge's providers; writes the verdict cache."},
		Examples: []string{"due assess", "due assess --until 60d --sphere perso --format text"},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, err := config.Load(ctx.Config)
			if err != nil {
				return nil, err
			}
			spheres, err := ReadSpheres(ctx, cfg)
			if err != nil {
				return nil, err
			}
			until := ctx.Str("until")
			if until == "" {
				until = cfg.Judge.Horizon
			}
			return Assess(ctx, cfg, spheres, until, ctx.Bool("force"))
		},
		Text: func(w io.Writer, v any) {
			a := v.(Assessment)
			fmt.Fprintf(w, "%d lines: %d judged now (%s), %d already judged; %d critical.\n", a.Lines, a.Judged, strings.Join(a.Models, ", "), a.Cached, a.Critical)
			for _, e := range a.Errors {
				fmt.Fprintln(w, "error: "+e)
			}
		},
	})
	spec.Register(&spec.Action{
		Category: "judge", Name: "alert", Top: true,
		Summary: "Send today's alerts on critical lines: a notice at each delay of judge.notice, and on judge.digest's weekday the critical lines of the coming weeks.",
		Discussion: "Ledger entries with notices of their own are left to them. Each alert goes once, through the sphere's tell command. " +
			"The launcher runs it every morning at judge.morning.",
		Params: []spec.Param{
			{Name: "dry-run", Kind: spec.Bool, Help: "Show the messages without sending them."},
			readSphereParam(),
		},
		Effects:  []string{"Judges new lines; sends Telegram messages through tell; records them in ~/.local/state/due/alerts.json."},
		Examples: []string{"due alert --dry-run --format text", "due alert"},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, err := config.Load(ctx.Config)
			if err != nil {
				return nil, err
			}
			spheres, err := ReadSpheres(ctx, cfg)
			if err != nil {
				return nil, err
			}
			return RunAlerts(ctx, cfg, spheres, ctx.Bool("dry-run"))
		},
		Text: func(w io.Writer, v any) {
			a := v.(Alerts)
			show := func(label string, m map[string]string) {
				var keys []string
				for k := range m {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Fprintf(w, "[%s, %s]\n%s\n\n", k, label, m[k])
				}
			}
			show("sent", a.Sent)
			show("not sent (dry run)", a.Pending)
			if len(a.Sent) == 0 && len(a.Pending) == 0 {
				fmt.Fprintln(w, "No alert today.")
			}
			for _, e := range a.Errors {
				fmt.Fprintln(w, "error: "+e)
			}
		},
	})
}
