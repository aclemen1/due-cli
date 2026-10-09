package ledger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Ack takes a line of another tool off due: seen and settled. It holds for
// that date; if the source moves the date, the line comes back.
type Ack struct {
	Source string `yaml:"source" json:"source"`
	ID     string `yaml:"id" json:"id"`
	At     string `yaml:"at" json:"at"` // RFC 3339 of the line acknowledged
	Title  string `yaml:"title" json:"title"`
	By     string `yaml:"by" json:"by"`
	When   string `yaml:"when" json:"when"`
	Note   string `yaml:"note,omitempty" json:"note,omitempty"`
}

// BaseID is a line's id without its occurrence (01M4…#0@2026-10-25 → 01M4…#0).
func BaseID(id string) string {
	base, _, _ := strings.Cut(id, "@")
	return base
}

// AckKey names an acknowledged line.
func AckKey(source, id, at string) string {
	return source + "\x00" + BaseID(id) + "\x00" + at
}

func (l *Ledger) acksPath() string { return filepath.Join(l.Root, "acks.yaml") }

// Acks reads the acknowledged lines of the sphere.
func (l *Ledger) Acks() ([]Ack, error) {
	b, err := os.ReadFile(l.acksPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Ack
	return out, yaml.Unmarshal(b, &out)
}

// SaveAcks writes them; an empty list removes the file.
func (l *Ledger) SaveAcks(acks []Ack) error {
	if len(acks) == 0 {
		err := os.Remove(l.acksPath())
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	b, err := yaml.Marshal(acks)
	if err != nil {
		return err
	}
	tmp := l.acksPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, l.acksPath())
}

// AckSet is the keys of the acknowledged lines.
func (l *Ledger) AckSet() map[string]bool {
	acks, _ := l.Acks()
	out := map[string]bool{}
	for _, a := range acks {
		out[AckKey(a.Source, a.ID, a.At)] = true
	}
	return out
}
