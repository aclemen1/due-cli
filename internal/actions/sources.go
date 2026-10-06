package actions

import (
	"fmt"
	"io"
	"time"

	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/connect"
	"github.com/aclemen1/due-cli/internal/spec"
	"github.com/aclemen1/due-cli/internal/when"
)

type SourceRow struct {
	Sphere   string `json:"sphere"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Off      bool   `json:"off,omitempty"`
	Items    *int   `json:"items,omitempty"`
	Error    string `json:"error,omitempty"`
	Duration string `json:"duration,omitempty"`
}

func registerSources() {
	spec.Register(&spec.Action{
		Category: "source", Name: "ls",
		Summary:    "List the sources: each sphere's ledger and the connectors of the configuration.",
		Discussion: "Connectors are declared under spheres.<sphere>.connectors in ~/.config/due/config.yaml. With --check, each one is run over its sphere's horizon.",
		Params: []spec.Param{
			{Name: "check", Kind: spec.Bool, Help: "Run each connector and report its count or error."},
			readSphereParam(),
		},
		Examples: []string{"due source ls", "due source ls --check --sphere perso --format text"},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, err := config.Load(ctx.Config)
			if err != nil {
				return nil, err
			}
			spheres, err := ReadSpheres(ctx, cfg)
			if err != nil {
				return nil, err
			}
			var rows []SourceRow
			now := Now()
			for _, sphere := range spheres {
				s := cfg.Spheres[sphere]
				rows = append(rows, SourceRow{Sphere: sphere, Name: "due", Type: "ledger"})
				until, err := when.Horizon(s.Horizon, now, cfg.DefaultTime)
				if err != nil {
					return nil, err
				}
				for _, c := range s.Connectors {
					r := SourceRow{Sphere: sphere, Name: c.Name, Type: c.Type, Off: c.Off}
					if ctx.Bool("check") && !c.Off {
						start := time.Now()
						items, err := connect.One(c, connect.Window{Until: until, Now: now, Sphere: sphere})
						r.Duration = time.Since(start).Round(time.Millisecond).String()
						if err != nil {
							r.Error = err.Error()
						} else {
							n := len(items)
							r.Items = &n
						}
					}
					rows = append(rows, r)
				}
			}
			return rows, nil
		},
		Text: func(w io.Writer, v any) {
			for _, r := range v.([]SourceRow) {
				line := fmt.Sprintf("%-6s %-12s %-10s", r.Sphere, r.Name, r.Type)
				switch {
				case r.Off:
					line += " off"
				case r.Error != "":
					line += " error: " + r.Error
				case r.Items != nil:
					line += fmt.Sprintf(" %d lines in %s", *r.Items, r.Duration)
				}
				fmt.Fprintln(w, line)
			}
		},
	})
}
