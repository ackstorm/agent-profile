package manifest

import (
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/pkg/agentreg"
)

const goodManifest = `version: 1
name: execute

bootstrap:
  - npm install -g @ackstorm/ach-cli

platforms:
  claude:
    install:
      - npx get-shit-done-cc@latest --claude --global
    variants:
      opus:
        args: --model=claude-opus-5 --effort=xhigh
      execute-plan:
        args:
          - --effort=xhigh
          - /superpowers:executing-plans {}
  codex:
`

func TestParseReadsTheWholeSchema(t *testing.T) {
	m, err := Parse("execute.yaml", []byte(goodManifest))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Path != "execute.yaml" || m.Name != "execute" {
		t.Fatalf("Path/Name = %q/%q", m.Path, m.Name)
	}
	if len(m.Bootstrap) != 1 || m.Bootstrap[0] != "npm install -g @ackstorm/ach-cli" {
		t.Fatalf("Bootstrap = %q", m.Bootstrap)
	}
	// Sorted by agent name: claude before codex, whatever the file order.
	if len(m.Platforms) != 2 || m.Platforms[0].Agent.Name != "claude" || m.Platforms[1].Agent.Name != "codex" {
		t.Fatalf("Platforms = %+v", m.Platforms)
	}
	c := m.Platforms[0]
	if len(c.Install) != 1 {
		t.Fatalf("Install = %q", c.Install)
	}
	// Sorted by variant name: execute-plan before opus.
	if len(c.Variants) != 2 || c.Variants[0].Name != "execute-plan" || c.Variants[1].Name != "opus" {
		t.Fatalf("Variants = %+v", c.Variants)
	}
	if got := strings.Join(c.Variants[1].Args, "\x00"); got != "--model=claude-opus-5\x00--effort=xhigh" {
		t.Errorf("string args = %q", c.Variants[1].Args)
	}
	// An agent with nothing under it is a platform with no configuration.
	if k := m.Platforms[1]; len(k.Install) != 0 || len(k.Variants) != 0 {
		t.Errorf("codex = %+v, want an empty platform", k)
	}
}

// §7.1: the two forms of args are the same thing on disk. If they can produce
// different argv, one of them is lying about what it does.
func TestTheTwoArgsFormsAgree(t *testing.T) {
	str := `version: 1
name: x
platforms:
  claude:
    variants:
      v:
        args: --model=claude-opus-5 --effort=xhigh
`
	list := `version: 1
name: x
platforms:
  claude:
    variants:
      v:
        args:
          - --model=claude-opus-5
          - --effort=xhigh
`
	a, err := Parse("a.yaml", []byte(str))
	if err != nil {
		t.Fatalf("string form: %v", err)
	}
	b, err := Parse("b.yaml", []byte(list))
	if err != nil {
		t.Fatalf("list form: %v", err)
	}
	x := strings.Join(a.Platforms[0].Variants[0].Args, "\x00")
	y := strings.Join(b.Platforms[0].Variants[0].Args, "\x00")
	if x != y {
		t.Fatalf("the two forms disagree: %q vs %q", x, y)
	}
}

// name: default is the sentinel, and it must be reached WITHOUT loosening
// ValidName — which still rejects it, and is what every other path relies on.
func TestNameDefaultIsTheSentinelAndValidNameStillRejectsIt(t *testing.T) {
	m, err := Parse("d.yaml", []byte("version: 1\nname: default\nplatforms:\n  claude:\n    install:\n      - true\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Name != agentreg.Default || !m.IsDefault() {
		t.Fatalf("Name = %q, IsDefault = %v", m.Name, m.IsDefault())
	}
	if agentreg.ValidName(agentreg.Default) == nil {
		t.Fatal("ValidName now ACCEPTS \"default\"; the sentinel must be handled above it, never by loosening it")
	}
}

func TestSchemaRejectsWhatItMustRejectBeforeAnythingIsCreated(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"missing version", "name: x\nplatforms:\n  claude:\n", "version"},
		{"wrong version", "version: 2\nname: x\nplatforms:\n  claude:\n", "version 2"},
		{"missing name", "version: 1\nplatforms:\n  claude:\n", "name"},
		{"missing platforms", "version: 1\nname: x\n", "platforms"},
		{"no platform entries", "version: 1\nname: x\nplatforms:\n", "at least one"},
		{"unknown top-level key", "version: 1\nname: x\nskills:\n  - a\nplatforms:\n  claude:\n", "unknown key \"skills\""},
		{"unknown platform key", "version: 1\nname: x\nplatforms:\n  claude:\n    varients:\n      v:\n", "unknown key \"varients\""},
		{"unknown variant key", "version: 1\nname: x\nplatforms:\n  claude:\n    variants:\n      v:\n        arg: -p\n", "unknown key \"arg\""},
		{"unknown agent", "version: 1\nname: x\nplatforms:\n  gemini:\n", "unknown platform \"gemini\""},
		{"traversing name", "version: 1\nname: ../../x\nplatforms:\n  claude:\n", "name"},
		{"dotted name", "version: 1\nname: .ssh\nplatforms:\n  claude:\n", "name"},
		{"traversing variant", "version: 1\nname: x\nplatforms:\n  claude:\n    variants:\n      ../y:\n        args: -p\n", "variant"},
		{"variant without args", "version: 1\nname: x\nplatforms:\n  claude:\n    variants:\n      v:\n", "args"},
		{"empty args", "version: 1\nname: x\nplatforms:\n  claude:\n    variants:\n      v:\n        args: \"\"\n", "no arguments"},
		{"empty args list", "version: 1\nname: x\nplatforms:\n  claude:\n    variants:\n      v:\n        args:\n", "args"},
		{"variants under default", "version: 1\nname: default\nplatforms:\n  claude:\n    variants:\n      v:\n        args: -p\n", "not accepted under"},
		{"install is not a list", "version: 1\nname: x\nplatforms:\n  claude:\n    install: one command\n", "list of shell commands"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse("t.yaml", []byte(tc.src))
			if err == nil {
				t.Fatalf("Parse(%q) = nil error, want one naming %q", tc.src, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to name %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "t.yaml") {
				t.Errorf("error = %q, want it to name the file", err)
			}
		})
	}
}
