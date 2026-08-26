package hydrate

import (
	"slices"
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/pkg/schema"
)

func modelProfile(runtimeEnv map[string]string) schema.Profile {
	p := schema.Profile{
		Model: &schema.Model{
			Type: "anthropic", BaseURL: "https://llm.company.com", Model: "claude-opus-5",
			Auth: &schema.Auth{Type: "bearer", ValueFrom: schema.ValueFrom{Secret: "llm-token"}},
		},
	}
	if runtimeEnv != nil {
		p.Runtimes = map[string]schema.Runtime{"claude": {Environment: runtimeEnv}}
	}
	return p
}

func TestModelBecomesAdapterManagedEnvironment(t *testing.T) {
	env, notices, err := ModelEnv("claude", modelProfile(nil), map[string]string{"llm-token": "LITELLM_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_BASE_URL"] != "https://llm.company.com" {
		t.Errorf("base url = %q", env["ANTHROPIC_BASE_URL"])
	}
	if env["ANTHROPIC_MODEL"] != "claude-opus-5" {
		t.Errorf("model = %q", env["ANTHROPIC_MODEL"])
	}
	// A REFERENCE, never a value. §34 has no exception for model.auth.
	if env["ANTHROPIC_AUTH_TOKEN"] != "${LITELLM_TOKEN}" {
		t.Errorf("token = %q, want a reference to the binding variable", env["ANTHROPIC_AUTH_TOKEN"])
	}
	if len(notices) != 0 {
		t.Errorf("notices = %v, want none", notices)
	}
}

// §15.1: the user's entry is applied LAST and WINS. It is the deliberate escape
// hatch, and the notice is what stops the model block silently lying about what
// the agent will use — the block being the first place anyone looks.
func TestUserEnvironmentWinsAndProducesANotice(t *testing.T) {
	env, notices, err := ModelEnv("claude",
		modelProfile(map[string]string{"ANTHROPIC_BASE_URL": "http://localhost:4000", "COMPANY_REGION": "eu-west-1"}),
		map[string]string{"llm-token": "LITELLM_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_BASE_URL"] != "http://localhost:4000" {
		t.Errorf("the user's override lost: %q", env["ANTHROPIC_BASE_URL"])
	}
	if env["COMPANY_REGION"] != "eu-west-1" {
		t.Errorf("a non-colliding user variable was dropped: %+v", env)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "ANTHROPIC_BASE_URL") {
		t.Fatalf("notices = %v, want one naming the overridden variable", notices)
	}
	// Only the COLLIDING variable produces a notice. Noticing every user
	// variable would make the signal worthless.
	if strings.Contains(strings.Join(notices, " "), "COMPANY_REGION") {
		t.Errorf("a non-colliding variable produced a notice: %v", notices)
	}
}

// Absent model means the runtime's native defaults AND native credentials —
// the subscription case. Writing anything would break it.
func TestAnAbsentModelWritesNothing(t *testing.T) {
	env, notices, err := ModelEnv("claude", schema.Profile{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 0 || len(notices) != 0 {
		t.Errorf("env = %+v, notices = %v; a subscription profile must get neither", env, notices)
	}
}

// Absent model.auth means native credentials even when base_url is declared,
// which is why the auth branch is separate from the endpoint one.
func TestAnAbsentModelAuthLeavesCredentialsNative(t *testing.T) {
	p := schema.Profile{Model: &schema.Model{BaseURL: "https://llm.company.com"}}
	env, _, err := ModelEnv("claude", p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, set := env["ANTHROPIC_AUTH_TOKEN"]; set {
		t.Errorf("a token variable was set with no model.auth: %+v", env)
	}
	if env["ANTHROPIC_BASE_URL"] == "" {
		t.Error("the endpoint was dropped along with the credential")
	}
}

// The env file is read by ap, never sourced by a shell, so a value holding a
// space or a quote needs no escaping and cannot smuggle in a command.
func TestEnvFileRoundTripsAwkwardValues(t *testing.T) {
	want := map[string]string{
		"A": "plain",
		"B": "with spaces and \"quotes\"",
		"C": "$(rm -rf /) && echo pwned",
		"D": "",
	}
	got := ParseEnvFile(FormatEnvFile(want))
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	// Sorted and stable, so a re-apply is byte-identical.
	first := string(FormatEnvFile(want))
	for range 3 {
		if string(FormatEnvFile(want)) != first {
			t.Fatal("FormatEnvFile is not deterministic")
		}
	}
	lines := strings.Split(strings.TrimSpace(first), "\n")
	if !slices.IsSorted(lines[1:]) {
		t.Errorf("env file is not sorted: %v", lines)
	}
}
