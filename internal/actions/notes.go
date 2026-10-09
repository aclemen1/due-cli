package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/spec"
)

// Note is a note of another tool tied to an entry.
type Note struct {
	ID      string `json:"id"`
	By      string `json:"by,omitempty"`
	Created string `json:"created,omitempty"`
	Body    string `json:"body"`
}

func notesArgv(tmpl []string, ref, sphere string) []string {
	argv := make([]string, len(tmpl))
	for i, a := range tmpl {
		a = strings.ReplaceAll(a, "{ref}", ref)
		a = strings.ReplaceAll(a, "{sphere}", sphere)
		argv[i] = config.Expand(a)
	}
	return argv
}

// NotesOf lists the notes that cite ref; none when no notes command is set.
func NotesOf(cfg *config.Config, ref string) ([]Note, error) {
	if len(cfg.Notes.Ls) == 0 {
		return nil, nil
	}
	argv := notesArgv(cfg.Notes.Ls, ref, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %v", argv[0], err)
	}
	var env struct {
		OK     *bool           `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	raw := json.RawMessage(out)
	if json.Unmarshal(out, &env) == nil && env.OK != nil {
		raw = env.Result
	}
	var notes []Note
	if json.Unmarshal(raw, &notes) != nil {
		var wrapped struct {
			Items []Note `json:"items"`
		}
		if err := json.Unmarshal(raw, &wrapped); err != nil {
			return nil, fmt.Errorf("%s: unreadable JSON: %v", argv[0], err)
		}
		notes = wrapped.Items
	}
	return notes, nil
}

// AddNote adds a note citing ref, in sphere.
func AddNote(cfg *config.Config, ref, sphere, text string) error {
	if len(cfg.Notes.Add) == 0 {
		return spec.UserError("no notes command: set notes.add in %s", config.Path(""))
	}
	argv := notesArgv(cfg.Notes.Add, ref, sphere)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %v: %s", argv[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RefItems runs a ref source and returns its refs and labels.
func RefItems(s config.RefSource) [][2]string {
	if len(s.Run) == 0 || s.Value == "" {
		return nil
	}
	argv := notesArgv(s.Run, "", "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		return nil
	}
	var env struct {
		OK     *bool           `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	raw := json.RawMessage(out)
	if json.Unmarshal(out, &env) == nil && env.OK != nil {
		raw = env.Result
	}
	var rows []map[string]any
	if json.Unmarshal(raw, &rows) != nil {
		var wrapped struct {
			Items []map[string]any `json:"items"`
		}
		if json.Unmarshal(raw, &wrapped) != nil {
			return nil
		}
		rows = wrapped.Items
	}
	var items [][2]string
	for _, r := range rows {
		v := fmt.Sprint(r[s.Value])
		if v == "" || v == "<nil>" {
			continue
		}
		label := ""
		if s.Label != "" && r[s.Label] != nil {
			label = fmt.Sprint(r[s.Label])
		}
		items = append(items, [2]string{s.Prefix + v, label})
	}
	return items
}
