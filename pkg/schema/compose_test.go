package schema

import (
	"slices"
	"testing"
)

// mergeYAML is the helper every composition test uses: parse two documents,
// merge them, render the result. Written once here so a case is three lines.
func mergeYAML(t *testing.T, base, overlay string) *Node {
	t.Helper()
	b, err := ParseYAML([]byte(base))
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	o, err := ParseYAML([]byte(overlay))
	if err != nil {
		t.Fatalf("overlay: %v", err)
	}
	return Merge(b, o, V1Schema())
}

func TestMergeRecursesIntoMappingsAndReplacesEverythingElse(t *testing.T) {
	// §3.3: a child overriding one parameter keeps its siblings.
	got := mergeYAML(t,
		"model:\n  type: anthropic\n  parameters:\n    effort: high\n    temperature: 0.2\n",
		"model:\n  parameters:\n    effort: xhigh\n")
	params := got.Map["model"].Map["parameters"]
	if params.Map["effort"].Str != "xhigh" {
		t.Errorf("effort = %q, want xhigh", params.Map["effort"].Str)
	}
	if params.Map["temperature"].Str != "0.2" {
		t.Errorf("temperature = %q, want 0.2 — a sibling was lost", params.Map["temperature"].Str)
	}
	if got.Map["model"].Map["type"].Str != "anthropic" {
		t.Error("model.type was lost")
	}

	// §3.4: lists replace wholesale. There is no implicit append.
	got = mergeYAML(t, "targets:\n  - claude\n  - codex\n", "targets:\n  - opencode\n")
	if len(got.Map["targets"].Seq) != 1 || got.Map["targets"].Seq[0].Str != "opencode" {
		t.Errorf("targets = %v, want exactly [opencode]", got.Map["targets"].Seq)
	}
}

func TestMergeTreatsNullAsResetToAbsent(t *testing.T) {
	// §3.7: `model: null` in a child yields an effective profile without model,
	// which is the subscription case — runtime-native defaults and credentials.
	got := mergeYAML(t, "model:\n  type: anthropic\n  model: claude-opus-5\n", "model: null\n")
	if _, ok := got.Map["model"]; ok {
		t.Error("model survived `model: null`; §3.7 makes it absent")
	}
	if slices.Contains(got.Keys, "model") {
		t.Error("model still listed in Keys; render would emit it")
	}
}
