package agent

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestEveryAgentKnowsItsRealConfigDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	h, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	want := map[string]string{
		"claude":   filepath.Join(h, ".claude"),
		"codex":    filepath.Join(h, ".codex"),
		"pi":       filepath.Join(h, ".pi", "agent"),
		"opencode": filepath.Join(h, ".config", "opencode"),
	}
	for _, name := range Names() {
		a, _ := Lookup(name)
		if a.Config != want[name] {
			t.Errorf("%s Config = %q, want %q", name, a.Config, want[name])
		}
	}
}

// opencode reads XDG_CONFIG_HOME when set (see ConfigBase), so its Config must
// too, or ap create --from default and ap run opencode:default would disagree
// with where opencode actually looks — verified as a three-way split: Config
// hardcoded ~/.config/opencode, profile.ConfigBase() already honoured
// XDG_CONFIG_HOME, and scripts/smoke.sh independently did too.
func TestOpencodeConfigHonoursXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/xdg")
	a, _ := Lookup("opencode")
	want := filepath.Join("/custom/xdg", "opencode")
	if a.Config != want {
		t.Errorf("opencode Config = %q, want %q", a.Config, want)
	}
}

// Config must agree with what Shared already claims about the real home, or the two
// would describe different machines. For opencode, credentials live under dataBase()
// rather than Config.
func TestConfigAgreesWithSharedSources(t *testing.T) {
	for _, name := range Names() {
		a, _ := Lookup(name)
		for _, s := range a.Shared {
			if strings.HasPrefix(s.From, a.Config+string(os.PathSeparator)) {
				continue
			}
			if strings.HasPrefix(s.From, filepath.Join(dataBase(), name)+string(os.PathSeparator)) {
				continue
			}
			t.Errorf("%s: shared %q lives at %q, outside Config %q and data base", name, s.Rel, s.From, a.Config)
		}
	}
}

func TestEveryAgentDeclaresWhatConfigMeans(t *testing.T) {
	for _, name := range Names() {
		a, _ := Lookup(name)
		if len(a.CloneAllow) == 0 {
			t.Errorf("%s declares no CloneAllow: --from would copy nothing", name)
		}
	}
}

// The allowlist must not name the credential: Link recreates it, and copying it
// would put a real file where the symlink belongs.
func TestCloneAllowNeverNamesASharedPath(t *testing.T) {
	for _, name := range Names() {
		a, _ := Lookup(name)
		for _, s := range a.Shared {
			if slices.Contains(a.CloneAllow, s.Rel) {
				t.Errorf("%s: %q is both CloneAllow and Shared", name, s.Rel)
			}
		}
	}
}

func TestStateIsNeverAlsoShared(t *testing.T) {
	for _, name := range Names() {
		a, _ := Lookup(name)
		for _, st := range a.State {
			for _, s := range a.Shared {
				if s.Rel == st {
					t.Errorf("%s: %q is both State and Shared", name, st)
				}
			}
		}
	}
}

func TestHistoryIsRecordedAsState(t *testing.T) {
	want := map[string][]string{
		"claude": {"projects"},
		"codex":  {"sessions", "history.jsonl"},
		"pi":     {"sessions"},
	}
	for name, rels := range want {
		a, _ := Lookup(name)
		for _, rel := range rels {
			if !slices.Contains(a.State, rel) {
				t.Errorf("%s: %q is not in State, so --from would copy it", name, rel)
			}
		}
	}
}

func TestClaudeKnowsItsInstructionsFile(t *testing.T) {
	a, _ := Lookup("claude")
	if a.Instructions == nil || a.Instructions.Name != "CLAUDE.md" {
		t.Fatalf("claude Instructions = %+v, want CLAUDE.md", a.Instructions)
	}
}

// A copied instructions file must not also be a share: Link would create the symlink
// and the copy would overwrite it, so which one won would depend on call order.
func TestInstructionsAreNeverShared(t *testing.T) {
	for _, name := range Names() {
		a, _ := Lookup(name)
		if a.Instructions == nil {
			continue
		}
		for _, s := range a.Shared {
			if s.Rel == a.Instructions.Name {
				t.Errorf("%s: %q is both Instructions and Shared", name, s.Rel)
			}
		}
	}
}

func TestEveryAgentSaysHowToSetUpAProfile(t *testing.T) {
	for _, name := range Names() {
		a, _ := Lookup(name)
		if !strings.Contains(a.Setup, "%s") {
			t.Errorf("%s: Setup %q must carry one %%s for the profile reference", name, a.Setup)
		}
	}
}

func TestLookupKnownAgents(t *testing.T) {
	for _, name := range []string{"claude", "codex", "opencode", "pi"} {
		a, ok := Lookup(name)
		if !ok {
			t.Fatalf("Lookup(%q) = not found, want found", name)
		}
		if a.Bin == "" {
			t.Errorf("%s: Bin is empty", name)
		}
		if a.ConfigEnv == "" {
			t.Errorf("%s: ConfigEnv is empty", name)
		}
	}
}

func TestLookupUnknownAgent(t *testing.T) {
	if _, ok := Lookup("cursor"); ok {
		t.Fatal(`Lookup("cursor") = found, want not found`)
	}
}

// opencode is the only additive agent. The distinction drives the --pure flag
// and the warning shown by `ap create`.
// Every agent replaces its config root. opencode used to be additive, which was
// a limitation rather than a choice: it has no private config-dir variable, so it
// is isolated through a shim instead. See profile.Shim and the Shim field.
func TestModes(t *testing.T) {
	for _, name := range Names() {
		a, _ := Lookup(name)
		if a.Mode != Replace {
			t.Errorf("%s Mode = %v, want Replace", name, a.Mode)
		}
	}
}

// A shared variable may only be set through a shim; a private one must never
// have the indirection. Now per-shim, because an agent may declare more than one.
func TestOnlySharedConfigVarsAreShimmed(t *testing.T) {
	shared := map[string]bool{
		"XDG_CONFIG_HOME": true,
		"XDG_DATA_HOME":   true,
		"XDG_STATE_HOME":  true,
		"XDG_CACHE_HOME":  true,
		"HOME":            true,
	}
	for _, name := range Names() {
		a, _ := Lookup(name)
		if shared[a.ConfigEnv] && len(a.Shims) == 0 {
			t.Errorf("%s sets the shared variable %s with no shim: every process it spawns would be redirected into the profile",
				name, a.ConfigEnv)
		}
		if !shared[a.ConfigEnv] && len(a.Shims) > 0 {
			t.Errorf("%s has a private variable %s but also a shim: drop the indirection", name, a.ConfigEnv)
		}
		seen := map[string]bool{}
		for _, s := range a.Shims {
			if s.Env == "" || s.Rel == "" || s.Entry == "" || s.Fallback == "" {
				t.Errorf("%s has an incomplete shim spec %+v", name, s)
			}
			if !shared[s.Env] {
				t.Errorf("%s shims %s, which is not a shared variable: a private one needs no shim", name, s.Env)
			}
			if seen[s.Rel] {
				t.Errorf("%s has two shims at the same Rel %q: they would overwrite each other", name, s.Rel)
			}
			seen[s.Rel] = true
			if s.Base() == "" {
				t.Errorf("%s: shim %s cannot resolve its base directory", name, s.Env)
			}
		}
	}
}

// Every Replace-mode agent forks auth unless we link it back. opencode's
// credentials live under XDG_DATA_HOME, which it now shims, so it needs shares
// exactly like the others — it did not before, when data was global.
func TestSharedEntries(t *testing.T) {
	oc, _ := Lookup("opencode")
	want := map[string]bool{"auth.json": true, "account.json": true, "mcp-auth.json": true}
	got := map[string]bool{}
	for _, s := range oc.Shared {
		got[s.Rel] = true
		if !filepath.IsAbs(s.From) {
			t.Errorf("opencode share %s: From %q is not absolute", s.Rel, s.From)
		}
	}
	for rel := range want {
		if !got[rel] {
			t.Errorf("opencode does not share %s: profiles would start logged out", rel)
		}
	}
	for _, name := range []string{"claude", "codex", "pi"} {
		a, _ := Lookup(name)
		if len(a.Shared) == 0 {
			t.Errorf("%s has no Shared entries; sessions and auth would fork", name)
		}
	}
}

// A clone must not carry another profile's sessions. opencode keeps them in a
// sqlite db inside the profile now, so the db and its sidecars are State — a
// clone that copied them would let you resume, inside the clone, a conversation
// that used tools the clone does not have.
func TestOpencodeSessionStateIsNotCloned(t *testing.T) {
	oc, _ := Lookup("opencode")
	state := map[string]bool{}
	for _, p := range oc.State {
		state[p] = true
	}
	for _, want := range []string{"opencode.db", "opencode.db-wal", "opencode.db-shm", "snapshot", "log"} {
		if !state[want] {
			t.Errorf("opencode State is missing %q: ap create --from would copy it", want)
		}
	}
	allow := map[string]bool{}
	for _, p := range oc.CloneAllow {
		allow[p] = true
	}
	for p := range state {
		if allow[p] {
			t.Errorf("%q is both State and CloneAllow: the allowlist wins and the session is cloned anyway", p)
		}
	}
}

// A profile is a separate environment: the credential is the only thing it
// inherits from the machine. A regression here silently makes something common
// to every profile again.
func TestEveryAgentSharesOnlyItsCredential(t *testing.T) {
	want := map[string][]string{
		"claude":   {".credentials.json"},
		"codex":    {"auth.json"},
		"pi":       {"auth.json"},
		"opencode": {"account.json", "auth.json", "mcp-auth.json"},
	}
	for _, name := range Names() {
		a, _ := Lookup(name)
		rels := make([]string, 0, len(a.Shared))
		for _, s := range a.Shared {
			rels = append(rels, s.Rel)
		}
		sort.Strings(rels)
		if !slices.Equal(rels, want[name]) {
			t.Errorf("%s shares %v, want %v (the credential, nothing else)", name, rels, want[name])
		}
	}
}

func TestDroppedSharesAreRecordedAsUnshared(t *testing.T) {
	want := map[string][]string{
		"claude": {".claude.json", "CLAUDE.md", "plugins/cache", "projects"},
		"codex":  {"history.jsonl", "sessions"},
		"pi":     {"sessions"},
	}
	for name, rels := range want {
		a, _ := Lookup(name)
		for _, rel := range rels {
			if !slices.Contains(a.Unshared, rel) {
				t.Errorf("%s: %q was dropped from Shared but is not in Unshared: "+
					"existing profiles would keep the symlink and go on sharing it", name, rel)
			}
		}
	}
}

// Nothing may be in both lists: Link would create the symlink and then remove it.
func TestUnsharedAndSharedDoNotOverlap(t *testing.T) {
	for _, name := range Names() {
		a, _ := Lookup(name)
		for _, s := range a.Shared {
			if slices.Contains(a.Unshared, s.Rel) {
				t.Errorf("%s: %q is both Shared and Unshared", name, s.Rel)
			}
		}
	}
}

// Shared.From must be absolute: Link resolves nothing, it symlinks verbatim.
func TestSharedFromIsAbsolute(t *testing.T) {
	for _, name := range Names() {
		a, _ := Lookup(name)
		for _, s := range a.Shared {
			if len(s.From) == 0 || s.From[0] != '/' {
				t.Errorf("%s: Shared %q From = %q, want absolute path", name, s.Rel, s.From)
			}
		}
	}
}

// --only-settings slices exactly one file per agent. It is a separate field
// rather than CloneAllow[0] on purpose: deriving it from the slice would make
// reordering that list change behaviour, which nobody would see in review. This
// test is the other half of that — the field and the allowlist must agree.
func TestEveryAgentDeclaresItsSettingsFile(t *testing.T) {
	for _, n := range Names() {
		a, _ := Lookup(n)
		if a.Settings == "" {
			t.Errorf("%s declares no Settings file", n)
			continue
		}
		if !slices.Contains(a.CloneAllow, a.Settings) {
			t.Errorf("%s: Settings %q is not a CloneAllow entry, so --only-settings "+
				"could reach a path the unfiltered clone cannot", n, a.Settings)
		}
		if a.SettingsFormat != JSON && a.SettingsFormat != TOML {
			t.Errorf("%s: SettingsFormat %d is neither JSON nor TOML", n, a.SettingsFormat)
		}
	}
}

// The Shared/State backstop applies to --only-settings too. An agent whose
// settings file was also its credential would make the narrowed clone the one
// way to copy it.
func TestSettingsIsNeverASharedOrStatePath(t *testing.T) {
	for _, n := range Names() {
		a, _ := Lookup(n)
		for _, s := range a.Shared {
			if s.Rel == a.Settings {
				t.Errorf("%s: Settings %q is also Shared", n, a.Settings)
			}
		}
		if slices.Contains(a.State, a.Settings) {
			t.Errorf("%s: Settings %q is also State", n, a.Settings)
		}
	}
}

func TestNamesIsSorted(t *testing.T) {
	got := Names()
	want := []string{"claude", "codex", "opencode", "pi"}
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v", got, want)
		}
	}
}
