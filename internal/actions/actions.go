// Package actions declares every due action once; the CLI, the schema and
// the MCP server are projections of these declarations.
package actions

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/ledger"
	"github.com/aclemen1/due-cli/internal/spec"
)

const Version = "0.1.0"

//go:embed skill.md
var skillText string

// clock replaces the real clock in tests.
var clock func() time.Time

// SetClock fixes the time the actions see; nil restores the real clock.
func SetClock(f func() time.Time) { clock = f }

func Now() time.Time {
	if clock != nil {
		return clock()
	}
	return time.Now()
}

func readSphereParam() spec.Param {
	return spec.Param{Name: "sphere", Kind: spec.String, Help: "Read only this sphere, e.g. pro. Defaults to $DUE_SPHERE, else every sphere."}
}

func writeSphereParam() spec.Param {
	return spec.Param{Name: "sphere", Kind: spec.String, Required: true,
		Help:    "Sphere the entry belongs to, by what it is about: pro for work, perso otherwise. Never a default.",
		Missing: "a write needs --sphere: the sphere follows what the entry is about, pro for work, perso otherwise; there is no default, not even $DUE_SPHERE"}
}

func checkSphere(ctx *spec.Context, cfg *config.Config, sphere string) error {
	if ctx.Spheres != nil && !contains(ctx.Spheres, sphere) {
		return spec.Forbidden("sphere %q is not served here; served: %s", sphere, strings.Join(ctx.Spheres, ", "))
	}
	if _, ok := cfg.Spheres[sphere]; !ok {
		return spec.UserError("unknown sphere %q; configured: %s. Example: due init --sphere perso --root ~/due/perso", sphere, strings.Join(cfg.Names(), ", "))
	}
	return nil
}

// ReadSpheres are the spheres a read covers: the one given, else $DUE_SPHERE
// on the command line, else every sphere configured or served.
func ReadSpheres(ctx *spec.Context, cfg *config.Config) ([]string, error) {
	sphere := ctx.Str("sphere")
	if sphere == "" && ctx.Spheres == nil {
		sphere = os.Getenv("DUE_SPHERE")
	}
	if sphere != "" {
		return []string{sphere}, checkSphere(ctx, cfg, sphere)
	}
	if ctx.Spheres != nil {
		return ctx.Spheres, nil
	}
	if len(cfg.Spheres) == 0 {
		return nil, spec.UserError("no sphere is configured. Example: due init --sphere perso --root ~/due/perso")
	}
	return cfg.Names(), nil
}

// WriteSphere is the sphere a write goes to: always given, never a default,
// since it follows what the entry is about.
func WriteSphere(ctx *spec.Context, cfg *config.Config) (string, error) {
	sphere := ctx.Str("sphere")
	if sphere == "" {
		return "", spec.UserError("a write needs --sphere (%s): the sphere follows what the entry is about, pro for work, perso otherwise; there is no default. Example: due add \"Renouveler le contrat\" --at 2027-03-31 --sphere pro",
			strings.Join(cfg.Names(), ", "))
	}
	return sphere, checkSphere(ctx, cfg, sphere)
}

// Open returns the configuration and the ledger a write goes to.
func Open(ctx *spec.Context) (*config.Config, *ledger.Ledger, error) {
	cfg, err := config.Load(ctx.Config)
	if err != nil {
		return nil, nil, err
	}
	sphere, err := WriteSphere(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	l, err := OpenLedger(ctx, cfg, sphere)
	return cfg, l, err
}

// OpenLedger opens a sphere's ledger with the call's author and clock.
func OpenLedger(ctx *spec.Context, cfg *config.Config, sphere string) (*ledger.Ledger, error) {
	by := os.Getenv("DUE_BY")
	if ctx != nil && ctx.Spheres != nil && !strings.HasPrefix(by, "agent:") {
		by = "agent:mcp"
	}
	l, err := ledger.OpenLedger(cfg, sphere, by)
	if err != nil {
		return nil, err
	}
	l.Now = Now
	if ctx != nil {
		l.Warn = ctx.Warn
	}
	return l, nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func init() {
	registerEntries()
	registerList()
	registerFire()
	registerSources()
	registerJudge()
	registerMeta()
}

func registerMeta() {
	spec.Register(&spec.Action{
		Category: "setup", Name: "init", Top: true,
		Summary: "Declare a sphere and create its ledger.",
		Params: []spec.Param{
			{Name: "sphere", Kind: spec.String, Required: true, Help: "Sphere name, e.g. perso."},
			{Name: "root", Kind: spec.String, Required: true, Help: "Directory of the sphere's ledger, e.g. ~/due/perso."},
			{Name: "vcs", Kind: spec.String, Default: "jj", Enum: []string{"jj", "git", "none"}, Help: "Version control of the ledger: a commit after each change."},
		},
		Effects:  []string{"Adds the sphere to the configuration file.", "Creates the ledger directory and, with jj or git, its repository."},
		Examples: []string{"due init --sphere perso --root ~/due/perso", "due init --sphere pro --root ~/due/pro --vcs none"},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, err := config.Load(ctx.Config)
			if err != nil {
				return nil, err
			}
			name, root, vcs := ctx.Str("sphere"), config.Expand(ctx.Str("root")), ctx.Str("vcs")
			if old, ok := cfg.Spheres[name]; ok && old.Root != root {
				return nil, spec.Conflict("sphere %s already has its ledger at %s", name, old.Root)
			}
			if err := ledger.Init(root, vcs); err != nil {
				return nil, err
			}
			s := cfg.Spheres[name]
			s.Root, s.VCS = root, vcs
			if err := cfg.AddSphere(name, s); err != nil {
				return nil, spec.UserError("%v", err)
			}
			return map[string]any{"sphere": name, "root": root, "vcs": vcs, "config": cfg.File()}, nil
		},
	})
	spec.Register(&spec.Action{
		Category: "meta", Name: "version", Top: true, Meta: true, Summary: "Print the due version.",
		Examples: []string{"due version"},
		Run:      func(*spec.Context) (any, error) { return Version, nil },
	})
	spec.Register(&spec.Action{
		Category: "meta", Name: "schema", Top: true, Meta: true,
		Summary: "Browse actions: catalog, category, or one action's full spec.",
		Params: []spec.Param{
			{Name: "category", Kind: spec.String, Positional: true, Help: "Category to list."},
			{Name: "action", Kind: spec.String, Positional: true, Help: "Action to describe."},
			{Name: "search", Kind: spec.String, Help: "Match actions across categories."},
		},
		Examples: []string{"due schema", "due schema due", "due schema due add", "due schema --search notice"},
		Run: func(ctx *spec.Context) (any, error) {
			if q := ctx.Str("search"); q != "" {
				return spec.Search(q), nil
			}
			cat, act := ctx.Str("category"), ctx.Str("action")
			switch {
			case cat == "":
				return spec.Catalog(), nil
			case act == "":
				l := spec.ActionsIn(cat)
				if len(l) == 0 {
					return nil, spec.NotFound("no category %q. Categories: %s", cat, strings.Join(spec.Categories(), ", "))
				}
				return l, nil
			}
			a := spec.Find(cat, act)
			if a == nil {
				return nil, spec.NotFound("no action %q in %q. Try `due schema %s`", act, cat, cat)
			}
			return spec.Leaf{Action: a, Usage: spec.Usage(a)}, nil
		},
		Text: spec.TextSchema,
	})
	spec.Register(&spec.Action{
		Category: "meta", Name: "skill", Top: true, Meta: true,
		Summary: "Print the embedded agent skill, or install it for an agent harness.",
		Params: []spec.Param{
			{Name: "verb", Kind: spec.String, Positional: true, Default: "show", Enum: []string{"show", "install"}, Help: "show or install"},
			{Name: "for", Kind: spec.String, Default: "claude", Help: "Harness to install for: claude."},
			{Name: "dir", Kind: spec.String, Help: "Install into this directory instead."},
		},
		Effects:  []string{"install: writes SKILL.md into ~/.claude/skills/due/ (or --dir)."},
		Examples: []string{"due skill show", "due skill install --for claude"},
		Run: func(ctx *spec.Context) (any, error) {
			if ctx.Str("verb") != "install" {
				return strings.TrimSpace(skillText), nil
			}
			if f := ctx.Str("for"); f != "claude" && f != "claude-code" {
				return nil, spec.UserError("--for takes claude, got %q. Example: due skill install --for claude", f)
			}
			dir := config.Expand(ctx.Str("dir"))
			if dir == "" {
				home, _ := os.UserHomeDir()
				dir = filepath.Join(home, ".claude", "skills", "due")
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
			p := filepath.Join(dir, "SKILL.md")
			if err := os.WriteFile(p, []byte(skillText), 0o644); err != nil {
				return nil, err
			}
			return "installed " + p, nil
		},
	})
}
