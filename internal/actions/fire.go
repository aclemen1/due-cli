package actions

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/launcher"
	"github.com/aclemen1/due-cli/internal/ledger"
	"github.com/aclemen1/due-cli/internal/spec"
	"github.com/aclemen1/due-cli/internal/trigger"
)

func registerFire() {
	spec.Register(&spec.Action{
		Category: "due", Name: "run", Top: true,
		Summary:    "Fire an entry now, by hand: its action, or with --notice the message of a notice.",
		Discussion: "The firing is recorded in the entry with its result and log. The planned instants stay as they are.",
		Params: []spec.Param{
			idParam(),
			{Name: "notice", Kind: spec.Bool, Help: "Send the notice message instead of running the action."},
			writeSphereParam(),
		},
		Effects:  []string{"Runs the action or the tell command of the sphere.", "Records the firing in the entry and commits."},
		Examples: []string{"due run 7 --sphere perso", "due run 7 --notice --sphere perso"},
		Run: func(ctx *spec.Context) (any, error) {
			_, l, err := Open(ctx)
			if err != nil {
				return nil, err
			}
			kind := "term"
			if ctx.Bool("notice") {
				kind = "notice"
			}
			return trigger.Fire(l, ctx.Str("id"), kind, Now())
		},
		Text: textFired,
	})
	spec.Register(&spec.Action{
		Category: "setup", Name: "tick", Top: true,
		Summary: "Fire what came in every sphere: notices tell, terms run their action. The launcher calls it every minute.",
		Discussion: "Missed instants (the Mac asleep) collapse: only the latest fires, the earlier ones are recorded as skipped. " +
			"Notices that precede an entry's creation never fire. A second tick while one runs does nothing.",
		Effects:  []string{"Runs actions; records each firing in its entry and commits."},
		Examples: []string{"due tick"},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, err := config.Load(ctx.Config)
			if err != nil {
				return nil, err
			}
			fired, warnings, err := trigger.Tick(cfg, Now(), func(sphere string) (*ledger.Ledger, error) {
				return OpenLedger(ctx, cfg, sphere)
			})
			for _, w := range warnings {
				ctx.Warn(w)
			}
			if fired == nil {
				fired = []trigger.Fired{}
			}
			return fired, err
		},
		Text: textFired,
	})
	spec.Register(&spec.Action{
		Category: "launcher", Name: "install",
		Summary:  "Install and load due's launch agent: `due tick` every minute and at login.",
		Params:   []spec.Param{{Name: "bin", Kind: spec.String, Help: "Path of the due binary. Defaults to this one."}},
		Effects:  []string{"Writes ~/Library/LaunchAgents/" + launcher.Label + ".plist and loads it with launchctl."},
		Examples: []string{"due launcher install"},
		Run: func(ctx *spec.Context) (any, error) {
			exe := config.Expand(ctx.Str("bin"))
			if exe == "" {
				var err error
				if exe, err = os.Executable(); err != nil {
					return nil, err
				}
				if r, err := filepath.EvalSymlinks(exe); err == nil {
					exe = r
				}
			}
			cfgPath := ""
			if ctx.Config != "" {
				cfgPath = config.Path(ctx.Config)
			}
			if err := launcher.Install(exe, cfgPath); err != nil {
				return nil, err
			}
			return launcher.Read(), nil
		},
	})
	spec.Register(&spec.Action{
		Category: "launcher", Name: "uninstall", Destructive: true,
		Summary:  "Unload due's launch agent and remove its plist. Nothing fires any more.",
		Effects:  []string{"Unloads the launch agent and deletes its plist."},
		Examples: []string{"due launcher uninstall"},
		Run: func(ctx *spec.Context) (any, error) {
			if err := launcher.Uninstall(); err != nil {
				return nil, err
			}
			return launcher.Read(), nil
		},
	})
	spec.Register(&spec.Action{
		Category: "launcher", Name: "status",
		Summary:  "Say whether the launch agent is installed, loaded and stopped.",
		Examples: []string{"due launcher status --format text"},
		Run: func(ctx *spec.Context) (any, error) {
			_, err := os.Stat(trigger.StopFile())
			return struct {
				launcher.Status
				Stopped bool `json:"stopped"`
			}{launcher.Read(), err == nil}, nil
		},
	})
	spec.Register(&spec.Action{
		Category: "launcher", Name: "stop",
		Summary:  "Kill switch: ticks keep running but fire nothing until `due launcher start`.",
		Effects:  []string{"Creates the stop file in ~/.local/state/due."},
		Examples: []string{"due launcher stop"},
		Run: func(ctx *spec.Context) (any, error) {
			if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
				return nil, err
			}
			return map[string]bool{"stopped": true}, os.WriteFile(trigger.StopFile(), nil, 0o644)
		},
	})
	spec.Register(&spec.Action{
		Category: "launcher", Name: "start",
		Summary:  "Lift the kill switch: ticks fire again. Missed instants fire once, the latest only.",
		Effects:  []string{"Removes the stop file."},
		Examples: []string{"due launcher start"},
		Run: func(ctx *spec.Context) (any, error) {
			if err := os.Remove(trigger.StopFile()); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			return map[string]bool{"stopped": false}, nil
		},
	})
}

func textFired(w io.Writer, v any) {
	var list []trigger.Fired
	switch x := v.(type) {
	case trigger.Fired:
		list = []trigger.Fired{x}
	case []trigger.Fired:
		list = x
	}
	for _, f := range list {
		fmt.Fprintf(w, "%s %s %s (%s): %s\n", f.Sphere, f.ID, f.Kind, f.Title, f.Result)
	}
}
