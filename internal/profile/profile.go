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

// Default names the agent's real, machine-wide configuration — the one it uses
// when ap is not involved. It is a sentinel, not a directory: nothing is ever
// created for it, Link never runs against it, no shim is built.
//
// It exists so `ap run codex:default` is a way to reach your normal setup
// through the same command as everything else, and so
// `ap create codex:plan --from default` can start a profile from the
// configuration you already have.
//
// Read-only, always. Dir resolves it to the real config directory, which is
// exactly why nothing that writes may accept it: `ap delete codex:default`
// would otherwise remove the configuration of the agent itself.
const Default = "default"

// Dir is the directory for one agent+profile pair, or, for Default, the
// agent's real config directory.
func Dir(a agent.Agent, name string) string {
	if name == Default {
		return a.Config
	}
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
	if s == Default {
		return fmt.Errorf("profile name %q is reserved for the agent's real config; "+
			"see ap run/which/env/--from, which accept it read-only", s)
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

// ParseRef splits an "<agent>:<profile>" reference. Rejects Default via
// ValidName, so every writing command that routes through it — create,
// delete — refuses the sentinel.
func ParseRef(ref string) (agent.Agent, string, error) {
	return parseRef(ref, false)
}

// ParseRefAllowDefault is ParseRef but additionally accepts Default for the
// profile name. Reserved for the four read-only paths that may resolve to the
// agent's real config directory: `run`, `which`, `env`, and the --from
// validation in `ap create`. Every writing path must keep using ParseRef.
func ParseRefAllowDefault(ref string) (agent.Agent, string, error) {
	return parseRef(ref, true)
}

func parseRef(ref string, allowDefault bool) (agent.Agent, string, error) {
	parts := strings.Split(ref, ":")
	if len(parts) != 2 {
		return agent.Agent{}, "", fmt.Errorf("bad reference %q: want <agent>:<profile>, e.g. claude:plan", ref)
	}
	a, ok := agent.Lookup(parts[0])
	if !ok {
		return agent.Agent{}, "", fmt.Errorf("unknown agent %q: supported are %s", parts[0], strings.Join(agent.Names(), ", "))
	}
	if allowDefault && parts[1] == Default {
		return a, Default, nil
	}
	if err := ValidName(parts[1]); err != nil {
		return agent.Agent{}, "", err
	}
	return a, parts[1], nil
}

// ParseVariantRef splits "<agent>:<profile>" or "<agent>:<profile>:<variant>",
// returning an empty variant for the two-segment form. Rejects Default via
// ValidName, so every writing command that routes through it — variant, delete,
// link, unlink — refuses the sentinel.
func ParseVariantRef(ref string) (agent.Agent, string, string, error) {
	return parseVariantRef(ref, false)
}

// ParseVariantRefAllowDefault is ParseVariantRef but additionally accepts
// Default as the profile of a two-segment reference, for the read-only paths
// that may resolve to the agent's real config directory: run, which, env.
func ParseVariantRefAllowDefault(ref string) (agent.Agent, string, string, error) {
	return parseVariantRef(ref, true)
}

// parseVariantRef splits at most one trailing ":<variant>" off a reference.
//
// Depth is exactly three, permanently. The name is a reference that gets
// parsed, so it has to be bounded; "for now" would make every later command
// guess how deep the thing it was handed goes.
//
// A variant over Default is refused whatever allowDefault says, which is why
// the head is parsed with allowDefault=false in that branch. Default is the
// agent's real config directory, read-only, and nothing is ever created for it
// — least of all a launch mode that only exists because ap wrote a file.
func parseVariantRef(ref string, allowDefault bool) (agent.Agent, string, string, error) {
	switch strings.Count(ref, ":") {
	case 1:
		a, name, err := parseRef(ref, allowDefault)
		return a, name, "", err
	case 2:
		i := strings.LastIndex(ref, ":")
		v := ref[i+1:]
		// The same ValidName as the profile segment, so "default" is refused here
		// too, and so the fuzz property covers both with one guard.
		if err := ValidName(v); err != nil {
			return agent.Agent{}, "", "", fmt.Errorf("variant: %w", err)
		}
		a, name, err := parseRef(ref[:i], false)
		return a, name, v, err
	default:
		return agent.Agent{}, "", "", fmt.Errorf(
			"bad reference %q: want <agent>:<profile> or <agent>:<profile>:<variant>, e.g. claude:review:opus", ref)
	}
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

// List returns the profile names for an agent, Default always first. A
// missing directory means no created profiles, not an error — Default is
// still there, since it names the real config rather than anything ap made.
func List(a agent.Agent) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(Root(), a.Name))
	if err != nil && !os.IsNotExist(err) {
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
	return append([]string{Default}, out...), nil
}
