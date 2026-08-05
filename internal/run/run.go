// Package run builds the child environment and execs the agent.
package run

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// ConfigDir is the value a.ConfigEnv is set to: the profile itself, or the
// matching shim directory inside it for an agent that needs one.
func ConfigDir(a agent.Agent, dir string) string {
	for _, s := range a.Shims {
		if s.Env == a.ConfigEnv {
			return filepath.Join(dir, s.Rel)
		}
	}
	return dir
}

// shimEnv is every variable a's shims claim, mapped to its directory inside the
// profile. Empty for an agent with a private config variable.
func shimEnv(a agent.Agent, dir string) map[string]string {
	out := make(map[string]string, len(a.Shims))
	for _, s := range a.Shims {
		out[s.Env] = filepath.Join(dir, s.Rel)
	}
	return out
}

// Env returns base with the agent's config variable pointed at the profile. base
// is normally os.Environ().
//
// Exactly one variable is ever set. Three of the four agents have a private one.
// opencode does not — its config root is computed from XDG_CONFIG_HOME, which
// every child process also reads — so for opencode the variable is pointed at a
// shim directory that passes every other program's config straight through to
// the real one. See profile.Shim; without it this would redirect git, gh, npm and
// every language server into the profile.
//
// dir == "" sets no override at all: the shape `ap run <agent>:default` needs,
// since the agent's real config directory is wherever it already looks with no
// variable set. The caller passes that fact in explicitly — profile.Default
// resolves to the real config directory for every other purpose, so inferring
// "no override" from dir equaling it here would be one string comparison away
// from silently breaking the day that directory moves.
//
// XDG_STATE_HOME and XDG_CACHE_HOME are never redirected, and neither is HOME.
// XDG_DATA_HOME is, for opencode only and only through a shim, because that is
// where its sessions live and it has no private variable for them. See
// agent.Shim.
func Env(a agent.Agent, dir string, base []string) []string {
	overrides := map[string]string{}
	if dir != "" {
		overrides[a.ConfigEnv] = ConfigDir(a, dir)
		for k, v := range shimEnv(a, dir) {
			overrides[k] = v
		}
	}

	out := make([]string, 0, len(base)+len(overrides))
	stripped := map[string]bool{a.ConfigEnv: true}
	for _, s := range a.Shims {
		stripped[s.Env] = true
	}
	for _, e := range base {
		// a.ConfigEnv is always stripped here, not only when it is about to be
		// replaced by an override: for dir == "" there is no override, and an
		// inherited value must not survive regardless. Without this, `ap run
		// <agent>:default` run from inside another profile inherited that
		// profile's config variable and kept using it — Default's whole point
		// silently failing in exactly the case it exists for.
		if k, _, ok := strings.Cut(e, "="); ok && stripped[k] {
			continue
		}
		out = append(out, e)
	}
	// Sorted, so `ap env` output is stable between runs and diffable.
	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+overrides[k])
	}
	return out
}
