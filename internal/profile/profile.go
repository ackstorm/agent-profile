// Package profile resolves and manages per-agent profile directories.
package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// Root is where all profiles live.
func Root() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "agent-profile", "profiles")
	}
	h, err := os.UserHomeDir()
	if err != nil {
		h = "."
	}
	return filepath.Join(h, ".local", "share", "agent-profile", "profiles")
}

// Dir is the directory for one agent+profile pair.
func Dir(a agent.Agent, name string) string {
	return filepath.Join(Root(), a.Name, name)
}

// Exists reports whether the profile directory is present.
func Exists(a agent.Agent, name string) bool {
	_, err := os.Stat(Dir(a, name))
	return err == nil
}

// ValidName rejects anything that could escape Root or collide with dotfiles.
//
// Exported because every caller that turns user input into a path under Root
// must run it — not just ParseRef. `ap create --from <name>` skipped it once,
// and `--from ../../../.claude` then copied files out of the real home.
func ValidName(s string) error {
	if s == "" {
		return fmt.Errorf("profile name must not be empty")
	}
	if strings.HasPrefix(s, ".") {
		return fmt.Errorf("invalid profile name %q: must not start with '.'", s)
	}
	if s != filepath.Base(s) || strings.ContainsAny(s, `/\`) {
		return fmt.Errorf("invalid profile name %q: must not contain a path separator", s)
	}
	for _, r := range s {
		ok := r == '-' || r == '_' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("invalid profile name %q: use letters, digits, '-' and '_'", s)
		}
	}
	return nil
}

// ParseRef splits an "<agent>:<profile>" reference.
func ParseRef(ref string) (agent.Agent, string, error) {
	parts := strings.Split(ref, ":")
	if len(parts) != 2 {
		return agent.Agent{}, "", fmt.Errorf("bad reference %q: want <agent>:<profile>, e.g. claude:plan", ref)
	}
	a, ok := agent.Lookup(parts[0])
	if !ok {
		return agent.Agent{}, "", fmt.Errorf("unknown agent %q: supported are %s", parts[0], strings.Join(agent.Names(), ", "))
	}
	if err := ValidName(parts[1]); err != nil {
		return agent.Agent{}, "", err
	}
	return a, parts[1], nil
}

// Create makes the profile directory. It refuses to clobber an existing one.
func Create(a agent.Agent, name string) (string, error) {
	dir := Dir(a, name)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("profile %s:%s already exists at %s", a.Name, name, dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// List returns the profile names for an agent. A missing directory is empty,
// not an error.
func List(a agent.Agent) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(Root(), a.Name))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		// e.IsDir() is dirent-based and false for a symlink, which would hide a
		// symlinked profile that Exists, which, and run all accept. Stat instead.
		fi, err := os.Stat(filepath.Join(Root(), a.Name, e.Name()))
		if err != nil {
			continue // dangling link or vanished mid-scan
		}
		if fi.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}
