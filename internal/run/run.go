// Package run builds the child environment and execs the agent.
package run

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// ConfigDir is the value a.ConfigEnv is set to: the profile itself, or the shim
// directory inside it for an agent that needs one.
func ConfigDir(a agent.Agent, dir string) string {
	if a.Shim != nil {
		return filepath.Join(dir, a.Shim.Rel)
	}
	return dir
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
// Nothing under XDG_DATA_HOME, XDG_STATE_HOME or XDG_CACHE_HOME is ever
// redirected, which is what keeps sessions, credentials and caches shared.
func Env(a agent.Agent, dir string, base []string) []string {
	overrides := map[string]string{a.ConfigEnv: ConfigDir(a, dir)}

	out := make([]string, 0, len(base)+len(overrides))
	for _, e := range base {
		if k, _, ok := strings.Cut(e, "="); ok {
			if _, shadowed := overrides[k]; shadowed {
				continue
			}
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
