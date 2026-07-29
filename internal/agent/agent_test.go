package agent

import (
	"slices"
	"sort"
	"testing"
)

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

// A profile is a separate environment: the credential is the only thing it
// inherits from the machine. A regression here silently makes something common
// to every profile again.
func TestEveryAgentSharesOnlyItsCredential(t *testing.T) {
	want := map[string][]string{
		"claude":   {".credentials.json"},
		"codex":    {"auth.json"},
		"pi":       {"auth.json"},
		"opencode": nil, // its auth lives under XDG_DATA_HOME, which ap never redirects
	}
	for _, name := range Names() {
		a, _ := Lookup(name)
		var rels []string
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
