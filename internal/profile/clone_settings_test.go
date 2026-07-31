//go:build unix

package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/internal/agent"
)

func onlySettingsAgent() agent.Agent {
	return agent.Agent{
		Name:           "test",
		CloneAllow:     []string{"settings.json", "CLAUDE.md", "skills"},
		Settings:       "settings.json",
		SettingsFormat: agent.JSON,
	}
}

// The point of the flag: every other CloneAllow entry is skipped. Asserted by
// their absence on disk, not by inspecting the filter — a filter can be right
// and the write still wrong.
func TestCloneSettingsWritesNothingButTheSlicedFile(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeTree(t, src, map[string]string{
		"settings.json":     `{"theme":"dark","permissions":{"allow":["Bash"]}}`,
		"CLAUDE.md":         "global instructions",
		"skills/a/SKILL.md": "a skill",
	})

	found, missing, err := CloneSettings(onlySettingsAgent(), src, dst, []string{"theme"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || len(missing) != 0 {
		t.Fatalf("found = %q, missing = %q, want just theme", found, missing)
	}
	got, err := os.ReadFile(filepath.Join(dst, "settings.json"))
	if err != nil {
		t.Fatalf("the settings file was not written: %v", err)
	}
	if !strings.Contains(string(got), "dark") || strings.Contains(string(got), "permissions") {
		t.Errorf("settings.json = %s, want only the named key", got)
	}
	entries, err := os.ReadDir(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the profile holds %d entries, want only settings.json: %v", len(entries), entries)
	}
}

// --only-settings is a narrowing of CloneAllow, so it can never reach a path the
// unfiltered clone would not have reached. The registry cannot express one
// today; this asserts the guard rather than the registry, because the guard is
// what has to hold for every future edit.
//
// Asks for a key that is NOT in evil.json on purpose. If it asked for one that
// is, removing escapesRoot would still produce an error — the write into dst
// goes through os.OpenRoot(dst).OpenFile, and os.Root independently refuses a
// ".." path, so that error would fire for a completely different reason and
// the test would stay green with the guard deleted. Asking for a missing key
// means the read-then-slice-then-nothing-found path returns (nil, missing,
// nil) — success — before the write is ever attempted, so escapesRoot removed
// is the only way this test's error disappears.
func TestCloneSettingsCannotReachOutsideTheProfile(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(filepath.Dir(src), "evil.json"), []byte(`{"x":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a := onlySettingsAgent()
	a.Settings = "../evil.json"

	if _, _, err := CloneSettings(a, src, dst, []string{"nonexistent"}); err == nil {
		t.Fatal("a Settings path above the profile was accepted")
	}
	if _, err := os.Stat(filepath.Join(dst, "evil.json")); !os.IsNotExist(err) {
		t.Error("something was written for a refused path")
	}
}

// The same backstop Clone has: the credential and session history are never
// copied, whatever the registry says.
func TestCloneSettingsRefusesASharedOrStatePath(t *testing.T) {
	for _, tt := range []struct {
		name string
		mut  func(*agent.Agent)
	}{
		{"shared", func(a *agent.Agent) {
			a.Shared = []agent.Share{{Rel: "settings.json", From: "/nowhere"}}
		}},
		{"state", func(a *agent.Agent) { a.State = []string{"settings.json"} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			src, dst := t.TempDir(), t.TempDir()
			writeTree(t, src, map[string]string{"settings.json": `{"theme":"dark"}`})
			a := onlySettingsAgent()
			tt.mut(&a)
			if _, _, err := CloneSettings(a, src, dst, []string{"theme"}); err == nil {
				t.Error("a Shared/State settings path was sliced")
			}
		})
	}
}

// A symlink at the source is the credential, or a link into the real home. Clone
// never copies one; here it is an error rather than a skip, because the settings
// file IS the whole command — silently producing nothing would be worse than
// saying so.
func TestCloneSettingsRefusesASymlinkedSource(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	real := filepath.Join(t.TempDir(), "real.json")
	if err := os.WriteFile(real, []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(src, "settings.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CloneSettings(onlySettingsAgent(), src, dst, []string{"theme"}); err == nil {
		t.Error("a symlinked settings file was followed")
	}
}

// Profiles and real config directories differ in what they happen to contain,
// the same tolerance Clone has. Every key is reported missing, and the caller
// warns.
func TestCloneSettingsToleratesAMissingSourceFile(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	found, missing, err := CloneSettings(onlySettingsAgent(), src, dst, []string{"theme"})
	if err != nil {
		t.Fatalf("a missing source file must not be an error: %v", err)
	}
	if len(found) != 0 || strings.Join(missing, " ") != "theme" {
		t.Errorf("found = %q, missing = %q, want every key missing", found, missing)
	}
}

// The destination is a profile being created, so the file cannot already exist.
// O_EXCL keeps this a copy rather than the first step towards a merge.
func TestCloneSettingsNeverOverwritesAnExistingFile(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeTree(t, src, map[string]string{"settings.json": `{"theme":"dark"}`})
	if err := os.WriteFile(filepath.Join(dst, "settings.json"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CloneSettings(onlySettingsAgent(), src, dst, []string{"theme"}); err == nil {
		t.Error("an existing settings file was overwritten")
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "settings.json")); string(b) != "mine" {
		t.Errorf("settings.json = %q, want it untouched", b)
	}
}

// A key given twice must not appear twice in the receipt, and a missing key
// given twice must not warn twice for the one typo.
func TestCloneSettingsDeduplicatesARepeatedKey(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeTree(t, src, map[string]string{"settings.json": `{"theme":"dark"}`})

	found, missing, err := CloneSettings(onlySettingsAgent(), src, dst, []string{"theme", "theme", "bogus", "bogus"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(found, " ") != "theme" {
		t.Errorf("found = %q, want theme once", found)
	}
	if strings.Join(missing, " ") != "bogus" {
		t.Errorf("missing = %q, want bogus once", missing)
	}
}

// containment is Clone's guard against a destination that lives inside the
// source. CloneSettings has no directory walk to run away with, but a
// destination nested inside the source would still let the sliced file land
// somewhere reachable back through the source, so the guard is reused rather
// than assumed to not apply.
func TestCloneSettingsRefusesADestinationInsideTheSource(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(src, "nested")
	if err := os.MkdirAll(dst, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTree(t, src, map[string]string{"settings.json": `{"theme":"dark"}`})

	if _, _, err := CloneSettings(onlySettingsAgent(), src, dst, []string{"theme"}); err == nil {
		t.Fatal("a destination inside the source was accepted")
	}
}
