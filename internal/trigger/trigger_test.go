package trigger

import (
	"testing"
	"time"

	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/ledger"
)

func TestChannels(t *testing.T) {
	for n, want := range map[string]string{"30d": Mail, "7d": Mail, "6d": Tell, "1d": Tell, "2h": Tell} {
		if got := NoticeChannel(n); got != want {
			t.Errorf("notice %s: %s, want %s", n, got, want)
		}
	}
	l := &ledger.Ledger{Sphere: "perso", Clock: "09:00", Now: time.Now, Actions: config.Actions{Tell: []string{"t"}}}
	e := &ledger.Entry{ID: "PE-0001", At: "2026-12-01", Do: "tell"}
	if got := EntryChannel(l, e, ledger.Instant{Kind: "notice 14d"}); got != Mail {
		t.Errorf("far notice: %s", got)
	}
	if got := EntryChannel(l, e, ledger.Instant{Kind: "term"}); got != Tell {
		t.Errorf("term: %s", got)
	}
	e.Via = Push
	if got := EntryChannel(l, e, ledger.Instant{Kind: "notice 14d"}); got != Push {
		t.Errorf("--via wins: %s", got)
	}
	if tmpl, used := ChannelCommand(l, Mail); used != Tell || tmpl[0] != "t" {
		t.Errorf("no mail command falls back to tell: %v %s", tmpl, used)
	}
}
