package agent

import "testing"

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

// A shim is only ever acceptable for an agent whose config variable is shared
// with other programs. Setting a private variable through a shim would be a
// pointless indirection, and an agent with a SHARED variable and NO shim would
// redirect every process it spawns.
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
		switch {
		case shared[a.ConfigEnv] && a.Shim == nil:
			t.Errorf("%s sets the shared variable %s with no shim: every process it spawns would be redirected into the profile",
				name, a.ConfigEnv)
		case !shared[a.ConfigEnv] && a.Shim != nil:
			t.Errorf("%s has a private variable %s but also a shim: drop the indirection", name, a.ConfigEnv)
		}
		if a.Shim != nil && (a.Shim.Rel == "" || a.Shim.Entry == "") {
			t.Errorf("%s has an incomplete shim spec %+v", name, *a.Shim)
		}
	}
}

// A Replace-mode agent forks auth and history unless we link them back.
// An Additive one keeps its data outside the config root, so it needs nothing.
func TestSharedEntries(t *testing.T) {
	oc, _ := Lookup("opencode")
	if len(oc.Shared) != 0 {
		t.Errorf("opencode Shared = %v, want empty (data dir is already separate)", oc.Shared)
	}
	for _, name := range []string{"claude", "codex", "pi"} {
		a, _ := Lookup(name)
		if len(a.Shared) == 0 {
			t.Errorf("%s has no Shared entries; sessions and auth would fork", name)
		}
	}
}

// Each of these has a specific reason to be shared; a regression here silently
// costs the user their login, their history or their trust prompts.
func TestClaudeSharesTrustAndCache(t *testing.T) {
	a, _ := Lookup("claude")
	want := map[string]Kind{
		"projects":          Dir,  // session transcripts
		".credentials.json": File, // one login for every profile
		".claude.json":      File, // hasTrustDialogAccepted
		"CLAUDE.md":         File, // global instructions
		"plugins/cache":     Dir,  // content-addressed, no point duplicating
	}
	got := map[string]Kind{}
	for _, s := range a.Shared {
		got[s.Rel] = s.Kind
	}
	for rel, kind := range want {
		k, ok := got[rel]
		if !ok {
			t.Errorf("claude does not share %q", rel)
			continue
		}
		if k != kind {
			t.Errorf("claude %q Kind = %v, want %v", rel, k, kind)
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
