package hydrate

import (
	"errors"
	"fmt"

	"github.com/ackstorm/agent-profile/pkg/schema"
)

// ModelTarget is where one runtime's model configuration lands: the file
// relative to the root, and the dotted path of the block inside it.
//
// Every row was measured against the real binary. Nothing here is a launcher
// concern: an earlier draft carried model through a profile-local env file that
// only `ap run` read, which meant a pod whose init container hydrated and whose
// main container exec'd the runtime directly lost the whole model block in
// silence. Files are read by whoever starts the agent, which is the point.
type ModelTarget struct {
	File  string
	Block string
}

// ModelConfig renders §9's model block into one runtime's native shape, and
// returns the file it belongs in.
//
// ok=false means this runtime has no model destination, which is a §8
// degradation: warn and skip, naming the runtime. pi is that case — its
// mechanism is not measured, and CLAUDE.md's rule is that a guessed path is
// worse than none.
//
// The token is NEVER written. Every runtime here references the binding by
// NAME, natively: codex has env_key, opencode has {env:...}, and claude reads
// its token from the environment the way it always did. Presence is checked at
// resolution; a variable missing at run time is the runtime's own failure.
func ModelConfig(runtime string, m schema.Model, bindings map[string]string) (ModelTarget, map[string]any, error) {
	switch runtime {
	case "claude":
		return claudeModel(m)
	case "codex":
		return codexModel(m, bindings)
	case "opencode":
		return opencodeModel(m, bindings)
	}
	return ModelTarget{}, nil, errNoModelTarget
}

// errNoModelTarget marks a runtime with no measured model destination. The
// caller turns it into a §8 warning rather than a failure: a profile that
// targets four runtimes should still apply to the three that can express it.
var errNoModelTarget = errors.New("no model destination")

// claudeModel writes into settings.json's `env` map. The values are literals
// and non-secret — base URL and model name — because claude reads its token
// from the environment as it always has, and §34 keeps values out of files.
//
// NOT YET SMOKE-VERIFIED: the registry discipline in CLAUDE.md says a row is
// proven by running the binary and watching it read the file. This one is
// carried from an inspection of the native binary's strings. Confirm the env
// map is actually loaded before treating it as settled.
func claudeModel(m schema.Model) (ModelTarget, map[string]any, error) {
	env := map[string]any{}
	if m.BaseURL != "" {
		env["ANTHROPIC_BASE_URL"] = m.BaseURL
	}
	if m.Model != "" {
		env["ANTHROPIC_MODEL"] = m.Model
	}
	if len(env) == 0 {
		return ModelTarget{}, nil, nil
	}
	return ModelTarget{File: "settings.json", Block: "env"}, env, nil
}

// codexModel writes a model_providers entry. env_key names the environment
// variable holding the credential — reference, not value, and native: it
// appears throughout codex's own binary, so this is codex's own mechanism
// rather than something layered on top.
func codexModel(m schema.Model, bindings map[string]string) (ModelTarget, map[string]any, error) {
	if m.BaseURL == "" && m.Auth == nil {
		return ModelTarget{}, nil, nil
	}
	entry := map[string]any{}
	if m.BaseURL != "" {
		entry["base_url"] = m.BaseURL
	}
	if m.Auth != nil {
		v, err := bindingVar(bindings, m.Auth.ValueFrom.Secret, m.Auth.ValueFrom.Variable)
		if err != nil {
			return ModelTarget{}, nil, fmt.Errorf("model.auth: %w", err)
		}
		entry["env_key"] = v
	}
	id := m.Type
	if id == "" {
		id = "default"
	}
	return ModelTarget{File: "config.toml", Block: "model_providers"},
		map[string]any{id: entry}, nil
}

// opencodeModel writes a provider block. apiKey carries opencode's own
// {env:VAR} expansion, which §34's table already names — so the binding travels
// as a reference the runtime itself resolves.
func opencodeModel(m schema.Model, bindings map[string]string) (ModelTarget, map[string]any, error) {
	if m.BaseURL == "" && m.Auth == nil {
		return ModelTarget{}, nil, nil
	}
	entry := map[string]any{}
	if m.BaseURL != "" {
		entry["baseURL"] = m.BaseURL
	}
	if m.Auth != nil {
		v, err := bindingVar(bindings, m.Auth.ValueFrom.Secret, m.Auth.ValueFrom.Variable)
		if err != nil {
			return ModelTarget{}, nil, fmt.Errorf("model.auth: %w", err)
		}
		ref, ok := SecretRef("opencode", v)
		if !ok {
			return ModelTarget{}, nil, ErrNoSecretSyntax{Runtime: "opencode", Resource: "model", Binding: v}
		}
		entry["apiKey"] = ref
	}
	id := m.Type
	if id == "" {
		id = "default"
	}
	return ModelTarget{File: "opencode.json", Block: "provider"},
		map[string]any{id: entry}, nil
}

// RuntimeEnvTarget is where §15's runtime environment lands, when the runtime
// has a native, profile-scoped mechanism for it — which §15 requires before an
// adapter materializes one at all.
//
// Only claude has a measured one: settings.json's `env` map, the same block
// its model configuration goes into. That is why §15.1's precedence lives here
// rather than in a launcher: both halves write the same keys in the same file,
// so "applied last wins" is a property of one merge and not of two mechanisms
// that might disagree.
func RuntimeEnvTarget(runtime string) (ModelTarget, bool) {
	if runtime == "claude" {
		return ModelTarget{File: "settings.json", Block: "env"}, true
	}
	return ModelTarget{}, false
}

// MergeRuntimeEnv applies §15.1: adapter-managed variables first, the user's
// runtimes.<r>.environment last, so the user WINS on a collision. It returns a
// notice per collision.
//
// The notice is required, and the reason is specific: without it the `model`
// block silently lies about what the agent will use, and the block is the first
// place anyone looks. Only a COLLIDING variable is noticed — noticing every
// user variable would make the signal worthless.
func MergeRuntimeEnv(managed map[string]any, userEnv map[string]string) (map[string]any, []string) {
	out := map[string]any{}
	for k, v := range managed {
		out[k] = v
	}
	var notices []string
	for _, k := range sortedKeys(userEnv) {
		if _, collides := out[k]; collides {
			notices = append(notices, fmt.Sprintf(
				"runtimes.<runtime>.environment overrides managed variable %s", k))
		}
		out[k] = userEnv[k]
	}
	return out, notices
}
