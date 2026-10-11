// Package judge asks a decision model (TypeSafe's System One API: jev, or a
// local OpenJev) which lines would cost dearly if forgotten. Verdicts are kept
// in a cache by line, so a line is asked about once until its title or date change.
package judge

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aclemen1/jev"

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

// Client asks the judge through the jev module.
type Client struct {
	conf config.Judge
	jev  *jev.Client
	only string
}

// New opens the judge of the configuration (judge.provider) on ~/.config/jev/config.yaml.
func New(conf config.Judge) (*Client, error) {
	if !conf.On() {
		return nil, fmt.Errorf("no judge: set judge.provider in %s (jev, or openjev)", config.Path(""))
	}
	j, err := jev.Load()
	if err != nil {
		return nil, err
	}
	return NewWith(conf, j)
}

// NewWith opens the judge on a jev client.
func NewWith(conf config.Judge, j *jev.Client) (*Client, error) {
	c := &Client{conf: conf, jev: j}
	switch conf.Provider {
	case "jev":
	case "openjev":
		c.only = "openjev"
	default:
		return nil, fmt.Errorf("judge.provider %q: jev or openjev", conf.Provider)
	}
	return c, nil
}

type answer struct {
	Choice     string   `json:"choice"`
	Noul       *float64 `json:"noul"`
	Confidence float64  `json:"confidence"`
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

// Judge asks about a line: Jev, then OpenJev when Jev refuses or does not answer.
func (c *Client) Judge(ctx context.Context, it connect.Item, now time.Time) (Verdict, error) {
	q := question
	if s := strings.TrimSpace(c.conf.Context); s != "" {
		q = s + "\n\n" + q
	}
	res, provider, err := c.jev.Ask(ctx, jev.Request{State: State(it, now), Only: c.only, Questions: map[string]any{
		"critical": map[string]any{"type": "noul", "instructions": map[string]any{"item": "the deadline in the state", "question": q}},
		"nature":   map[string]any{"type": "choice", "instructions": "What would forgetting this deadline cost the owner?", "criteria": Natures},
	}})
	if err != nil {
		return Verdict{}, err
	}
	var crit, nature answer
	_ = res.Decode("nature", &nature)
	if err := res.Decode("critical", &crit); err != nil || crit.Noul == nil {
		return Verdict{}, fmt.Errorf("%s: no answer to critical", provider)
	}
	model, _, _ := strings.Cut(res.Model, " ")
	if model == "" {
		model = provider
	}
	return Verdict{Critical: *crit.Noul, Nature: nature.Choice, Confidence: nature.Confidence, Model: model, Judged: now}, nil
}

// Close withdraws from the local server; the last user stops it.
func (c *Client) Close() { _ = c.jev.Close() }
