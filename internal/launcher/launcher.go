// Package launcher installs due's own launch agent, which runs `due tick`
// every minute and at login.
package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aclemen1/due-cli/internal/config"
)

const Label = "aero.clement.due"

func PlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
}

func LogPath() string { return filepath.Join(config.StateDir(), "launcher.log") }

func domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

// Plist is the launch agent for the binary at exe.
func Plist(exe, cfgPath string) string {
	home, _ := os.UserHomeDir()
	path := strings.Join([]string{
		filepath.Join(home, "go", "bin"), filepath.Join(home, ".local", "bin"),
		"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin",
	}, ":")
	args := []string{exe, "tick", "--format", "text"}
	if cfgPath != "" {
		args = append(args, "--config", cfgPath)
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>
	<key>ProgramArguments</key>
	<array>
`)
	for _, a := range args {
		b.WriteString("\t\t<string>" + xmlEscape(a) + "</string>\n")
	}
	b.WriteString(`	</array>
	<key>StartInterval</key>
	<integer>60</integer>
	<key>RunAtLoad</key>
	<true/>
	<key>ProcessType</key>
	<string>Background</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>` + xmlEscape(path) + `</string>
		<key>HOME</key>
		<string>` + xmlEscape(home) + `</string>
	</dict>
	<key>StandardOutPath</key>
	<string>` + xmlEscape(LogPath()) + `</string>
	<key>StandardErrorPath</key>
	<string>` + xmlEscape(LogPath()) + `</string>
</dict>
</plist>
`)
	return b.String()
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// Install writes the plist and loads it, replacing a loaded one.
func Install(exe, cfgPath string) error {
	if err := os.MkdirAll(filepath.Dir(PlistPath()), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(PlistPath(), []byte(Plist(exe, cfgPath)), 0o644); err != nil {
		return err
	}
	_ = exec.Command("launchctl", "bootout", domain()+"/"+Label).Run()
	if out, err := exec.Command("launchctl", "bootstrap", domain(), PlistPath()).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Uninstall unloads the agent and removes its plist.
func Uninstall() error {
	_ = exec.Command("launchctl", "bootout", domain()+"/"+Label).Run()
	if err := os.Remove(PlistPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

type Status struct {
	Installed bool   `json:"installed"`
	Loaded    bool   `json:"loaded"`
	Plist     string `json:"plist"`
	Program   string `json:"program,omitempty"`
	LastExit  string `json:"last_exit,omitempty"`
	Log       string `json:"log"`
}

func Read() Status {
	s := Status{Plist: PlistPath(), Log: LogPath()}
	if _, err := os.Stat(s.Plist); err == nil {
		s.Installed = true
	}
	out, err := exec.Command("launchctl", "print", domain()+"/"+Label).CombinedOutput()
	if err == nil {
		s.Loaded = true
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if v, ok := strings.CutPrefix(line, "program = "); ok {
				s.Program = v
			}
			if v, ok := strings.CutPrefix(line, "last exit code = "); ok {
				s.LastExit = v
			}
		}
	}
	return s
}
