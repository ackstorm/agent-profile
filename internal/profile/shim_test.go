//go:build unix

package profile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// fakeConfigBase points ConfigBase at a temporary directory containing the given
// entries, and returns its path.
func fakeConfigBase(t *testing.T, entries ...string) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	for _, e := range entries {
		if err := os.MkdirAll(filepath.Join(base, e), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

// THE canary for the shim. It links to every entry of the user's real config
// directory, which makes a Delete that followed those links the most destructive
// thing this program could do — worse than the session-history case, because
// ~/.config holds the configuration of every application on the machine.
//
// os.RemoveAll lstats each entry and unlinks symlinks rather than descending.
// This test is what keeps that true. Do not delete or weaken it.
func TestDeleteDoesNotFollowTheConfigShim(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	base := fakeConfigBase(t, "git", "gh", "nvim")
	canary := filepath.Join(base, "git", "config")
	if err := os.WriteFile(canary, []byte("[user]\n\tname = do not lose me\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := agentOrFail(t, "opencode")
	dir, err := Create(a, "canary")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Shim(a, dir); err != nil {
		t.Fatal(err)
	}
	shimDir := filepath.Join(dir, a.Shims[0].Rel)
	// Prove the link is really there, or the test proves nothing.
	if _, err := os.Stat(filepath.Join(shimDir, "git", "config")); err != nil {
		t.Fatalf("shim does not reach the real config, so this test is vacuous: %v", err)
	}

	if err := Delete(a, "canary"); err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{"git", "gh", "nvim"} {
		if _, err := os.Stat(filepath.Join(base, e)); err != nil {
			t.Errorf("Delete destroyed the real config entry %s: %v", e, err)
		}
	}
	if b, err := os.ReadFile(canary); err != nil {
		t.Fatalf("Delete followed the shim into the real config: %v", err)
	} else if len(b) == 0 {
		t.Fatal("canary emptied")
	}
}

// The isolation and the passthrough are one property: the agent's own name must
// resolve to the profile, and every OTHER entry must resolve to the real one.
func TestShimIsolatesTheAgentAndPassesEverythingElseThrough(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	base := fakeConfigBase(t, "git", "gh", "opencode")
	a := agentOrFail(t, "opencode")
	dir, err := Create(a, "iso")
	if err != nil {
		t.Fatal(err)
	}
	foundReal, err := Shim(a, dir)
	if err != nil {
		t.Fatal(err)
	}
	shimDir := filepath.Join(dir, a.Shims[0].Rel)
	if len(foundReal) != 0 {
		t.Errorf("fresh shim reported real entries: %v", foundReal)
	}

	// The agent's own entry: the profile, NOT the real config.
	got, err := filepath.EvalSymlinks(filepath.Join(shimDir, a.Shims[0].Entry))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("%s resolves to %s, want the profile %s", a.Shims[0].Entry, got, want)
	}
	if real := filepath.Join(base, a.Shims[0].Entry); got == real {
		t.Errorf("%s resolves to the real config %s: no isolation at all", a.Shims[0].Entry, real)
	}

	// Everything else: the real config, so the agent's subprocesses still work.
	for _, e := range []string{"git", "gh"} {
		got, err := filepath.EvalSymlinks(filepath.Join(shimDir, e))
		if err != nil {
			t.Fatalf("%s not reachable through the shim: %v", e, err)
		}
		want, err := filepath.EvalSymlinks(filepath.Join(base, e))
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s resolves to %s, want the real %s", e, got, want)
		}
	}
}

// Every run re-asserts, because ~/.config gains entries over time: a profile
// created last month must not hide a tool installed yesterday.
func TestShimPicksUpNewEntriesOnEveryRun(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	base := fakeConfigBase(t, "git")
	a := agentOrFail(t, "opencode")
	dir, err := Create(a, "fresh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Shim(a, dir); err != nil {
		t.Fatal(err)
	}
	shimDir := filepath.Join(dir, a.Shims[0].Rel)
	if _, err := os.Lstat(filepath.Join(shimDir, "installedlater")); err == nil {
		t.Fatal("entry exists before it was created")
	}

	if err := os.MkdirAll(filepath.Join(base, "installedlater"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Shim(a, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(shimDir, "installedlater")); err != nil {
		t.Errorf("re-asserting the shim did not pick up a new config entry: %v", err)
	}

	// And an entry that disappeared leaves no dangling name behind.
	if err := os.RemoveAll(filepath.Join(base, "installedlater")); err != nil {
		t.Fatal(err)
	}
	if _, err := Shim(a, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(shimDir, "installedlater")); err == nil {
		t.Error("shim kept a link to a config entry that no longer exists")
	}
}

// A symlink whose target moved must be re-pointed, not left stale. This is why
// the links are replaced unconditionally rather than only when absent.
func TestShimRepointsAStaleLink(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	base := fakeConfigBase(t, "git")
	a := agentOrFail(t, "opencode")
	dir, err := Create(a, "stale")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Shim(a, dir); err != nil {
		t.Fatal(err)
	}
	shimDir := filepath.Join(dir, a.Shims[0].Rel)

	// Repoint it somewhere wrong, as a stale shim from an older layout would be.
	wrong := t.TempDir()
	if err := os.Remove(filepath.Join(shimDir, "git")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(wrong, filepath.Join(shimDir, "git")); err != nil {
		t.Fatal(err)
	}
	if _, err := Shim(a, dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(filepath.Join(shimDir, "git"))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(base, "git") {
		t.Errorf("stale link not re-pointed: got %s, want %s", got, filepath.Join(base, "git"))
	}
}

// The documented failure mode: a program run inside the agent writes a real
// config directory into the shim instead of following a passthrough link. It must
// be reported, never silently deleted — it is the user's data.
func TestShimReportsRealEntriesAndDoesNotDeleteThem(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	fakeConfigBase(t, "git")
	a := agentOrFail(t, "opencode")
	dir, err := Create(a, "realentry")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Shim(a, dir); err != nil {
		t.Fatal(err)
	}
	shimDir := filepath.Join(dir, a.Shims[0].Rel)

	// brandnewtool has no counterpart in the real config base, so it is only
	// reachable from inside this profile.
	newTool := filepath.Join(shimDir, "brandnewtool")
	if err := os.MkdirAll(newTool, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newTool, "conf"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	foundReal, err := Shim(a, dir)
	if err != nil {
		t.Fatal(err)
	}
	var reported bool
	for _, n := range foundReal {
		if n == filepath.Join(a.Shims[0].Rel, "brandnewtool") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("real config written inside the shim was not reported: %v", foundReal)
	}
	if _, err := os.Stat(filepath.Join(newTool, "conf")); err != nil {
		t.Errorf("the report deleted the user's config instead of surfacing it: %v", err)
	}
}

// Shim must not clobber a real directory sitting where the agent's own entry
// goes, for the same reason Link refuses to: it would be data loss.
func TestShimDoesNotClobberARealAgentEntry(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	fakeConfigBase(t, "git")
	a := agentOrFail(t, "opencode")
	dir, err := Create(a, "clobber")
	if err != nil {
		t.Fatal(err)
	}
	shimDir := filepath.Join(dir, a.Shims[0].Rel)
	if err := os.MkdirAll(filepath.Join(shimDir, a.Shims[0].Entry), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(shimDir, a.Shims[0].Entry, "keepme")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	foundReal, err := Shim(a, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("Shim destroyed a real directory at the agent entry: %v", err)
	}
	var reported bool
	for _, n := range foundReal {
		if n == filepath.Join(a.Shims[0].Rel, a.Shims[0].Entry) {
			reported = true
		}
	}
	if !reported {
		t.Errorf("a real directory at the agent entry was not reported: %v", foundReal)
	}
}

// Agents without a Shim must be left completely alone.
func TestShimIsANoOpWithoutASpec(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	fakeConfigBase(t, "git")
	for _, name := range []string{"claude", "codex", "pi"} {
		a := agentOrFail(t, name)
		if len(a.Shims) != 0 {
			t.Fatalf("%s unexpectedly has a shim spec", name)
		}
		dir, err := Create(a, "nos")
		if err != nil {
			t.Fatal(err)
		}
		foundReal, err := Shim(a, dir)
		if err != nil || foundReal != nil {
			t.Errorf("%s: Shim = (%v, %v), want empty", name, foundReal, err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("%s: Shim created %d entries in a profile with no shim spec", name, len(entries))
		}
	}
}

// Every declared shim gets its own directory, each with its own passthrough set.
// With one shim this is what Shim always did; the test exists so the second one
// cannot silently share the first one's links.
func TestShimBuildsOneDirectoryPerSpec(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "dat"))
	for _, p := range []string{"cfg/opencode", "cfg/git", "dat/opencode", "dat/fonts"} {
		if err := os.MkdirAll(filepath.Join(home, p), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	a := agent.Agent{Name: "x", Shims: []agent.Shim{
		{Env: "XDG_CONFIG_HOME", Rel: "xdg", Entry: "opencode", Fallback: ".config"},
		{Env: "XDG_DATA_HOME", Rel: "xdg-data", Entry: "opencode", Fallback: ".local/share"},
	}}
	dir := t.TempDir()
	if _, err := Shim(a, dir); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ rel, entry, passthrough, absent string }{
		{"xdg", "opencode", "git", "fonts"},
		{"xdg-data", "opencode", "fonts", "git"},
	} {
		got, err := os.Readlink(filepath.Join(dir, tc.rel, tc.entry))
		if err != nil || got != dir {
			t.Errorf("%s/%s -> %q (err %v), want the profile %q", tc.rel, tc.entry, got, err, dir)
		}
		if _, err := os.Lstat(filepath.Join(dir, tc.rel, tc.passthrough)); err != nil {
			t.Errorf("%s: missing passthrough %s: %v", tc.rel, tc.passthrough, err)
		}
		if _, err := os.Lstat(filepath.Join(dir, tc.rel, tc.absent)); err == nil {
			t.Errorf("%s: has %s, which belongs to the other shim's base", tc.rel, tc.absent)
		}
	}
}
