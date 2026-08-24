package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func manifestFor(name string, platforms ...string) string {
	var b strings.Builder
	b.WriteString("version: 1\nname: " + name + "\nplatforms:\n")
	for _, p := range platforms {
		b.WriteString("  " + p + ":\n")
	}
	return b.String()
}

func TestLoadScansADirectoryInFilenameOrderAndNotRecursively(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "review.yaml", manifestFor("review", "claude"))
	write(t, dir, "execute.yml", manifestFor("execute", "codex"))
	write(t, dir, "notes.md", "not a manifest")
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "node_modules"), "evil.yaml", manifestFor("evil", "claude"))

	ms, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ms) != 2 {
		t.Fatalf("loaded %d manifests, want 2 (the subdirectory must not be walked)", len(ms))
	}
	// Sorted by filename: execute.yml before review.yaml.
	if ms[0].Name != "execute" || ms[1].Name != "review" {
		t.Fatalf("order = %q, %q; want execute then review", ms[0].Name, ms[1].Name)
	}
}

func TestLoadAcceptsASingleFileWhateverItIsCalled(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "profiles.txt", manifestFor("plan", "claude"))
	ms, err := Load(filepath.Join(dir, "profiles.txt"))
	if err != nil || len(ms) != 1 || ms[0].Name != "plan" {
		t.Fatalf("Load(file) = %+v, %v", ms, err)
	}
}

// Two manifests MAY share a name when they target different platforms, because
// the real identity is <platform>:<name>.
func TestLoadAllowsOneNameAcrossTwoPlatforms(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.yaml", manifestFor("execute", "claude"))
	write(t, dir, "b.yaml", manifestFor("execute", "codex"))
	if _, err := Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoadRefusesTwoManifestsProducingTheSameIdentity(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.yaml", manifestFor("execute", "claude"))
	write(t, dir, "b.yaml", manifestFor("execute", "claude", "codex"))
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load = nil error, want a duplicate-profile error")
	}
	// Both files, because "which two" is the only useful part of the answer.
	for _, want := range []string{"claude:execute", "a.yaml", "b.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %q", err, want)
		}
	}
}

func TestLoadFailsOnTheFirstBadManifestAndReturnsNothing(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.yaml", manifestFor("plan", "claude"))
	write(t, dir, "b.yaml", "version: 1\nname: x\nplatforms:\n  claude:\n    varients:\n      v:\n")
	ms, err := Load(dir)
	if err == nil {
		t.Fatal("Load = nil error, want a schema error")
	}
	if ms != nil {
		t.Errorf("Load returned %d manifests alongside an error; §9 aborts the whole run with nothing done", len(ms))
	}
}

func TestLoadSaysSoWhenADirectoryHoldsNoManifests(t *testing.T) {
	_, err := Load(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no *.yaml") {
		t.Fatalf("Load(empty dir) = %v, want an error naming the extensions", err)
	}
}
