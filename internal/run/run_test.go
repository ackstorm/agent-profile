package run

import (
	"path/filepath"
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
	got := envMap(t, Env(a, "/p/plan", []string{"PATH=/usr/bin", "HOME=/home/x"}))
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
	env := Env(a, "/p/review", []string{"CODEX_HOME=/home/x/.codex", "PATH=/usr/bin"})
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

// The decision the whole design rests on, in the only form that survives
// shimming: whatever variable is set must point INSIDE the profile.
//
// This replaced a blanket "never touch XDG_CONFIG_HOME". That rule existed
// because a redirected XDG_CONFIG_HOME sends every child process the agent spawns
// — git, gh, npm, language servers — looking for their own config in the profile.
// opencode has no private variable (its config root is
// (XDG_CONFIG_HOME || ~/.config)/opencode and nothing else), so isolating it
// means setting that variable, and the harm is prevented instead by pointing it
// at a shim that passes every other program through. See profile.Shim.
//
// The invariant below is strictly stronger than the old one: it also forbids
// pointing a variable at some unrelated place outside the profile, which the old
// allowlist would have permitted.
func TestEnvOnlySetsPathsInsideTheProfile(t *testing.T) {
	const dir = "/p/x"
	for _, name := range agent.Names() {
		a, _ := agent.Lookup(name)
		for k, v := range envMap(t, Env(a, dir, nil)) {
			rel, err := filepath.Rel(dir, v)
			if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
				t.Errorf("%s: %s=%q points outside the profile %s", name, k, v, dir)
			}
		}
	}
}

// State and cache are never redirected, and neither is HOME. That is what keeps
// credentials, caches and the user's own home shared across every profile.
//
// Data is the one exception, and only for an agent that declares a shim for it:
// opencode keeps its sessions there with no private variable to point elsewhere.
// Any agent WITHOUT such a shim must still see data untouched, which is what
// stops this exception from creeping outward.
func TestEnvNeverRedirectsStateOrCache(t *testing.T) {
	base := []string{
		"XDG_DATA_HOME=/home/x/.local/share",
		"XDG_STATE_HOME=/home/x/.local/state",
		"XDG_CACHE_HOME=/home/x/.cache",
		"HOME=/home/x",
	}
	for _, name := range agent.Names() {
		a, _ := agent.Lookup(name)
		shimmed := map[string]bool{}
		for _, s := range a.Shims {
			shimmed[s.Env] = true
		}
		got := envMap(t, Env(a, "/p/x", base))
		for _, want := range base {
			k, v, _ := strings.Cut(want, "=")
			if k == "XDG_STATE_HOME" || k == "XDG_CACHE_HOME" || k == "HOME" {
				if shimmed[k] {
					t.Errorf("%s shims %s: state, cache and HOME are never redirected", name, k)
				}
				if got[k] != v {
					t.Errorf("%s: %s = %q, want untouched %q", name, k, got[k], v)
				}
				continue
			}
			if !shimmed[k] && got[k] != v {
				t.Errorf("%s: %s = %q, want untouched %q (no shim declares it)", name, k, got[k], v)
			}
		}
	}
}

// opencode's sessions live under XDG_DATA_HOME in a sqlite db, and it has no
// private variable for them, so ap points that variable at a shim inside the
// profile. Without this the db is shared with every other profile and with the
// bare agent — measured: a session created inside `opencode:plan` landed in
// ~/.local/share/opencode/opencode.db.
func TestOpencodeRedirectsDataThroughAShim(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	got := envMap(t, Env(a, "/p/x", []string{"XDG_DATA_HOME=/real/share"}))
	if want := "/p/x/xdg-data"; got["XDG_DATA_HOME"] != want {
		t.Errorf("XDG_DATA_HOME = %q, want %q", got["XDG_DATA_HOME"], want)
	}
}

// `ap run codex:default` execs with no config variable set at all — nothing is
// created, nothing is linked, no shim is built. The empty dir is what cmd/ap
// passes here for Default, in place of a.Config, precisely so this holds.
// Moved here from internal/profile, which imported run.Env only to call it -
// this is where the behaviour actually lives.
func TestRunDefaultSetsNoConfigVariable(t *testing.T) {
	a, _ := agent.Lookup("claude")
	env := Env(a, "", nil)
	for _, e := range env {
		if strings.HasPrefix(e, a.ConfigEnv+"=") {
			t.Errorf("default must set no config variable, got %q", e)
		}
	}
}

// dir == "" (the Default shape) must not just decline to set an override - it
// must actively strip an inherited value for the agent's config variable.
// Without this, `CLAUDE_CONFIG_DIR=<parent profile> ap run claude:default`
// runs against the PARENT profile instead of the real config, because base
// (normally os.Environ()) still carries the inherited value straight through.
func TestEnvStripsInheritedConfigVarForDefault(t *testing.T) {
	a, _ := agent.Lookup("claude")
	got := envMap(t, Env(a, "", []string{"CLAUDE_CONFIG_DIR=/parent/profile", "PATH=/usr/bin"}))
	if v, ok := got["CLAUDE_CONFIG_DIR"]; ok {
		t.Errorf("default must strip an inherited config var, still got %q", v)
	}
	if got["PATH"] != "/usr/bin" {
		t.Error("unrelated base entries must survive")
	}
}

// Env sets one variable per declared shim, or one for agents with a private
// config variable. A second variable appearing for an agent without a shim
// should be a deliberate decision, not a side effect.
func TestEnvSetsOnlyTheConfigVars(t *testing.T) {
	for _, name := range agent.Names() {
		a, _ := agent.Lookup(name)
		got := Env(a, "/p/x", nil)
		wantLen := 1
		if len(a.Shims) > 0 {
			wantLen = len(a.Shims)
		}
		if len(got) != wantLen {
			t.Errorf("%s: Env with empty base = %v, want %d entries", name, got, wantLen)
		}
	}
}

// A shimmed agent's variable points at the shim subdirectory, not the profile
// root: the agent looks for its own name inside whatever it is given, and the
// profile root does not contain a directory called "opencode".
func TestEnvPointsAShimmedAgentAtTheShimDir(t *testing.T) {
	oc, _ := agent.Lookup("opencode")
	if len(oc.Shims) == 0 {
		t.Fatal("opencode has no shim spec; this test no longer describes it")
	}
	got := envMap(t, Env(oc, "/p/plan", nil))
	want := filepath.Join("/p/plan", oc.Shims[0].Rel)
	if got[oc.ConfigEnv] != want {
		t.Errorf("%s = %q, want %q", oc.ConfigEnv, got[oc.ConfigEnv], want)
	}
	if got[oc.ConfigEnv] == "/p/plan" {
		t.Error("shimmed agent was pointed at the profile root, so it would find no config of its own")
	}
}

// An agent with no shim gets the profile itself, unchanged.
func TestEnvPointsUnshimmedAgentsAtTheProfile(t *testing.T) {
	for _, name := range []string{"claude", "codex", "pi"} {
		a, _ := agent.Lookup(name)
		if got := envMap(t, Env(a, "/p/plan", nil)); got[a.ConfigEnv] != "/p/plan" {
			t.Errorf("%s: %s = %q, want /p/plan", name, a.ConfigEnv, got[a.ConfigEnv])
		}
	}
}

// `ap env` output must be stable between runs: the overrides come from a map, so
// without sorting the order changed on every invocation.
func TestEnvOutputIsSorted(t *testing.T) {
	oc, _ := agent.Lookup("opencode")
	first := Env(oc, "/p/x", nil)
	for range 8 {
		got := Env(oc, "/p/x", nil)
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

// Every declared shim contributes its own variable, each pointing at its own
// subdirectory of the profile. TestEnvOnlySetsPathsInsideTheProfile already
// guarantees they stay inside; this one guarantees they are all set at all.
func TestEnvSetsOneVariablePerShim(t *testing.T) {
	a := agent.Agent{Name: "x", ConfigEnv: "XDG_CONFIG_HOME", Shims: []agent.Shim{
		{Env: "XDG_CONFIG_HOME", Rel: "xdg", Entry: "x", Fallback: ".config"},
		{Env: "XDG_DATA_HOME", Rel: "xdg-data", Entry: "x", Fallback: ".local/share"},
	}}
	got := envMap(t, Env(a, "/p/x", []string{"XDG_DATA_HOME=/real/share"}))
	for k, want := range map[string]string{
		"XDG_CONFIG_HOME": "/p/x/xdg",
		"XDG_DATA_HOME":   "/p/x/xdg-data",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}
