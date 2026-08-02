package profile

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ackstorm/agent-profile/internal/agent"
)

func TestLinkRemovesAnUnsharedSymlink(t *testing.T) {
	realHome := t.TempDir()
	realFile := filepath.Join(realHome, ".claude.json")
	if err := os.WriteFile(realFile, []byte(`{"real":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(realFile, filepath.Join(dir, ".claude.json")); err != nil {
		t.Fatal(err)
	}

	a := agent.Agent{Name: "test", Unshared: []string{".claude.json"}}
	_, _, unshared, _, err := Link(a, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(unshared, ".claude.json") {
		t.Errorf("unshared = %v, want it to report .claude.json", unshared)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".claude.json")); !os.IsNotExist(err) {
		t.Error("the symlink is still in the profile")
	}
	// The one thing that must never happen.
	if _, err := os.Stat(realFile); err != nil {
		t.Fatalf("the real file was removed: %v", err)
	}
}

func TestLinkLeavesARealFileAtAnUnsharedPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(p, []byte(`{"mine":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a := agent.Agent{Name: "test", Unshared: []string{".claude.json"}}
	_, _, unshared, _, err := Link(a, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(unshared) != 0 {
		t.Errorf("unshared = %v, want nothing: a real file belongs to the profile", unshared)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != `{"mine":true}` {
		t.Errorf("the profile's own file was disturbed: %q, %v", b, err)
	}
}

func TestLinkIsQuietWhenNothingIsUnshared(t *testing.T) {
	dir := t.TempDir()
	a := agent.Agent{Name: "test", Unshared: []string{".claude.json"}}
	_, _, unshared, _, err := Link(a, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(unshared) != 0 {
		t.Errorf("unshared = %v, want nothing when the path is absent", unshared)
	}
}

// Targets that do not exist yet (fresh agent install) are skipped, not an
// error — otherwise creating a profile before ever running the agent fails.
//
// Every share is the agent's credential, and a credentials file cannot be
// fabricated, so a missing target is always skipped. TestLinkNeverInventsAMissingTarget
// pins the other half: nothing is written into the real home either.
func TestLinkSkipsMissingTargets(t *testing.T) {
	dir := t.TempDir()
	a := agent.Agent{Name: "fake", Shared: []agent.Share{
		{Rel: "auth.json", From: filepath.Join(t.TempDir(), "nope.json")},
	}}
	linked, skipped, _, _, err := Link(a, dir)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(linked) != 0 {
		t.Errorf("linked = %v, want none", linked)
	}
	if len(skipped) != 1 || skipped[0] != "auth.json" {
		t.Errorf("skipped = %v, want [auth.json]", skipped)
	}
	// Nothing was created in the profile for a share that did not happen.
	if _, err := os.Lstat(filepath.Join(dir, "auth.json")); !os.IsNotExist(err) {
		t.Error("Link left something at auth.json for a skipped share")
	}
}

func TestLinkCreatesSymlinks(t *testing.T) {
	src := t.TempDir()
	sessions := filepath.Join(src, "sessions")
	if err := os.Mkdir(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	authFile := filepath.Join(src, "auth.json")
	if err := os.WriteFile(authFile, []byte(`{"t":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	a := agent.Agent{Name: "fake", Shared: []agent.Share{
		{Rel: "sessions", From: sessions},
		{Rel: "auth.json", From: authFile},
	}}

	linked, _, _, _, err := Link(a, dir)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(linked) != 2 {
		t.Fatalf("linked = %v, want 2 entries", linked)
	}
	for _, rel := range []string{"sessions", "auth.json"} {
		fi, err := os.Lstat(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("Lstat %s: %v", rel, err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is not a symlink", rel)
		}
	}
}

// claude shares plugins/cache, which is nested: the parent must be created.
func TestLinkCreatesParentDirs(t *testing.T) {
	src := t.TempDir()
	cache := filepath.Join(src, "cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	a := agent.Agent{Name: "fake", Shared: []agent.Share{
		{Rel: "plugins/cache", From: cache},
	}}
	if _, _, _, _, err := Link(a, dir); err != nil {
		t.Fatalf("Link: %v", err)
	}
	fi, err := os.Lstat(filepath.Join(dir, "plugins", "cache"))
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("plugins/cache is not a symlink")
	}
}

// Called on every run, so it must be idempotent.
func TestLinkIsIdempotent(t *testing.T) {
	src := t.TempDir()
	sessions := filepath.Join(src, "sessions")
	if err := os.Mkdir(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	a := agent.Agent{Name: "fake", Shared: []agent.Share{
		{Rel: "sessions", From: sessions},
	}}
	if _, _, _, _, err := Link(a, dir); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := Link(a, dir); err != nil {
		t.Fatalf("second Link: %v", err)
	}
}

// A stale link pointing somewhere else gets repointed, not left wrong.
func TestLinkRepointsStaleSymlink(t *testing.T) {
	src := t.TempDir()
	want := filepath.Join(src, "real")
	if err := os.Mkdir(want, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(src, "old"), filepath.Join(dir, "sessions")); err != nil {
		t.Fatal(err)
	}
	a := agent.Agent{Name: "fake", Shared: []agent.Share{
		{Rel: "sessions", From: want},
	}}
	if _, _, _, _, err := Link(a, dir); err != nil {
		t.Fatalf("Link: %v", err)
	}
	got, err := os.Readlink(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("link target = %q, want %q", got, want)
	}
}

// If an agent replaced our link with a real file (token rotation via
// temp+rename), Link must restore the sharing WITHOUT destroying what it found.
//
// Both halves matter and each catches a different regression. Healing: this used
// to be a hard refusal, which dead-ended every later `ap run` on the profile —
// measured on two real claude profiles. Keeping: healing by removing would delete
// a credential, and the file moved aside may hold the newer token of the two.
func TestLinkMovesRealDataAsideAndRelinks(t *testing.T) {
	src := t.TempDir()
	sessions := filepath.Join(src, "sessions")
	if err := os.Mkdir(sessions, 0o700); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	real := filepath.Join(dir, "sessions")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "keepme"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := agent.Agent{Name: "fake", Shared: []agent.Share{
		{Rel: "sessions", From: sessions},
	}}
	_, _, _, orphaned, err := Link(a, dir)
	if err != nil {
		t.Fatalf("Link over real data = %v, want it healed", err)
	}
	if want := []string{"sessions" + orphanSuffix}; !slices.Equal(orphaned, want) {
		t.Errorf("orphaned = %v, want %v", orphaned, want)
	}
	// The sharing is restored...
	fi, err := os.Lstat(real)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("share is not a symlink after Link: the sharing was not restored")
	}
	// ...and nothing was destroyed to do it.
	if _, err := os.Stat(filepath.Join(real+orphanSuffix, "keepme")); err != nil {
		t.Errorf("Link destroyed real data instead of moving it aside: %v", err)
	}
}

// A second overwrite must not fail because the first orphan is still there.
// os.Root.Rename overwrites, and that is the intended behaviour: of two stale
// credentials the older one is the one worth losing.
func TestLinkOverwritesAPreviousOrphan(t *testing.T) {
	src := t.TempDir()
	cred := filepath.Join(src, "cred")
	if err := os.WriteFile(cred, []byte("shared"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	a := agent.Agent{Name: "fake", Shared: []agent.Share{{Rel: "cred", From: cred}}}

	for _, content := range []string{"first", "second"} {
		if err := os.Remove(filepath.Join(dir, "cred")); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cred"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, _, _, err := Link(a, dir); err != nil {
			t.Fatalf("Link with %q in place = %v", content, err)
		}
	}
	got, err := os.ReadFile(filepath.Join(dir, "cred"+orphanSuffix))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Errorf("orphan = %q, want %q: the newer file must win", got, "second")
	}
}

// THE important test. Deleting a profile must never reach through a symlink
// into the user's real session history.
func TestDeleteDoesNotFollowSymlinks(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	realHome := t.TempDir()
	realSessions := filepath.Join(realHome, "projects")
	if err := os.Mkdir(realSessions, 0o700); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(realSessions, "important-transcript.jsonl")
	if err := os.WriteFile(canary, []byte("do not lose me"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := agent.Agent{Name: "claude", Shared: []agent.Share{
		{Rel: "projects", From: realSessions},
	}}
	dir, err := Create(a, "plan")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := Link(a, dir); err != nil {
		t.Fatal(err)
	}

	if err := Delete(a, "plan"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("profile dir still present: %v", err)
	}
	if _, err := os.Stat(canary); err != nil {
		t.Fatalf("DELETE FOLLOWED THE SYMLINK AND ATE REAL DATA: %v", err)
	}
	if _, err := os.Stat(realSessions); err != nil {
		t.Fatalf("real sessions directory was removed: %v", err)
	}
}

func TestDeleteMissingProfileErrors(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := Delete(agentOrFail(t, "claude"), "nope"); err == nil {
		t.Error("Delete of missing profile = nil error, want error")
	}
}

// Belt as well as braces, the same defense in depth as
// TestDeleteDoesNotFollowSymlinks and TestDeleteDoesNotFollowTheConfigShim:
// Delete must refuse Default on its own, not only because ParseRef rejects it
// upstream. Dir(a, Default) is the user's real config directory, and Delete is
// os.RemoveAll — the guard must not depend on a validator having been called
// correctly somewhere else.
func TestDeleteRefusesDefaultDirectly(t *testing.T) {
	a := agentOrFail(t, "claude")
	if err := Delete(a, Default); err == nil {
		t.Error("Delete(a, Default) = nil error, want refusal")
	}
}
