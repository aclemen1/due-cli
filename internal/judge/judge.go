// Package judge asks a decision model (TypeSafe's System One API: jev, or a
// local OpenJev) which lines would cost dearly if forgotten. Verdicts are kept
// in a cache by line, so a line is asked about once until its title or date change.
package judge

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/connect"
	"github.com/aclemen1/due-cli/internal/when"
)

// Natures are what forgetting a line would cost.
var Natures = map[string]string{
	"legal":        "a right lost or a legal duty missed: appeal, objection, notice period, contract term, filing",
	"financial":    "a fee, penalty, interest, lost refund or other money",
	"irreversible": "something that cannot be undone or caught up: health, a missed one-off occasion, a lost document",
	"none":         "nothing serious; an inconvenience at most",
}

var NatureLabels = map[string]string{"legal": "juridique", "financial": "financière", "irreversible": "irréversible", "none": "aucune"}

const question = "Would forgetting this deadline, or missing its date, have legal, regulatory, heavy financial or irreversible consequences for the owner?"

// Verdict is the judge's answer about one line.
type Verdict struct {
	Critical   float64      `json:"critical"`
	Nature     string       `json:"nature"`
	Confidence float64      `json:"confidence"`
	Model      string       `json:"model"`
	Judged     time.Time    `json:"judged"`
	Line       connect.Item `json:"line"`
}

// Key names a line as the judge saw it: a new title or date asks again.
func Key(it connect.Item) string {
	h := sha1.Sum([]byte(strings.Join([]string{it.Sphere, it.Source, it.ID, it.Title, it.At.Format(time.RFC3339)}, "\x00")))
	return hex.EncodeToString(h[:10])
}

// ---------------------------------------------------------------- cache

type Cache struct {
	mu   sync.Mutex
	path string
	V    map[string]Verdict
}

func CachePath() string { return filepath.Join(config.StateDir(), "judge.json") }

// Load reads the cache; a missing or broken file gives an empty one.
func Load() *Cache {
	c := &Cache{path: CachePath(), V: map[string]Verdict{}}
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, &c.V)
	}
	if c.V == nil {
		c.V = map[string]Verdict{}
	}
	return c
}

func (c *Cache) Get(it connect.Item) (Verdict, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.V[Key(it)]
	return v, ok
}

func (c *Cache) Put(it connect.Item, v Verdict) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v.Line = it
	v.Line.Critical, v.Line.Nature = nil, ""
	c.V[Key(it)] = v
}

// Save writes the cache, dropping verdicts on lines over for more than 60 days.
func (c *Cache) Save(now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.V {
		if v.Line.At.Before(now.AddDate(0, 0, -60)) {
			delete(c.V, k)
		}
	}
	b, err := json.MarshalIndent(c.V, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// Annotate puts the cached verdicts on the lines.
func Annotate(items []connect.Item) {
	c := Load()
	for i := range items {
		if v, ok := c.Get(items[i]); ok {
			p := v.Critical
			items[i].Critical, items[i].Nature = &p, v.Nature
		}
	}
}

// IsCritical says whether a judged line passes the threshold.
func IsCritical(it connect.Item, threshold float64) bool {
	return it.Critical != nil && *it.Critical >= threshold
}

// ---------------------------------------------------------------- client

type Client struct {
	conf    config.Judge
	http    *http.Client
	started map[string]*exec.Cmd
}

func New(conf config.Judge) (*Client, error) {
	if len(conf.Providers) == 0 {
		return nil, fmt.Errorf("no judge provider: add judge.providers to %s (jev, then openjev)", config.Path(""))
	}
	return &Client{conf: conf, http: &http.Client{Timeout: 60 * time.Second}, started: map[string]*exec.Cmd{}}, nil
}

func key(k config.Key) string {
	if k.Env != "" {
		if v := strings.TrimSpace(os.Getenv(k.Env)); v != "" {
			return v
		}
	}
	if k.Keychain == "" {
		return ""
	}
	args := []string{"find-generic-password", "-s", k.Keychain, "-w"}
	if k.KeychainFile != "" {
		_ = exec.Command("security", "unlock-keychain", "-p", "", k.KeychainFile).Run()
		args = append(args, k.KeychainFile)
	}
	out, err := exec.Command("security", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type answer struct {
	Choice     string   `json:"choice"`
	Noul       *float64 `json:"noul"`
	Confidence float64  `json:"confidence"`
}

func (c *Client) ask(ctx context.Context, p config.Provider, state map[string]any) (Verdict, error) {
	q := question
	if s := strings.TrimSpace(c.conf.Context); s != "" {
		q = s + "\n\n" + q
	}
	body, _ := json.Marshal(map[string]any{"model": p.Model, "state": state, "questions": map[string]any{
		"critical": map[string]any{"type": "noul", "instructions": map[string]any{"item": "the deadline in the state", "question": q}},
		"nature":   map[string]any{"type": "choice", "instructions": "What would forgetting this deadline cost the owner?", "criteria": Natures},
	}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Verdict{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if k := key(p.Key); k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Verdict{}, err
	}
	defer resp.Body.Close()
	var out struct {
		Model   string            `json:"model"`
		Answers map[string]answer `json:"answers"`
		Error   any               `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Verdict{}, fmt.Errorf("%s: %s", p.Name, resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		return Verdict{}, fmt.Errorf("%s: %s %v", p.Name, resp.Status, out.Error)
	}
	crit := out.Answers["critical"]
	if crit.Noul == nil {
		return Verdict{}, fmt.Errorf("%s: no answer to critical", p.Name)
	}
	model, _, _ := strings.Cut(out.Model, " ")
	if model == "" {
		model = p.Name
	}
	return Verdict{Critical: *crit.Noul, Nature: out.Answers["nature"].Choice, Confidence: out.Answers["nature"].Confidence, Model: model}, nil
}

// State is what the judge sees of a line: no body, no message.
func State(it connect.Item, now time.Time) map[string]any {
	day := when.Day(it.At)
	if !it.AllDay {
		day += " " + it.At.Format("15:04")
	}
	s := map[string]any{"title": it.Title, "date": day + " (" + when.Until(it.At, now) + ")", "source": it.Source + " (" + it.Type + ")", "sphere": it.Sphere}
	if it.Detail != "" {
		s["detail"] = it.Detail
	}
	return s
}

// Judge asks the providers in order about a line; the first that answers wins.
func (c *Client) Judge(ctx context.Context, it connect.Item, now time.Time) (Verdict, error) {
	var errs []string
	for _, p := range c.conf.Providers {
		if len(p.Start) > 0 {
			if err := c.ensure(ctx, p); err != nil {
				errs = append(errs, err.Error())
				continue
			}
		}
		v, err := c.ask(ctx, p, State(it, now))
		if err == nil {
			v.Judged = now
			return v, nil
		}
		errs = append(errs, err.Error())
	}
	return Verdict{}, fmt.Errorf("no judge answered: %s", strings.Join(errs, "; "))
}

// ensure starts a local provider that does not answer, and waits for it.
func (c *Client) ensure(ctx context.Context, p config.Provider) error {
	base := strings.TrimSuffix(p.Endpoint, "/systemone")
	up := func() bool {
		r, err := (&http.Client{Timeout: 2 * time.Second}).Get(base + "/version")
		if err != nil {
			return false
		}
		r.Body.Close()
		return r.StatusCode < 500
	}
	if up() {
		return nil
	}
	if _, ok := c.started[p.Name]; ok {
		return fmt.Errorf("%s: started but not answering", p.Name)
	}
	cmd := exec.Command(p.Start[0], p.Start[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	logf, _ := os.OpenFile(filepath.Join(config.StateDir(), "judge-"+p.Name+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: start: %v", p.Name, err)
	}
	c.started[p.Name] = cmd
	wait, err := when.ParseDuration(p.Ready)
	if err != nil {
		wait = 10 * time.Minute
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if up() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("%s: not ready after %s (log: %s)", p.Name, wait, filepath.Join(config.StateDir(), "judge-"+p.Name+".log"))
}

// Close stops the local servers this client started.
func (c *Client) Close() {
	for _, cmd := range c.started {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			_ = cmd.Wait()
		}
	}
}
