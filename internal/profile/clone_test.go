package profile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// writeTree builds a directory from a path->content map. Empty content makes a
// directory.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if content == "" {
			if err := os.MkdirAll(p, 0o700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCloneCopiesFilesAndNestedDirs(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeTree(t, src, map[string]string{
		"settings.json":                  `{"model":"opus"}`,
		"plugins/installed_plugins.json": `{"a":true}`,
		"skills/mine/SKILL.md":           "# mine",
		"empty":                          "",
	})

	a := agent.Agent{Name: "fake"}
	if err := Clone(a, src, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	for rel, want := range map[string]string{
		"settings.json":                  `{"model":"opus"}`,
		"plugins/installed_plugins.json": `{"a":true}`,
		"skills/mine/SKILL.md":           "# mine",
	} {
		got, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil {
			t.Errorf("missing %s: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if fi, err := os.Stat(filepath.Join(dst, "empty")); err != nil || !fi.IsDir() {
		t.Errorf("empty directory not copied: %v", err)
	}
}

// Symlinks are the shared state. Copying their contents would duplicate the
// user's whole session history into the new profile.
func TestCloneSkipsSymlinks(t *testing.T) {
	realHome := t.TempDir()
	realSessions := filepath.Join(realHome, "projects")
	writeTree(t, realSessions, map[string]string{"big-transcript.jsonl": "lots of data"})

	src, dst := t.TempDir(), t.TempDir()
	writeTree(t, src, map[string]string{"settings.json": "{}"})
	if err := os.Symlink(realSessions, filepath.Join(src, "projects")); err != nil {
		t.Fatal(err)
	}

	a := agent.Agent{Name: "fake", Shared: []agent.Share{
		{Rel: "projects", From: realSessions},
	}}
	if err := Clone(a, src, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	if _, err := os.Lstat(filepath.Join(dst, "projects")); !os.IsNotExist(err) {
		t.Errorf("projects was copied into the clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "settings.json")); err != nil {
		t.Errorf("settings.json was not copied: %v", err)
	}
}

// The test above cannot pin the symlink rule: it declares the same path in
// Shared, so isShared returns first and the symlink is never reached as a
// symlink. This one uses a link the user made by hand, which no Shared entry
// covers, so only the symlink rule can possibly skip it.
func TestCloneSkipsAnUnsharedSymlink(t *testing.T) {
	elsewhere := t.TempDir()
	writeTree(t, elsewhere, map[string]string{"payload.txt": "should not be copied"})

	src, dst := t.TempDir(), t.TempDir()
	writeTree(t, src, map[string]string{"settings.json": "{}"})
	// Neither of these is in Shared.
	if err := os.Symlink(elsewhere, filepath.Join(src, "handmade-dir-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(elsewhere, "payload.txt"), filepath.Join(src, "handmade-file-link")); err != nil {
		t.Fatal(err)
	}

	if err := Clone(agent.Agent{Name: "fake"}, src, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	for _, rel := range []string{"handmade-dir-link", "handmade-file-link"} {
		if _, err := os.Lstat(filepath.Join(dst, rel)); !os.IsNotExist(err) {
			t.Errorf("%s was reproduced in the clone; symlinks must be skipped", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "handmade-dir-link", "payload.txt")); err == nil {
		t.Error("Clone followed a symlink and copied its contents")
	}
	if _, err := os.Stat(filepath.Join(dst, "settings.json")); err != nil {
		t.Errorf("settings.json was not copied: %v", err)
	}
}

// A Shared entry may be a real file in the source (older profile, or an agent
// that rewrote it). Skipping by relative path keeps Link working afterwards.
func TestCloneSkipsSharedPathsEvenWhenReal(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeTree(t, src, map[string]string{
		"settings.json":        "{}",
		"auth.json":            `{"token":"secret"}`,
		"plugins/cache/x/file": "cached",
		"plugins/config.json":  `{"keep":true}`,
	})

	a := agent.Agent{Name: "fake", Shared: []agent.Share{
		{Rel: "auth.json", From: "/nonexistent/auth.json"},
		{Rel: "plugins/cache", From: "/nonexistent/cache"},
	}}
	if err := Clone(a, src, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dst, "auth.json")); !os.IsNotExist(err) {
		t.Error("auth.json was copied; credentials must never be duplicated")
	}
	if _, err := os.Stat(filepath.Join(dst, "plugins", "cache")); !os.IsNotExist(err) {
		t.Error("plugins/cache was copied; it is shared, not per profile")
	}
	// A sibling under the same parent must survive: the skip is per path, not
	// per parent directory.
	if _, err := os.Stat(filepath.Join(dst, "plugins", "config.json")); err != nil {
		t.Errorf("plugins/config.json was skipped too: %v", err)
	}
}

// Secrets must not become world-readable in the clone.
func TestClonePreservesFileMode(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	p := filepath.Join(src, "private.json")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Clone(agent.Agent{Name: "fake"}, src, dst); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dst, "private.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 600", got)
	}
}

func TestCloneMissingSourceErrors(t *testing.T) {
	if err := Clone(agent.Agent{Name: "fake"}, filepath.Join(t.TempDir(), "nope"), t.TempDir()); err == nil {
		t.Error("Clone from missing source = nil error, want error")
	}
}
