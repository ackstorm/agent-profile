// Package profile resolves and manages per-agent profile directories.
package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ackstorm/agent-profile/pkg/agentreg"
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

// CacheDir is where fetched sources are cached. It sits beside the profiles
// rather than inside one: a source fetched for claude:plan is the same bytes
// when codex:review asks for it, and content addressed by SHA or digest cannot
// collide between them.
//
// It follows XDG_CACHE_HOME, not XDG_DATA_HOME. A cache is reconstructible by
// definition — deleting it costs a re-fetch and nothing else — which is
// exactly what that variable means, and it keeps a cache out of whatever the
// user backs up.
func CacheDir() (string, error) {
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		return filepath.Join(d, "agent-profile", "sources"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".cache", "agent-profile", "sources"), nil
}

// Dir is the directory for one agent+profile pair, or, for agentreg.Default,
// the agent's real config directory.
func Dir(a agentreg.Agent, name string) string {
	if name == agentreg.Default {
		return a.Config
	}
	return filepath.Join(Root(), a.Name, name)
}

// Exists reports whether the profile directory is present.
func Exists(a agentreg.Agent, name string) bool {
	_, err := os.Stat(Dir(a, name))
	return err == nil
}

// ParseRef splits an "<agent>:<profile>" reference. Rejects agentreg.Default
// via agentreg.ValidName, so every writing command that routes through it —
// create, delete — refuses the sentinel.
func ParseRef(ref string) (agentreg.Agent, string, error) {
	return parseRef(ref, false)
}

// ParseRefAllowDefault is ParseRef but additionally accepts agentreg.Default
// for the profile name. Reserved for the four read-only paths that may
// resolve to the agent's real config directory: `run`, `which`, `env`, and the
// --from validation in `ap create`. Every writing path must keep using
// ParseRef.
func ParseRefAllowDefault(ref string) (agentreg.Agent, string, error) {
	return parseRef(ref, true)
}

func parseRef(ref string, allowDefault bool) (agentreg.Agent, string, error) {
	parts := strings.Split(ref, ":")
	if len(parts) != 2 {
		return agentreg.Agent{}, "", fmt.Errorf("bad reference %q: want <agent>:<profile>, e.g. claude:plan", ref)
	}
	a, ok := agentreg.Lookup(parts[0])
	if !ok {
		return agentreg.Agent{}, "", fmt.Errorf("unknown agent %q: supported are %s", parts[0], strings.Join(agentreg.Names(), ", "))
	}
	if allowDefault && parts[1] == agentreg.Default {
		return a, agentreg.Default, nil
	}
	if err := agentreg.ValidName(parts[1]); err != nil {
		return agentreg.Agent{}, "", err
	}
	return a, parts[1], nil
}

// ParseVariantRef splits "<agent>:<profile>" or "<agent>:<profile>:<variant>",
// returning an empty variant for the two-segment form. Rejects agentreg.Default
// via agentreg.ValidName, so every writing command that routes through it —
// variant, delete, link, unlink — refuses the sentinel.
func ParseVariantRef(ref string) (agentreg.Agent, string, string, error) {
	return parseVariantRef(ref, false)
}

// ParseVariantRefAllowDefault is ParseVariantRef but additionally accepts
// agentreg.Default as the profile of a two-segment reference, for the
// read-only paths that may resolve to the agent's real config directory: run,
// which, env.
func ParseVariantRefAllowDefault(ref string) (agentreg.Agent, string, string, error) {
	return parseVariantRef(ref, true)
}

// parseVariantRef splits at most one trailing ":<variant>" off a reference.
//
// Depth is exactly three, permanently. The name is a reference that gets
// parsed, so it has to be bounded; "for now" would make every later command
// guess how deep the thing it was handed goes.
//
// A variant over agentreg.Default is refused whatever allowDefault says, which
// is why the head is parsed with allowDefault=false in that branch.
// agentreg.Default is the agent's real config directory, read-only, and
// nothing is ever created for it — least of all a launch mode that only
// exists because ap wrote a file.
func parseVariantRef(ref string, allowDefault bool) (agentreg.Agent, string, string, error) {
	switch strings.Count(ref, ":") {
	case 1:
		a, name, err := parseRef(ref, allowDefault)
		return a, name, "", err
	case 2:
		i := strings.LastIndex(ref, ":")
		v := ref[i+1:]
		// The same agentreg.ValidName as the profile segment, so "default" is
		// refused here too, and so the fuzz property covers both with one guard.
		if err := agentreg.ValidName(v); err != nil {
			return agentreg.Agent{}, "", "", fmt.Errorf("variant: %w", err)
		}
		a, name, err := parseRef(ref[:i], false)
		return a, name, v, err
	default:
		return agentreg.Agent{}, "", "", fmt.Errorf(
			"bad reference %q: want <agent>:<profile> or <agent>:<profile>:<variant>, e.g. claude:review:opus", ref)
	}
}

// Create makes the profile directory. It refuses to clobber an existing one.
func Create(a agentreg.Agent, name string) (string, error) {
	dir := Dir(a, name)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("profile %s:%s already exists at %s", a.Name, name, dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// List returns the profile names for an agent, agentreg.Default always first.
// A missing directory means no created profiles, not an error —
// agentreg.Default is still there, since it names the real config rather than
// anything ap made.
func List(a agentreg.Agent) ([]string, error) {
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
	return append([]string{agentreg.Default}, out...), nil
}
