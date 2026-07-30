package profile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// State is a real directory inside the profile, never named in CloneAllow — the
// same way "projects" is absent from claude's real row. It survives in the
// source and simply never gets copied, because nothing ever names it.
func TestCloneSkipsState(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "projects", "deep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "projects", "deep", "a.jsonl"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "settings.json"), []byte(`{"model":"haiku"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	a := agent.Agent{Name: "test", CloneAllow: []string{"settings.json"}}
	if err := Clone(a, src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "projects")); !os.IsNotExist(err) {
		t.Error("the source profile's history was copied into the clone")
	}
	if _, err := os.Stat(filepath.Join(dst, "settings.json")); err != nil {
		t.Errorf("configuration was not cloned: %v", err)
	}
}

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

	a := agent.Agent{Name: "fake", CloneAllow: []string{"settings.json", "plugins", "skills", "empty"}}
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

// A CloneAllow entry that is itself a symlink in the source — the shape every
// Shared entry has — must never be copied. TestCloneAllowNeverNamesASharedPath
// keeps the real registry from ever doing this; this pins the mechanism that
// would make it safe even if it did.
func TestCloneSkipsASymlinkedAllowlistEntry(t *testing.T) {
	realHome := t.TempDir()
	realSessions := filepath.Join(realHome, "projects")
	writeTree(t, realSessions, map[string]string{"big-transcript.jsonl": "lots of data"})

	src, dst := t.TempDir(), t.TempDir()
	writeTree(t, src, map[string]string{"settings.json": "{}"})
	if err := os.Symlink(realSessions, filepath.Join(src, "projects")); err != nil {
		t.Fatal(err)
	}

	a := agent.Agent{Name: "fake", CloneAllow: []string{"settings.json", "projects"}}
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

// A symlink nested inside an allowed directory must be skipped too — not just
// one named directly in CloneAllow. Only the WalkDir-level symlink check can
// catch this, since the entry itself ("skills") is a real directory.
func TestCloneSkipsASymlinkNestedInAnAllowedDirectory(t *testing.T) {
	elsewhere := t.TempDir()
	writeTree(t, elsewhere, map[string]string{"payload.txt": "should not be copied"})

	src, dst := t.TempDir(), t.TempDir()
	writeTree(t, src, map[string]string{"skills/mine/SKILL.md": "# mine"})
	if err := os.Symlink(elsewhere, filepath.Join(src, "skills", "handmade-dir-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(elsewhere, "payload.txt"), filepath.Join(src, "skills", "handmade-file-link")); err != nil {
		t.Fatal(err)
	}

	a := agent.Agent{Name: "fake", CloneAllow: []string{"skills"}}
	if err := Clone(a, src, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	for _, rel := range []string{"skills/handmade-dir-link", "skills/handmade-file-link"} {
		if _, err := os.Lstat(filepath.Join(dst, rel)); !os.IsNotExist(err) {
			t.Errorf("%s was reproduced in the clone; symlinks must be skipped", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "skills", "handmade-dir-link", "payload.txt")); err == nil {
		t.Error("Clone followed a symlink and copied its contents")
	}
	if _, err := os.Stat(filepath.Join(dst, "skills", "mine", "SKILL.md")); err != nil {
		t.Errorf("skills/mine/SKILL.md was not copied: %v", err)
	}
}

// Selection is purely by name now: a path absent from CloneAllow is never
// copied, whether it is the credential, accumulated runtime, or anything else —
// there is no separate skip list to keep in sync with the allowlist.
func TestCloneNeverCopiesAPathOutsideCloneAllow(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeTree(t, src, map[string]string{
		"settings.json":        "{}",
		"auth.json":            `{"token":"secret"}`,
		"plugins/cache/x/file": "cached",
		"plugins/config.json":  `{"keep":true}`,
	})

	a := agent.Agent{Name: "fake", CloneAllow: []string{"settings.json", "plugins/config.json"}}
	if err := Clone(a, src, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dst, "auth.json")); !os.IsNotExist(err) {
		t.Error("auth.json was copied; credentials must never be duplicated")
	}
	if _, err := os.Stat(filepath.Join(dst, "plugins", "cache")); !os.IsNotExist(err) {
		t.Error("plugins/cache was copied; it was never named in CloneAllow")
	}
	// A sibling under the same parent must survive: selection is per path, not
	// per parent directory.
	if _, err := os.Stat(filepath.Join(dst, "plugins", "config.json")); err != nil {
		t.Errorf("plugins/config.json was not copied: %v", err)
	}
}

// Secrets must not become world-readable in the clone.
func TestClonePreservesFileMode(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	p := filepath.Join(src, "private.json")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Clone(agent.Agent{Name: "fake", CloneAllow: []string{"private.json"}}, src, dst); err != nil {
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

func TestCloneCopiesOnlyTheAllowlist(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	for _, rel := range []string{
		"settings.json",          // allowed
		"skills/x/SKILL.md",      // allowed, nested
		"tmp/huge.bin",           // runtime, must not be copied
		"telemetry/events.jsonl", // runtime
		"file-history/a",         // runtime
	} {
		p := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := agent.Agent{Name: "test", CloneAllow: []string{"settings.json", "skills"}}
	if err := Clone(a, src, dst); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"settings.json", "skills/x/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(dst, want)); err != nil {
			t.Errorf("%s was not cloned: %v", want, err)
		}
	}
	for _, unwanted := range []string{"tmp", "telemetry", "file-history"} {
		if _, err := os.Stat(filepath.Join(dst, unwanted)); !os.IsNotExist(err) {
			t.Errorf("%s was cloned and should not have been", unwanted)
		}
	}
}

// An allowlist entry that is not present in the source is not an error: profiles and
// real config dirs differ in what they happen to contain.
func TestCloneToleratesAMissingAllowlistEntry(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "settings.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := agent.Agent{Name: "test", CloneAllow: []string{"settings.json", "nope", "also/nope"}}
	if err := Clone(a, src, dst); err != nil {
		t.Fatalf("a missing entry must not fail the clone: %v", err)
	}
}

// The allowlist is a list of names, not patterns: an entry must never escape dst.
func TestCloneAllowCannotEscape(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	a := agent.Agent{Name: "test", CloneAllow: []string{"../../etc/passwd"}}
	if err := Clone(a, src, dst); err == nil {
		t.Error("want an error for an allowlist entry containing ..")
	}
}
