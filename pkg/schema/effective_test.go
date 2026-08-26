package schema

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEffectiveOverlaysTheRuntimeBlockOntoTheCommonVocabulary(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "p.yaml", `version: "1"
name: p
targets:
  - opencode
skills:
  ponytail:
    source:
      local:
        path: ./ponytail
runtimes:
  opencode:
    skills:
      ponytail:
        enabled: false
    variants:
      brainstorm:
        - "/superpowers:brainstorming {}"
`)
	p, warns, err := Effective(filepath.Join(dir, "p.yaml"), "opencode")
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	// §4: the resource stays visible for diagnostics, but disabled.
	s, ok := p.Skills["ponytail"]
	if !ok {
		t.Fatal("disabled resource dropped; §4 keeps it for render and explain")
	}
	if s.Enabled {
		t.Error("runtime overlay did not disable the skill")
	}
	if s.Source == nil {
		t.Error("the inherited locator was lost by an enabled-only overlay")
	}
	// Runtime-native keys stay under the runtime.
	if got := p.Runtimes["opencode"].Variants["brainstorm"]; len(got) != 1 {
		t.Errorf("variants = %v, want one arg", got)
	}
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
}

func TestEffectiveIgnoresAnInheritedNonTargetBlockAndWarnsOnALocalOne(t *testing.T) {
	dir := t.TempDir()
	// §7.1: inherited, ignored SILENTLY.
	write(t, dir, "base.yaml", "version: \"1\"\nname: base\nruntimes:\n  opencode:\n    environment:\n      A: b\n")
	write(t, dir, "leaf.yaml", "version: \"1\"\nname: leaf\nextends: base\ntargets:\n  - claude\n")
	if _, warns, err := Effective(filepath.Join(dir, "leaf.yaml"), "claude"); err != nil || len(warns) != 0 {
		t.Errorf("warns = %v, err = %v; an inherited non-target block is ignored silently", warns, err)
	}
	// §7.2: locally declared, WARNS.
	write(t, dir, "local.yaml", "version: \"1\"\nname: local\ntargets:\n  - claude\nruntimes:\n  opencode:\n    environment:\n      A: b\n")
	_, warns, err := Effective(filepath.Join(dir, "local.yaml"), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0].Text, "opencode") {
		t.Errorf("warns = %v; a locally declared non-target block must warn and name the runtime", warns)
	}
}

func TestEffectiveRefusesABaseProfileAndNamesWhy(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "base.yaml", "version: \"1\"\nname: coding-base\n")
	_, _, err := Effective(filepath.Join(dir, "base.yaml"), "claude")
	if err == nil || !strings.Contains(err.Error(), "base profile") {
		t.Errorf("err = %v; §5.3 requires the message to say it is a base profile", err)
	}
}
