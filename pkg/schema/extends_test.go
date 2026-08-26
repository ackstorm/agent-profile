package schema

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

func TestLoadFoldsAnExtendsChainParentFirst(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "base.yaml", "version: \"1\"\nname: base\nmodel:\n  type: anthropic\n  parameters:\n    effort: high\n")
	write(t, dir, "mid.yaml", "version: \"1\"\nname: mid\nextends: base\nmodel:\n  parameters:\n    effort: xhigh\n")
	write(t, dir, "leaf.yaml", "version: \"1\"\nname: leaf\nextends: ./mid.yaml\ntargets:\n  - claude\n")

	got, _, err := Load(filepath.Join(dir, "leaf.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Map["model"].Map["type"].Str != "anthropic" {
		t.Error("grandparent's model.type was lost")
	}
	if got.Map["model"].Map["parameters"].Map["effort"].Str != "xhigh" {
		t.Error("parent's override was lost")
	}
	if got.Map["name"].Str != "leaf" {
		t.Errorf("name = %q, want leaf — the leaf's own name must win", got.Map["name"].Str)
	}
}

func TestLoadRefusesACycleAndNamesIt(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.yaml", "version: \"1\"\nname: a\nextends: b\n")
	write(t, dir, "b.yaml", "version: \"1\"\nname: b\nextends: a\n")
	_, _, err := Load(filepath.Join(dir, "a.yaml"))
	if err == nil {
		t.Fatal("cycle accepted")
	}
	if !strings.Contains(err.Error(), "a.yaml") {
		t.Errorf("error %q does not name the file that closes the cycle", err)
	}
}

func TestLoadRefusesAParentOutsideTheManifestDirectory(t *testing.T) {
	// --from once skipped ValidName and became a path traversal that copied the
	// user's real ~/.claude into a profile. `extends` turns manifest text into a
	// path the same way and gets the same guard.
	//
	// Both subtests place a genuinely loadable parent OUTSIDE the manifest
	// directory, so a removed guard would make Load succeed rather than merely
	// fail to find a file — os.ReadFile erroring on a missing/unparseable
	// target would make this test pass vacuously even with the guard deleted.
	outer := t.TempDir()
	sub := filepath.Join(outer, "manifests")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, outer, "secret.yaml", "version: \"1\"\nname: secret\n")
	write(t, sub, "leaf.yaml", "version: \"1\"\nname: leaf\nextends: ../secret.yaml\n")
	if _, _, err := Load(filepath.Join(sub, "leaf.yaml")); err == nil {
		t.Fatal("`extends: ../secret.yaml` reached a real, loadable file outside the manifest directory")
	}

	write(t, outer, "base", "version: \"1\"\nname: base\n")
	write(t, sub, "leaf2.yaml", "version: \"1\"\nname: leaf\nextends: ../base\n")
	if _, _, err := Load(filepath.Join(sub, "leaf2.yaml")); err == nil {
		t.Fatal("`extends: ../base` (no .yaml suffix) reached a real, loadable file outside the manifest directory")
	}
}
