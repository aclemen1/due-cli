// Package config reads ~/.config/due/config.yaml.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// DefaultTime is the hour of an entry given as a date alone, and of its notices.
	DefaultTime string            `yaml:"default_time,omitempty"`
	Spheres     map[string]Sphere `yaml:"spheres"`

	path string
}

type Sphere struct {
	Root string `yaml:"root"`
	// Prefix starts the ids of the sphere's entries: P gives PE-0001. Defaults
	// to the sphere's initial; two spheres never share one.
	Prefix     string      `yaml:"prefix,omitempty"`
	VCS        string      `yaml:"vcs,omitempty"`     // jj (default), git, none
	Horizon    string      `yaml:"horizon,omitempty"` // window of ls, default 30d
	Actions    Actions     `yaml:"actions,omitempty"`
	Connectors []Connector `yaml:"connectors,omitempty"`
}

// Actions are the commands run when an entry fires. Placeholders: {id}, {dossier} (office:<id> of the ref, else desk),
// {title}, {message}, {prompt}, {at}, {when}, {sphere}, {cwd}, {ref}.
type Actions struct {
	Tell    []string `yaml:"tell,omitempty"`
	Agent   []string `yaml:"agent,omitempty"`
	Shell   string   `yaml:"shell,omitempty"`   // runs do: command, default /bin/zsh
	Timeout string   `yaml:"timeout,omitempty"` // per action, default 10min
}

// Connector brings entries from another tool, read at each ls and never copied.
type Connector struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"` // reminders, calendar, office, routine, oj, command
	// Bin replaces the tool's executable (macos, office, routine, oj).
	Bin     string `yaml:"bin,omitempty"`
	Timeout string `yaml:"timeout,omitempty"` // default 20s
	Off     bool   `yaml:"off,omitempty"`

	Lists       []string `yaml:"lists,omitempty"`        // reminders: keep these lists
	Tags        []string `yaml:"tags,omitempty"`         // reminders: keep those with one of these tags
	ExcludeTags []string `yaml:"exclude_tags,omitempty"` // reminders: drop those with one of these tags
	Calendars   []string `yaml:"calendars,omitempty"`    // calendar: these calendars
	Office      string   `yaml:"office,omitempty"`       // office: directory of the office
	OJSphere    string   `yaml:"oj_sphere,omitempty"`    // oj: its sphere
	Owners      []string `yaml:"owners,omitempty"`       // routine: owner prefixes kept; "-" keeps routines without owner
	Include     []string `yaml:"include,omitempty"`      // routine: id globs kept
	Exclude     []string `yaml:"exclude,omitempty"`      // routine: id globs dropped
	Frequent    bool     `yaml:"frequent,omitempty"`     // routine: keep minutely and hourly routines
	Run         []string `yaml:"run,omitempty"`          // command: argv with {from}, {until}, {sphere}
	PastKinds   []string `yaml:"past_kinds,omitempty"`   // command: past lines kept only for these kinds
}

var (
	sphereName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	prefixRe   = regexp.MustCompile(`^[A-Z]{1,3}$`)
)

var Types = []string{"reminders", "calendar", "office", "routine", "oj", "command"}

// Path resolves the configuration file: the flag, then DUE_CONFIG, then
// ~/.config/due/config.yaml.
func Path(flag string) string {
	if flag != "" {
		return Expand(flag)
	}
	if env := os.Getenv("DUE_CONFIG"); env != "" {
		return Expand(env)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "due", "config.yaml")
}

// Load reads the configuration. A missing file yields an empty one.
func Load(flag string) (*Config, error) {
	p := Path(flag)
	c := &Config{Spheres: map[string]Sphere{}, path: p}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		c.fill()
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	c.fill()
	owner := map[string]string{}
	for _, name := range c.Names() {
		s := c.Spheres[name]
		if !prefixRe.MatchString(s.Prefix) {
			return nil, fmt.Errorf("%s: sphere %s: prefix %q must be one to three capital letters, e.g. P", p, name, s.Prefix)
		}
		if other, ok := owner[s.Prefix]; ok {
			return nil, fmt.Errorf("%s: spheres %s and %s share the prefix %s; give one of them its own, e.g. prefix: U", p, other, name, s.Prefix)
		}
		owner[s.Prefix] = name
		for i, k := range s.Connectors {
			if k.Name == "" {
				return nil, fmt.Errorf("%s: sphere %s, connector %d has no name", p, name, i+1)
			}
			if !contains(Types, k.Type) {
				return nil, fmt.Errorf("%s: connector %s: type %q; expected one of %s", p, k.Name, k.Type, strings.Join(Types, ", "))
			}
		}
	}
	return c, nil
}

func (c *Config) fill() {
	if c.Spheres == nil {
		c.Spheres = map[string]Sphere{}
	}
	if c.DefaultTime == "" {
		c.DefaultTime = "09:00"
	}
	for name, s := range c.Spheres {
		s.Root = Expand(s.Root)
		if s.Prefix == "" {
			s.Prefix = strings.ToUpper(name[:1])
		}
		s.Prefix = strings.ToUpper(s.Prefix)
		if s.VCS == "" {
			s.VCS = "jj"
		}
		if s.Horizon == "" {
			s.Horizon = "30d"
		}
		if s.Actions.Shell == "" {
			s.Actions.Shell = "/bin/zsh"
		}
		if s.Actions.Timeout == "" {
			s.Actions.Timeout = "10min"
		}
		for i := range s.Connectors {
			s.Connectors[i].Office = Expand(s.Connectors[i].Office)
		}
		c.Spheres[name] = s
	}
}

func (c *Config) File() string { return c.path }

func (c *Config) Names() []string {
	var out []string
	for n := range c.Spheres {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// AddSphere declares a sphere and writes the file.
func (c *Config) AddSphere(name string, s Sphere) error {
	if !sphereName.MatchString(name) {
		return fmt.Errorf("sphere name %q: use lower-case letters, digits, - or _, e.g. perso", name)
	}
	c.Spheres[name] = s
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, b, 0o644)
}

// Expand replaces a leading ~ with the home directory.
func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

// StateDir is ~/.local/state/due, or DUE_STATE.
func StateDir() string {
	if env := os.Getenv("DUE_STATE"); env != "" {
		return Expand(env)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "due")
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// SphereOfID finds the sphere whose prefix starts an entry id (PE-0001 → perso).
func (c *Config) SphereOfID(id string) (string, bool) {
	id = strings.ToUpper(strings.TrimSpace(id))
	for _, name := range c.Names() {
		if strings.HasPrefix(id, c.Spheres[name].Prefix+"E-") {
			return name, true
		}
	}
	return "", false
}
