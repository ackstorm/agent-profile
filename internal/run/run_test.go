package run

import (
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/internal/agent"
)

func envMap(t *testing.T, env []string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			t.Fatalf("malformed env entry %q", e)
		}
		m[k] = v
	}
	return m
}

func TestEnvSetsConfigVar(t *testing.T) {
	a, _ := agent.Lookup("claude")
	got := envMap(t, Env(a, "/p/plan", []string{"PATH=/usr/bin", "HOME=/home/x"}, Options{}))
	if got["CLAUDE_CONFIG_DIR"] != "/p/plan" {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want /p/plan", got["CLAUDE_CONFIG_DIR"])
	}
	if got["PATH"] != "/usr/bin" || got["HOME"] != "/home/x" {
		t.Error("base environment was not passed through")
	}
}

// A pre-existing value for the same variable must be replaced, not duplicated:
// the user may already have exported it.
func TestEnvOverridesExistingValue(t *testing.T) {
	a, _ := agent.Lookup("codex")
	env := Env(a, "/p/review", []string{"CODEX_HOME=/home/x/.codex", "PATH=/usr/bin"}, Options{})
	if got := envMap(t, env); got["CODEX_HOME"] != "/p/review" {
		t.Errorf("CODEX_HOME = %q, want /p/review", got["CODEX_HOME"])
	}
	var n int
	for _, e := range env {
		if strings.HasPrefix(e, "CODEX_HOME=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("CODEX_HOME appears %d times, want 1", n)
	}
}

// The decision the whole design rests on: never touch XDG_CONFIG_HOME. It is a
// freedesktop-wide variable, and every child process the agent spawns (git, gh,
// npm, LSPs) would inherit a redirected value.
func TestEnvNeverTouchesGenericVars(t *testing.T) {
	generic := []string{
		"XDG_CONFIG_HOME=/home/x/.config",
		"XDG_DATA_HOME=/home/x/.local/share",
		"XDG_STATE_HOME=/home/x/.local/state",
		"XDG_CACHE_HOME=/home/x/.cache",
		"HOME=/home/x",
	}
	for _, name := range agent.Names() {
		a, _ := agent.Lookup(name)
		got := envMap(t, Env(a, "/p/x", generic, Options{Pure: true}))
		for _, want := range generic {
			k, v, _ := strings.Cut(want, "=")
			if got[k] != v {
				t.Errorf("%s: %s = %q, want untouched %q", name, k, got[k], v)
			}
		}
	}
}

// Each agent sets exactly one variable (plus opencode's suppressors under
// --pure). A new variable appearing here should be a deliberate decision.
func TestEnvSetsOnlyTheConfigVar(t *testing.T) {
	for _, name := range agent.Names() {
		a, _ := agent.Lookup(name)
		got := Env(a, "/p/x", nil, Options{})
		if len(got) != 1 {
			t.Errorf("%s: Env with empty base = %v, want exactly one entry", name, got)
		}
	}
}

// --pure only means something for the additive agent.
func TestPureSuppressesOpencodeGlobals(t *testing.T) {
	oc, _ := agent.Lookup("opencode")
	got := envMap(t, Env(oc, "/p/plan", nil, Options{Pure: true}))
	for _, k := range []string{"OPENCODE_PURE", "OPENCODE_DISABLE_PROJECT_CONFIG", "OPENCODE_DISABLE_DEFAULT_PLUGINS"} {
		if got[k] != "1" {
			t.Errorf("%s = %q, want 1", k, got[k])
		}
	}
	if got["OPENCODE_CONFIG_DIR"] != "/p/plan" {
		t.Errorf("OPENCODE_CONFIG_DIR = %q, want /p/plan", got["OPENCODE_CONFIG_DIR"])
	}
}

func TestPureIsANoOpForReplaceAgents(t *testing.T) {
	for _, name := range []string{"claude", "codex", "pi"} {
		a, _ := agent.Lookup(name)
		got := envMap(t, Env(a, "/p/plan", nil, Options{Pure: true}))
		if len(got) != 1 {
			t.Errorf("%s: --pure added variables: %v", name, got)
		}
		if _, leaked := got["OPENCODE_PURE"]; leaked {
			t.Errorf("%s: OPENCODE_PURE leaked", name)
		}
	}
}

func TestEnvWithoutPureLeavesOpencodeGlobalsAlone(t *testing.T) {
	oc, _ := agent.Lookup("opencode")
	if got := envMap(t, Env(oc, "/p/plan", nil, Options{})); len(got) != 1 {
		t.Errorf("Env without --pure = %v, want only the config var", got)
	}
}

// `ap env` output must be stable between runs: the overrides come from a map, so
// without sorting the order changed on every invocation.
func TestEnvOutputIsSorted(t *testing.T) {
	oc, _ := agent.Lookup("opencode")
	first := Env(oc, "/p/x", nil, Options{Pure: true})
	for range 8 {
		got := Env(oc, "/p/x", nil, Options{Pure: true})
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("Env is not deterministic: %v vs %v", got, first)
			}
		}
	}
	for i := 1; i < len(first); i++ {
		if first[i-1] > first[i] {
			t.Errorf("Env output is not sorted: %q before %q", first[i-1], first[i])
		}
	}
}
