// Package connect brings the dates held by other tools. Each connector runs
// its tool's CLI at each read: nothing is copied, the tool stays the source.
package connect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/when"
)

// Item is a line of the unified list, from due's ledger or a connector.
type Item struct {
	Sphere string    `json:"sphere"`
	Source string    `json:"source"` // due, or the connector's name
	Type   string    `json:"type"`   // due, reminders, calendar, office, routine, oj, command
	ID     string    `json:"id"`     // id in its source
	Title  string    `json:"title"`
	At     time.Time `json:"at"`
	AllDay bool      `json:"all_day,omitempty"`
	Detail string    `json:"detail,omitempty"`
	Ref    string    `json:"ref,omitempty"`
	State  string    `json:"state,omitempty"`
	Do     string    `json:"do,omitempty"`
	Late   bool      `json:"late,omitempty"`
	// Critical is the judge's probability that forgetting the line costs dearly; nil until judged.
	Critical *float64 `json:"critical,omitempty"`
	Nature   string   `json:"nature,omitempty"` // legal, financial, irreversible, none
	// Since is when an office dossier went waiting.
	Since *time.Time `json:"waiting_since,omitempty"`
}

// Window is the span asked for. From is zero to keep what is late.
type Window struct {
	From, Until, Now time.Time
	Sphere           string
}

func (w Window) keep(t time.Time) bool {
	return !t.After(w.Until) && (w.From.IsZero() || !t.Before(w.From))
}

// Result is the outcome of one connector.
type Result struct {
	Name  string
	Items []Item
	Err   error
}

// Fetch runs the connectors in parallel.
func Fetch(conns []config.Connector, w Window) []Result {
	out := make([]Result, len(conns))
	var wg sync.WaitGroup
	for i, c := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			items, err := One(c, w)
			out[i] = Result{Name: c.Name, Items: items, Err: err}
		}()
	}
	wg.Wait()
	return out
}

// One runs one connector.
func One(c config.Connector, w Window) ([]Item, error) {
	timeout := 20 * time.Second
	if c.Timeout != "" {
		d, err := when.ParseDuration(c.Timeout)
		if err != nil {
			return nil, err
		}
		timeout = d
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var items []Item
	var err error
	switch c.Type {
	case "reminders":
		items, err = reminders(ctx, c, w)
	case "calendar":
		items, err = calendar(ctx, c, w)
	case "office":
		items, err = office(ctx, c, w)
	case "routine":
		items, err = routine(ctx, c, w)
	case "oj":
		items, err = oj(ctx, c, w)
	case "task":
		items, err = task(ctx, c, w)
	case "command":
		items, err = command(ctx, c, w)
	default:
		err = fmt.Errorf("unknown type %q", c.Type)
	}
	if err != nil {
		return nil, err
	}
	var kept []Item
	for _, it := range items {
		if !w.keep(it.At) {
			continue
		}
		it.Source, it.Type = c.Name, c.Type
		if it.At.Before(w.Now) && !it.AllDay {
			it.Late = true
		}
		if it.AllDay && endOfDay(it.At).Before(w.Now) {
			it.Late = true
		}
		kept = append(kept, it)
	}
	return kept, nil
}

func endOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 23, 59, 59, 0, t.Location())
}

// Sort orders items by time, then source, then id.
func Sort(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].At.Equal(items[j].At) {
			return items[i].At.Before(items[j].At)
		}
		if items[i].Source != items[j].Source {
			return items[i].Source < items[j].Source
		}
		return items[i].ID < items[j].ID
	})
}

func bin(c config.Connector, def string) string {
	if c.Bin != "" {
		return config.Expand(c.Bin)
	}
	return def
}

// run calls a tool and decodes its JSON; the envelope {"ok", "result"} is opened.
func run(ctx context.Context, v any, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%s: timeout", name)
	}
	b := bytes.TrimSpace(out.Bytes())
	var env struct {
		OK     *bool           `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if len(b) > 0 && b[0] == '{' && json.Unmarshal(b, &env) == nil && env.OK != nil {
		if !*env.OK {
			msg := "failed"
			if env.Error != nil {
				msg = env.Error.Message
			}
			return fmt.Errorf("%s: %s", name, msg)
		}
		b = env.Result
	} else if err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(errb.String()))
	}
	if len(b) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: unreadable JSON: %v", name, err)
	}
	return nil
}

func parseTime(s string, loc *time.Location) (time.Time, bool) {
	for _, l := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			return t.In(loc), true
		}
	}
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func isDate(s string) bool { return len(s) == 10 }

func anyFold(want, have []string) bool {
	for _, h := range have {
		if containsFold(want, h) {
			return true
		}
	}
	return false
}

func containsFold(l []string, s string) bool {
	for _, x := range l {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func reminders(ctx context.Context, c config.Connector, w Window) ([]Item, error) {
	var res struct {
		Items []struct {
			ID     string   `json:"id"`
			Title  string   `json:"title"`
			Due    string   `json:"due"`
			List   string   `json:"list"`
			AllDay bool     `json:"allDay"`
			Tags   []string `json:"tags"`
		} `json:"items"`
	}
	err := run(ctx, &res, bin(c, "macos"), "reminders", "items", "list", "--due-before", w.Until.Format(time.RFC3339),
		"--format", "json", "--fields", "id,title,due,list,allDay,tags", "--limit", "0")
	if err != nil {
		return nil, err
	}
	loc := w.Now.Location()
	var out []Item
	for _, r := range res.Items {
		if r.Due == "" || (len(c.Lists) > 0 && !containsFold(c.Lists, r.List)) {
			continue
		}
		if containsFold(c.ExcludeLists, r.List) {
			continue
		}
		if (len(c.Tags) > 0 && !anyFold(c.Tags, r.Tags)) || anyFold(c.ExcludeTags, r.Tags) {
			continue
		}
		t, ok := parseTime(r.Due, loc)
		if !ok {
			continue
		}
		out = append(out, Item{ID: r.ID, Title: r.Title, At: t, AllDay: r.AllDay, Detail: r.List, Ref: "reminder:" + r.ID})
	}
	return out, nil
}

func calendar(ctx context.Context, c config.Connector, w Window) ([]Item, error) {
	from := w.From
	if from.IsZero() {
		y, m, d := w.Now.Date()
		from = time.Date(y, m, d, 0, 0, 0, 0, w.Now.Location())
	}
	cals := c.Calendars
	if len(cals) == 0 {
		cals = []string{""}
	}
	loc := w.Now.Location()
	var out []Item
	for _, cal := range cals {
		args := []string{"calendar", "events", "list", "--from", from.Format(time.RFC3339), "--to", w.Until.Format(time.RFC3339), "--format", "json", "--limit", "0"}
		if cal != "" {
			args = append(args, "--calendar", cal)
		}
		var res struct {
			Events []struct {
				ID       string `json:"id"`
				Title    string `json:"title"`
				Start    string `json:"startDate"`
				AllDay   bool   `json:"isAllDay"`
				Calendar string `json:"calendar"`
				Status   string `json:"status"`
			} `json:"events"`
		}
		if err := run(ctx, &res, bin(c, "macos"), args...); err != nil {
			return nil, err
		}
		for _, e := range res.Events {
			if e.Status == "canceled" || e.Status == "cancelled" {
				continue
			}
			t, ok := parseTime(e.Start, loc)
			if !ok {
				continue
			}
			out = append(out, Item{ID: e.ID, Title: e.Title, At: t, AllDay: e.AllDay, Detail: e.Calendar, Ref: "event:" + e.ID})
		}
	}
	return out, nil
}

func office(ctx context.Context, c config.Connector, w Window) ([]Item, error) {
	args := []string{"ls", "--status", "waiting", "--format", "json"}
	if c.Office != "" {
		args = append(args, "--office", c.Office)
	}
	var res []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		WaitingOn string `json:"waiting_on"`
		WaitUntil string `json:"wait_until"`
		Since     string `json:"waiting_since"`
	}
	if err := run(ctx, &res, bin(c, "office"), args...); err != nil {
		return nil, err
	}
	loc := w.Now.Location()
	var out []Item
	for _, d := range res {
		t, ok := parseTime(d.WaitUntil, loc)
		if d.WaitUntil == "" || !ok {
			continue
		}
		detail := ""
		if d.WaitingOn != "" {
			detail = "attend " + d.WaitingOn
		}
		it := Item{ID: d.ID, Title: d.Title, At: t, Detail: detail, Ref: "office:" + d.ID}
		if s, ok := parseTime(d.Since, loc); d.Since != "" && ok {
			it.Since = &s
			days := int(w.Now.Sub(s).Hours() / 24)
			it.Detail = strings.TrimPrefix(fmt.Sprintf("%s · depuis %d j", detail, days), " · ")
		}
		out = append(out, it)
	}
	return out, nil
}

func routine(ctx context.Context, c config.Connector, w Window) ([]Item, error) {
	var res struct {
		Routines []struct {
			ID         string   `json:"id"`
			Active     bool     `json:"active"`
			Rrules     []string `json:"rrules"`
			Recurrence string   `json:"recurrence"`
			Next       string   `json:"next"`
			Owner      string   `json:"owner"`
		} `json:"routines"`
	}
	if err := run(ctx, &res, bin(c, "routine"), "ls", "--json"); err != nil {
		return nil, err
	}
	loc := w.Now.Location()
	var out []Item
	for _, r := range res.Routines {
		if !r.Active || r.Next == "" || !keepRoutine(c, r.ID, r.Owner, r.Rrules) {
			continue
		}
		t, ok := parseTime(r.Next, loc)
		if !ok {
			continue
		}
		out = append(out, Item{ID: r.ID, Title: r.ID, At: t, Detail: r.Recurrence, Ref: "routine:" + r.ID})
	}
	return out, nil
}

func keepRoutine(c config.Connector, id, owner string, rrules []string) bool {
	if len(c.Owners) > 0 {
		ok := false
		for _, o := range c.Owners {
			if (o == "-" && owner == "") || (o != "-" && strings.HasPrefix(owner, o)) {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	if len(c.Include) > 0 && !globAny(c.Include, id) {
		return false
	}
	if globAny(c.Exclude, id) {
		return false
	}
	if !c.Frequent {
		frequent := len(rrules) > 0
		for _, r := range rrules {
			u := strings.ToUpper(r)
			if !strings.Contains(u, "FREQ=MINUTELY") && !strings.Contains(u, "FREQ=HOURLY") && !strings.Contains(u, "FREQ=SECONDLY") {
				frequent = false
			}
		}
		if frequent {
			return false
		}
	}
	return true
}

// globAny matches ids against globs where * also crosses /.
func globAny(globs []string, s string) bool {
	for _, g := range globs {
		re := "^" + strings.ReplaceAll(strings.ReplaceAll(regexp.QuoteMeta(g), `\*`, ".*"), `\?`, ".") + "$"
		if ok, _ := regexp.MatchString(re, s); ok {
			return true
		}
	}
	return false
}

func oj(ctx context.Context, c config.Connector, w Window) ([]Item, error) {
	sphere := c.OJSphere
	if sphere == "" {
		sphere = w.Sphere
	}
	var sittings []struct {
		ID      string `json:"id"`
		Meeting string `json:"meeting"`
		Date    string `json:"date"`
		Time    string `json:"time"`
		State   string `json:"state"`
		Order   []string
	}
	if err := run(ctx, &sittings, bin(c, "oj"), "sitting", "ls", "--sphere", sphere, "--format", "json"); err != nil {
		return nil, err
	}
	loc := w.Now.Location()
	var out []Item
	for _, s := range sittings {
		if s.State != "planned" && s.State != "frozen" {
			continue
		}
		at, allDay := s.Date, true
		if s.Time != "" {
			at, allDay = s.Date+"T"+s.Time, false
		}
		t, ok := parseTime(at, loc)
		if !ok {
			continue
		}
		detail := s.State
		if n := len(s.Order); n > 0 {
			detail = fmt.Sprintf("%s, %d points", s.State, n)
		}
		out = append(out, Item{ID: s.ID, Title: "Séance " + s.Meeting, At: t, AllDay: allDay, Detail: detail, Ref: "oj:" + s.ID})
	}
	if c.SittingsOnly {
		return out, nil
	}
	var actions []struct {
		Item  string `json:"item"`
		Title string `json:"title"`
		N     int    `json:"n"`
		What  string `json:"what"`
		Who   string `json:"who"`
		Due   string `json:"due"`
	}
	if err := run(ctx, &actions, bin(c, "oj"), "actions", "ls", "--sphere", sphere, "--state", "open", "--format", "json"); err != nil {
		return nil, err
	}
	for _, a := range actions {
		t, ok := parseTime(a.Due, loc)
		if a.Due == "" || !ok {
			continue
		}
		detail := a.Title
		if a.Who != "" {
			detail = a.Who + " · " + detail
		}
		id := fmt.Sprintf("%s#%d", a.Item, a.N)
		out = append(out, Item{ID: id, Title: a.What, At: t, AllDay: isDate(a.Due), Detail: detail, Ref: "oj:" + a.Item})
	}
	return out, nil
}

// command runs any tool that prints a JSON list of {id, title, at, all_day,
// detail, ref}, bare or as {"ok": true, "result": [...]} or {"items": [...]}.
func command(ctx context.Context, c config.Connector, w Window) ([]Item, error) {
	if len(c.Run) == 0 {
		return nil, fmt.Errorf("no run given")
	}
	from := ""
	if !w.From.IsZero() {
		from = w.From.Format(time.RFC3339)
	}
	argv := make([]string, len(c.Run))
	for i, a := range c.Run {
		a = strings.ReplaceAll(a, "{from}", from)
		a = strings.ReplaceAll(a, "{until}", w.Until.Format(time.RFC3339))
		a = strings.ReplaceAll(a, "{sphere}", w.Sphere)
		argv[i] = config.Expand(a)
	}
	var raw json.RawMessage
	if err := run(ctx, &raw, argv[0], argv[1:]...); err != nil {
		return nil, err
	}
	type line struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		At     string `json:"at"`
		AllDay bool   `json:"all_day"`
		Detail string `json:"detail"`
		Ref    string `json:"ref"`
		Kind   string `json:"kind"`
	}
	var lines []line
	if err := json.Unmarshal(raw, &lines); err != nil {
		var wrapped struct {
			Items []line `json:"items"`
		}
		if err2 := json.Unmarshal(raw, &wrapped); err2 != nil {
			return nil, fmt.Errorf("expected a JSON list of {id, title, at}: %v", err)
		}
		lines = wrapped.Items
	}
	loc := w.Now.Location()
	var out []Item
	for _, l := range lines {
		t, ok := parseTime(l.At, loc)
		if !ok {
			continue
		}
		allDay := l.AllDay || isDate(l.At)
		past := t.Before(w.Now)
		if allDay {
			past = endOfDay(t).Before(w.Now)
		}
		if past && len(c.PastKinds) > 0 && !containsFold(c.PastKinds, l.Kind) {
			continue
		}
		detail := l.Detail
		if l.Kind != "" && detail != "" {
			detail = kindLabel(l.Kind) + " · " + detail
		} else if l.Kind != "" {
			detail = kindLabel(l.Kind)
		}
		out = append(out, Item{ID: l.ID, Title: l.Title, At: t, AllDay: allDay, Detail: detail, Ref: l.Ref})
	}
	return out, nil
}

// kindLabel names a kind of date in French, as the list shows it.
func kindLabel(k string) string {
	if l, ok := map[string]string{"contract": "contrat", "warranty": "garantie", "renewal": "renouvellement",
		"legal": "délai légal", "payment": "paiement", "appointment": "rendez-vous", "other": "autre"}[k]; ok {
		return l
	}
	return k
}

// task reads the dated tasks of the task tool: the owner's and those followed.
func task(ctx context.Context, c config.Connector, w Window) ([]Item, error) {
	sphere := c.TaskSphere
	if sphere == "" {
		sphere = w.Sphere
	}
	var res struct {
		Items []struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			Who       string `json:"who"`
			Due       string `json:"due"`
			State     string `json:"state"`
			WaitingOn string `json:"waiting_on"`
			Mine      bool   `json:"mine"`
			Ref       string `json:"ref"`
		} `json:"items"`
	}
	if err := run(ctx, &res, bin(c, "task"), "ls", "--format", "json", "--sphere", sphere); err != nil {
		return nil, err
	}
	loc := w.Now.Location()
	var out []Item
	for _, t := range res.Items {
		at, ok := parseTime(t.Due, loc)
		if t.Due == "" || !ok {
			continue
		}
		var parts []string
		if !t.Mine && t.Who != "" {
			parts = append(parts, t.Who)
		}
		if t.WaitingOn != "" {
			parts = append(parts, "attend "+t.WaitingOn)
		}
		if t.State == "proposed" {
			parts = append(parts, "proposée")
		}
		ref := t.Ref
		if ref == "" {
			ref = "task:" + t.ID
		}
		out = append(out, Item{ID: t.ID, Title: t.Title, At: at, AllDay: isDate(t.Due), Detail: strings.Join(parts, " · "), Ref: ref})
	}
	return out, nil
}
