//go:build unix

package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// ValidName is the only thing standing between user input and a path under Root.
// Every caller that builds a path from user input must run it — `ap create
// --from` skipped it once and became a traversal out of the profile root.
func TestValidNameRejectsTraversal(t *testing.T) {
	for _, bad := range []string{
		"..", ".", "../x", "../../../.claude", "a/b", `a\b`, ".hidden", "", "has space", "x..y/z",
	} {
		if err := ValidName(bad); err == nil {
			t.Errorf("ValidName(%q) = nil, want error", bad)
		}
	}
	for _, good := range []string{"plan", "review", "my_profile-2", "a"} {
		if err := ValidName(good); err != nil {
			t.Errorf("ValidName(%q) = %v, want nil", good, err)
		}
	}
}

// `ap create --from .` resolved the source to the PARENT of the destination, so
// WalkDir descended into the directory Clone was writing to and recursed until
// the path was too long for the filesystem — 2000+ directories, and with a
// populated source it re-copied every real file at each level.
func TestCloneRefusesDestinationInsideSource(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{"settings.json": "{}"})

	dst := filepath.Join(src, "inner")
	if err := os.Mkdir(dst, 0o700); err != nil {
		t.Fatal(err)
	}

	err := Clone(agent.Agent{Name: "fake"}, src, dst)
	if err == nil {
		t.Fatal("Clone into a subdirectory of the source = nil error, want refusal")
	}
	if !strings.Contains(err.Error(), "inside the source") {
		t.Errorf("error %q does not explain the containment problem", err)
	}
	// And nothing was written before the refusal.
	if _, err := os.Stat(filepath.Join(dst, "settings.json")); !os.IsNotExist(err) {
		t.Error("Clone copied files before refusing")
	}
}

func TestCloneRefusesIdenticalSourceAndDestination(t *testing.T) {
	d := t.TempDir()
	err := Clone(agent.Agent{Name: "fake"}, d, d)
	if err == nil {
		t.Fatal("Clone(src, src) = nil error, want refusal")
	}
	if !strings.Contains(err.Error(), "same directory") {
		t.Errorf("error %q does not explain the problem", err)
	}
}

// WalkDir lstats its root, so a symlinked source profile used to walk nothing at
// all: Clone reported success having copied zero files.
func TestCloneResolvesSymlinkedSource(t *testing.T) {
	real := t.TempDir()
	writeTree(t, real, map[string]string{"settings.json": `{"model":"opus"}`})

	link := filepath.Join(t.TempDir(), "golden")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	if err := Clone(agent.Agent{Name: "fake"}, link, dst); err != nil {
		t.Fatalf("Clone from a symlinked source: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "settings.json"))
	if err != nil {
		t.Fatalf("clone from a symlinked source copied nothing: %v", err)
	}
	if string(got) != `{"model":"opus"}` {
		t.Errorf("settings.json = %q, want the source content", got)
	}
}

// os.Lstat only refuses to follow the FINAL path component. With a nested Rel
// such as "plugins/cache", a symlinked "plugins" inside the profile would make
// the remove-and-relink land in the user's real home. os.Root refuses symlink
// traversal, so Link must error instead of reaching through.
func TestLinkRefusesToReachThroughSymlinkedAncestor(t *testing.T) {
	realHome := t.TempDir()
	realPlugins := filepath.Join(realHome, "plugins")
	realCache := filepath.Join(realPlugins, "cache")
	if err := os.MkdirAll(realCache, 0o700); err != nil {
		t.Fatal(err)
	}
	// What the user chose; Link must not repoint it.
	userChoice := filepath.Join(realHome, "user-chose-this")
	if err := os.Mkdir(userChoice, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(realCache); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(userChoice, realCache); err != nil {
		t.Fatal(err)
	}

	// A profile whose "plugins" is itself a symlink into the real home.
	dir := t.TempDir()
	if err := os.Symlink(realPlugins, filepath.Join(dir, "plugins")); err != nil {
		t.Fatal(err)
	}

	apThinks := filepath.Join(realHome, "ap-thinks-this")
	if err := os.Mkdir(apThinks, 0o700); err != nil {
		t.Fatal(err)
	}
	a := agent.Agent{Name: "fake", Shared: []agent.Share{
		{Rel: "plugins/cache", From: apThinks, Kind: agent.Dir},
	}}

	if _, _, err := Link(a, dir); err == nil {
		t.Error("Link through a symlinked ancestor = nil error, want refusal")
	}

	// The decisive assertion: the real home was not rewritten.
	target, err := os.Readlink(realCache)
	if err != nil {
		t.Fatalf("the real cache link is gone: %v", err)
	}
	if target != userChoice {
		t.Errorf("LINK REACHED THROUGH AND REWROTE THE REAL HOME: %s -> %s, want %s",
			realCache, target, userChoice)
	}
}

// Link is called on every `ap run`, so a second call must leave the link pointing
// at the same place — not merely return no error.
func TestLinkIsIdempotentAndKeepsTheTarget(t *testing.T) {
	src := t.TempDir()
	sessions := filepath.Join(src, "sessions")
	if err := os.Mkdir(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	a := agent.Agent{Name: "fake", Shared: []agent.Share{
		{Rel: "sessions", From: sessions, Kind: agent.Dir},
	}}
	for i := range 3 {
		if _, _, err := Link(a, dir); err != nil {
			t.Fatalf("Link call %d: %v", i+1, err)
		}
		got, err := os.Readlink(filepath.Join(dir, "sessions"))
		if err != nil {
			t.Fatalf("after Link call %d: %v", i+1, err)
		}
		if got != sessions {
			t.Fatalf("after Link call %d: target = %q, want %q", i+1, got, sessions)
		}
	}
}

// Delete is canary-tested for a top-level symlink; the nested case is the one a
// naive rewrite gets wrong, so pin it too.
func TestDeleteDoesNotFollowNestedSymlinks(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	realHome := t.TempDir()
	realCache := filepath.Join(realHome, "cache")
	if err := os.MkdirAll(realCache, 0o700); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(realCache, "expensive-download.tar")
	if err := os.WriteFile(canary, []byte("do not lose me"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := agent.Agent{Name: "claude", Shared: []agent.Share{
		{Rel: "plugins/cache", From: realCache, Kind: agent.Dir},
	}}
	dir, err := Create(a, "nested")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Link(a, dir); err != nil {
		t.Fatal(err)
	}
	if err := Delete(a, "nested"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(canary); err != nil {
		t.Fatalf("DELETE FOLLOWED THE NESTED SYMLINK AND ATE REAL DATA: %v", err)
	}
}

// A dangling symlink used to report "profile does not exist" from Delete while
// staying on disk forever.
func TestDeleteRemovesADanglingProfileSymlink(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")
	if err := os.MkdirAll(filepath.Join(Root(), a.Name), 0o700); err != nil {
		t.Fatal(err)
	}
	link := Dir(a, "dangling")
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), link); err != nil {
		t.Fatal(err)
	}
	if err := Delete(a, "dangling"); err != nil {
		t.Fatalf("Delete on a dangling symlink: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("the dangling symlink is still on disk")
	}
}

// List used to hide a symlinked profile that Exists, which and run all accept.
func TestListIncludesSymlinkedProfiles(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")
	real, err := Create(a, "real")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, Dir(a, "golden")); err != nil {
		t.Fatal(err)
	}
	got, err := List(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "golden" || got[1] != "real" {
		t.Errorf("List = %v, want [golden real]", got)
	}
}

// A Kind: File target that does not exist yet cannot be invented, so it must be
// reported. Silence used to mean the user believed their login was shared when it
// was not, and only found out much later when a run dead-ended on "refusing to
// replace real file".
func TestLinkReportsSkippedFileShares(t *testing.T) {
	realHome := t.TempDir()
	present := filepath.Join(realHome, "CLAUDE.md")
	if err := os.WriteFile(present, []byte("# hi"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	a := agent.Agent{Name: "fake", Shared: []agent.Share{
		{Rel: "CLAUDE.md", From: present, Kind: agent.File},
		{Rel: "auth.json", From: filepath.Join(realHome, "auth.json"), Kind: agent.File},
	}}

	linked, skipped, err := Link(a, dir)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(linked) != 1 || linked[0] != "CLAUDE.md" {
		t.Errorf("linked = %v, want [CLAUDE.md]", linked)
	}
	if len(skipped) != 1 || skipped[0] != "auth.json" {
		t.Errorf("skipped = %v, want [auth.json]", skipped)
	}
	// And it did not fabricate a credentials file.
	if _, err := os.Lstat(filepath.Join(realHome, "auth.json")); !os.IsNotExist(err) {
		t.Error("Link created a credentials file that did not exist")
	}
}

// A Kind: Dir target CAN be created, so directory shares always link and never
// land in skipped - the agent would have created the directory itself anyway.
func TestLinkCreatesMissingSharedDirectories(t *testing.T) {
	realHome := t.TempDir()
	missing := filepath.Join(realHome, "sessions")

	dir := t.TempDir()
	a := agent.Agent{Name: "fake", Shared: []agent.Share{
		{Rel: "sessions", From: missing, Kind: agent.Dir},
	}}

	linked, skipped, err := Link(a, dir)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %v, want empty: a directory share can be created", skipped)
	}
	if len(linked) != 1 || linked[0] != "sessions" {
		t.Fatalf("linked = %v, want [sessions]", linked)
	}
	if fi, err := os.Stat(missing); err != nil || !fi.IsDir() {
		t.Errorf("the shared directory was not created: %v", err)
	}
	got, err := os.Readlink(filepath.Join(dir, "sessions"))
	if err != nil || got != missing {
		t.Errorf("link target = %q (%v), want %q", got, err, missing)
	}
}

// Discard removes a half-created profile, and the name it is given came from the
// command line. ValidName is what normally makes that safe, but Discard does not
// depend on ValidName having been called: it removes through an os.Root confined
// to the agent directory, so confinement is enforced by the runtime.
//
// Reverting Discard to os.RemoveAll(Dir(a, name)) makes this test fail.
func TestDiscardCannotEscapeTheAgentDirectory(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	a := agentOrFail(t, "claude")

	// A canary two levels above the agent directory, where a traversal would land.
	if _, err := Create(a, "real"); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(data, "canary.txt")
	if err := os.WriteFile(canary, []byte("do not lose me"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"../../canary.txt",
		"../..",
		"../claude/real",
	} {
		Discard(a, name)
		if _, err := os.Stat(canary); err != nil {
			t.Fatalf("Discard(%q) removed the canary outside the agent directory: %v", name, err)
		}
		if _, err := os.Stat(Dir(a, "real")); err != nil {
			t.Fatalf("Discard(%q) reached a sibling profile: %v", name, err)
		}
	}

	// And it still does its actual job.
	Discard(a, "real")
	if _, err := os.Stat(Dir(a, "real")); !os.IsNotExist(err) {
		t.Errorf("Discard did not remove the profile it was asked to: %v", err)
	}
}
