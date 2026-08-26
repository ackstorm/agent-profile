package hydrate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ackstorm/agent-profile/pkg/schema"
)

// EnvFile is the profile-local file apply writes derived environment variables
// into, and the launcher exports at run time.
//
// It exists because §9 and §15.1 describe variables, not files: `model` becomes
// a base URL and a credential reference that the agent reads from its
// ENVIRONMENT, and there is no configuration key to merge them into. Apply and
// launch are separate invocations, so something between them has to hold the
// answer, and the profile is the only place that survives both — a manifest is
// an input and may be thrown away (§1.1).
//
// The consequence is stated rather than discovered, and it is the same class as
// §34's file-sourced secrets: a profile with a `model` block REQUIRES launching
// through ap. Invoking the agent's own CLI reads none of this.
const EnvFile = ".ap-env"

// ModelEnv derives §9's environment for one runtime, in the order §15.1
// requires: adapter-managed variables first, user-declared
// runtimes.<r>.environment last, so the user's entry WINS on a collision.
//
// Returns the variables and a NOTICE for every managed variable a user entry
// overrode. §15.1 requires the notice, and the reason is specific: without it
// the `model` block silently lies about what the agent will use, and the block
// is the first place anyone looks.
func ModelEnv(runtime string, p schema.Profile, bindings map[string]string) (env map[string]string, notices []string, err error) {
	env = map[string]string{}

	if p.Model != nil {
		managed, err := managedModelEnv(runtime, *p.Model, bindings)
		if err != nil {
			return nil, nil, err
		}
		env = managed
	}

	// User-declared environment is applied LAST and wins. It is the deliberate
	// escape hatch (§15.1), not an accident to be resolved in the adapter's
	// favour.
	if rt, ok := p.Runtimes[runtime]; ok {
		for _, k := range sortedKeys(rt.Environment) {
			if _, managed := env[k]; managed {
				notices = append(notices, fmt.Sprintf(
					"runtimes.%s.environment overrides managed variable %s", runtime, k))
			}
			env[k] = rt.Environment[k]
		}
	}
	sort.Strings(notices)
	return env, notices, nil
}

// managedModelEnv is the per-runtime mapping of §9 onto native variables.
//
// Absent `model` means the runtime's native defaults AND native credentials —
// the subscription case — so ModelEnv writes nothing at all. Absent
// `model.auth` means native credentials even when base_url or model are
// declared, which is why the auth branch is separate from the endpoint one.
func managedModelEnv(runtime string, m schema.Model, bindings map[string]string) (map[string]string, error) {
	env := map[string]string{}
	names, ok := modelVars[runtime]
	if !ok {
		return nil, fmt.Errorf("runtime %q has no model environment mapping", runtime)
	}
	if m.BaseURL != "" {
		env[names.baseURLEnv] = m.BaseURL
	}
	if m.Model != "" && names.modelEnv != "" {
		env[names.modelEnv] = m.Model
	}
	if m.Auth != nil {
		v, err := bindingVar(bindings, m.Auth.ValueFrom.Secret, m.Auth.ValueFrom.Variable)
		if err != nil {
			return nil, fmt.Errorf("model.auth: %w", err)
		}
		// The variable NAME is what lands here, wrapped in the runtime's own
		// expansion syntax where it has one. The launcher resolves it at run
		// time; nothing writes a value (§34).
		ref, ok := SecretRef(runtime, v)
		if !ok {
			// A runtime with no expansion syntax still reads a plain
			// environment variable, and the launcher can point one at another.
			ref = "$" + v
		}
		env[names.tokenEnv] = ref
	}
	return env, nil
}

// modelVars is the per-runtime variable table. Every value here is a variable
// NAME, never a credential — the fields are suffixed Env to say so, because
// `token: "OPENAI_API_KEY"` reads to a scanner (and to a reader) as a secret
// and is the opposite: it is the name of the thing that holds one.
//
// modelVars is the per-runtime variable table. Like every other table in this
// package it is measured, not derived: the names come from each agent's own
// documentation of what it reads, and smoke is what re-verifies them.
// as hardcoded credentials. They are the NAMES of the variables that hold one,
// which is the whole point: §34 forbids this package from ever holding a value,
// and every field here is suffixed Env to say so.
//
//nolint:gosec // G101 matches the strings ANTHROPIC_AUTH_TOKEN and OPENAI_API_KEY
var modelVars = map[string]struct{ baseURLEnv, tokenEnv, modelEnv string }{
	"claude":   {baseURLEnv: "ANTHROPIC_BASE_URL", tokenEnv: "ANTHROPIC_AUTH_TOKEN", modelEnv: "ANTHROPIC_MODEL"},
	"codex":    {baseURLEnv: "OPENAI_BASE_URL", tokenEnv: "OPENAI_API_KEY"},
	"opencode": {baseURLEnv: "OPENCODE_BASE_URL", tokenEnv: "OPENCODE_API_KEY"},
	"pi":       {baseURLEnv: "PI_BASE_URL", tokenEnv: "PI_API_KEY"},
}

// FormatEnvFile renders the env file. One KEY=VALUE per line, sorted, so a
// re-apply is byte-identical and a diff shows only what changed.
//
// Deliberately not a shell script: it is read by ap, never sourced by a shell,
// so a value holding a space or a quote needs no escaping ceremony and cannot
// smuggle in a command.
func FormatEnvFile(env map[string]string) []byte {
	var b strings.Builder
	b.WriteString("# written by ap; read by ap run. Not a shell script.\n")
	for _, k := range sortedKeys(env) {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(env[k])
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// ParseEnvFile is FormatEnvFile's inverse, used by the launcher. An unparseable
// line is skipped rather than fatal: this file sits in a profile the user can
// open, and a stray line should not stop them launching an agent.
func ParseEnvFile(body []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || k == "" {
			continue
		}
		out[k] = v
	}
	return out
}
