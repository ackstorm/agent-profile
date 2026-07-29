// Package run builds the child environment and execs the agent.
package run

import (
	"sort"
	"strings"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// Options are per-invocation switches.
type Options struct {
	// Pure asks an additive agent to ignore its global and project config, so
	// the profile is close to the only thing loaded. No effect on Replace-mode
	// agents, which replace their config root outright.
	Pure bool
}

// pureEnv lists the suppression variables per agent. Only opencode has any.
func pureEnv(a agent.Agent) map[string]string {
	if a.Mode != agent.Additive {
		return nil
	}
	if a.Name != "opencode" {
		return nil
	}
	return map[string]string{
		"OPENCODE_PURE":                    "1",
		"OPENCODE_DISABLE_PROJECT_CONFIG":  "1",
		"OPENCODE_DISABLE_DEFAULT_PLUGINS": "1",
	}
}

// Env returns base with the agent's config variable pointed at dir. base is
// normally os.Environ().
//
// Nothing generic is ever set. XDG_CONFIG_HOME in particular would redirect
// every child process the agent spawns — git, gh, npm, language servers — so we
// use each agent's own variable even when that means settling for additive
// behaviour (opencode).
func Env(a agent.Agent, dir string, base []string, opts Options) []string {
	overrides := map[string]string{a.ConfigEnv: dir}
	if opts.Pure {
		for k, v := range pureEnv(a) {
			overrides[k] = v
		}
	}

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
