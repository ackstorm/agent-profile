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
// returning an empty variant for the two-segment form.
//
// Depth is exactly three, permanently. The name is a reference that gets
// parsed, so it has to be bounded; "for now" would make every later command
// guess how deep the thing it was handed goes.
//
// agentreg.Default is ACCEPTED as the profile, with or without a variant, and
// what a bare "<agent>:default" means is then each command's own decision: a
// launch for run, which and env, a refusal naming the way forward for variant,
// delete, link and unlink. There used to be a strict sibling that rejected the
// sentinel on behalf of the writing commands, and it hid that decision rather
// than making it — three of those four still needed a guard of their own,
// because the sentinel is fine as the PARENT of a variant and never fine alone.
//
// A variant over the sentinel is a variant like any other. It used to be
// refused outright, on the grounds that nothing is ever created for `default` —
// which confused WHERE a variant lives with WHAT it names. A variant has no
// directory, no shim and no links: it is one file under VariantsRoot, a sibling
// of the profiles root, and running one still hands Exec no override at all.
// Nothing is written inside the real config directory, so `default` is exactly
// as read-only with a variant as it is without one.
func ParseVariantRef(ref string) (agentreg.Agent, string, string, error) {
	switch strings.Count(ref, ":") {
	case 1:
		a, name, err := parseRef(ref, true)
		return a, name, "", err
	case 2:
		i := strings.LastIndex(ref, ":")
		v := ref[i+1:]
		// agentreg.ValidName and not ValidNameAllowDefault: the variant segment
		// names a file ap writes, so "default" is refused here even though it is
		// permitted as the profile it hangs off.
		if err := agentreg.ValidName(v); err != nil {
			return agentreg.Agent{}, "", "", fmt.Errorf("variant: %w", err)
		}
		a, name, err := parseRef(ref[:i], true)
		return a, name, v, err
	default:
		return agentreg.Agent{}, "", "", fmt.Errorf(
			"bad reference %q: want <agent>:<profile> or <agent>:<profile>:<variant>, e.g. claude:review:opus", ref)
	}
}

// Create makes the profile directory. It refuses to clobber an existing one.
//
// The refusal names the way forward, the same shape WriteVariant uses. `ap
// create` against a profile that already exists is overwhelmingly someone
// reaching for its WRAPPER — a profile materialized by `ap manifest apply`
// before apply provisioned one, or made before create wrote one at all — and a
// bare "already exists" left them with nowhere to go. `ap link` is that
// somewhere; it writes the wrapper and touches nothing else.
func Create(a agentreg.Agent, name string) (string, error) {
	dir := Dir(a, name)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("profile %s:%s already exists at %s\n"+
			"to write its wrapper without touching the profile: ap link %s:%s",
			a.Name, name, dir, a.Name, name)
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
