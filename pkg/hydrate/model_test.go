package hydrate

import (
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/pkg/schema"
)

func specModel() schema.Model {
	return schema.Model{
		Type: "anthropic", BaseURL: "https://llm.company.com", Model: "claude-opus-5",
		Auth: &schema.Auth{Type: "bearer", ValueFrom: schema.ValueFrom{Secret: "llm-token"}},
	}
}

var modelBindings = map[string]string{"llm-token": "LITELLM_TOKEN"}

// Each runtime's own native mechanism, and none of them is a launcher concern.
// An earlier draft carried this through an env file only `ap run` read, which
// lost the whole block in the topology that needs it most: an init container
// hydrates, the main container execs the runtime directly.
func TestModelLandsInEachRuntimesOwnFile(t *testing.T) {
	for _, tc := range []struct {
		runtime, file, block string
		want                 map[string]any
	}{
		// claude: literals in settings.json's env map. Non-secret only — the
		// token is read from the environment, as it always was.
		{"claude", "settings.json", "env", map[string]any{
			"ANTHROPIC_BASE_URL": "https://llm.company.com",
			"ANTHROPIC_MODEL":    "claude-opus-5",
		}},
		// codex: env_key names the variable. Reference, not value, and native
		// — it is codex's own mechanism rather than something layered on top.
		{"codex", "config.toml", "model_providers", map[string]any{
			"anthropic": map[string]any{
				"base_url": "https://llm.company.com",
				"env_key":  "LITELLM_TOKEN",
			},
		}},
		// opencode: {env:VAR}, which §34's table already names.
		{"opencode", "opencode.json", "provider", map[string]any{
			"anthropic": map[string]any{
				"baseURL": "https://llm.company.com",
				"apiKey":  "{env:LITELLM_TOKEN}",
			},
		}},
	} {
		t.Run(tc.runtime, func(t *testing.T) {
			target, block, err := ModelConfig(tc.runtime, specModel(), modelBindings)
			if err != nil {
				t.Fatal(err)
			}
			if target.File != tc.file || target.Block != tc.block {
				t.Errorf("target = %+v, want %s/%s", target, tc.file, tc.block)
			}
			assertDeepEqual(t, block, tc.want)
		})
	}
}

// pi's mechanism is not measured. CLAUDE.md's rule is that a guessed path is
// worse than none — the flag silently writes nothing, or writes to a name the
// agent never opens — so this is a §8 degradation, not an invention.
func TestARuntimeWithNoMeasuredModelDestinationIsRefusedNotGuessed(t *testing.T) {
	if _, _, err := ModelConfig("pi", specModel(), modelBindings); err == nil {
		t.Error("pi produced a model destination; none has been measured")
	}
}

// §34 has no exception for model.auth: the token is never written anywhere.
func TestTheModelTokenIsNeverWritten(t *testing.T) {
	const value = "sk-do-not-materialize-me"
	t.Setenv("LITELLM_TOKEN", value)
	for _, runtime := range []string{"claude", "codex", "opencode"} {
		_, block, err := ModelConfig(runtime, specModel(), modelBindings)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump(block), value) {
			t.Errorf("%s: the value reached the configuration: %s", runtime, dump(block))
		}
		if !strings.Contains(dump(block), "LITELLM_TOKEN") && runtime != "claude" {
			t.Errorf("%s: the binding name was dropped: %s", runtime, dump(block))
		}
	}
}

// Absent model means native defaults AND native credentials — the subscription
// case. Writing anything would break it.
func TestAnAbsentModelWritesNothing(t *testing.T) {
	_, block, err := ModelConfig("claude", schema.Model{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(block) != 0 {
		t.Errorf("block = %+v, want nothing for a subscription profile", block)
	}
}

// Absent model.auth means native credentials even when base_url is declared,
// which is why the auth branch is separate from the endpoint one.
func TestAnAbsentModelAuthLeavesCredentialsNative(t *testing.T) {
	m := schema.Model{Type: "anthropic", BaseURL: "https://llm.company.com"}
	_, block, err := ModelConfig("codex", m, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry := block["anthropic"].(map[string]any)
	if _, set := entry["env_key"]; set {
		t.Errorf("a credential key was set with no model.auth: %+v", entry)
	}
	if entry["base_url"] == nil {
		t.Error("the endpoint was dropped along with the credential")
	}
}

// §15.1: the user's entry is applied LAST and WINS. It shares claude's
// destination with model, so the precedence is a property of one merge rather
// than of two mechanisms that might disagree about who ran last.
func TestUserEnvironmentWinsAndProducesANotice(t *testing.T) {
	managed := map[string]any{"ANTHROPIC_BASE_URL": "https://llm.company.com"}
	got, notices := MergeRuntimeEnv(managed, map[string]string{
		"ANTHROPIC_BASE_URL": "http://localhost:4000",
		"COMPANY_REGION":     "eu-west-1",
	})
	if got["ANTHROPIC_BASE_URL"] != "http://localhost:4000" {
		t.Errorf("the user's override lost: %v", got["ANTHROPIC_BASE_URL"])
	}
	if got["COMPANY_REGION"] != "eu-west-1" {
		t.Errorf("a non-colliding user variable was dropped: %+v", got)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "ANTHROPIC_BASE_URL") {
		t.Fatalf("notices = %v, want one naming the overridden variable", notices)
	}
	// Only the COLLIDING variable is noticed. Noticing every user variable
	// would make the signal worthless.
	if strings.Contains(strings.Join(notices, " "), "COMPANY_REGION") {
		t.Errorf("a non-colliding variable produced a notice: %v", notices)
	}
}

// Only claude has a measured native mechanism for §15's environment, and §15
// requires one before an adapter materializes it at all.
func TestOnlyClaudeHasAMeasuredRuntimeEnvironmentDestination(t *testing.T) {
	if _, ok := RuntimeEnvTarget("claude"); !ok {
		t.Error("claude has no runtime environment destination")
	}
	for _, r := range []string{"codex", "opencode", "pi"} {
		if _, ok := RuntimeEnvTarget(r); ok {
			t.Errorf("%s reports an unmeasured runtime environment destination", r)
		}
	}
}
