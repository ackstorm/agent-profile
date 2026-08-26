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

func TestMergeReplacesAUnionWholesaleWhenTheBranchChanges(t *testing.T) {
	// §3.5.1: git → local replaces the node. Branches never coexist.
	got := mergeYAML(t,
		"skills:\n  pdf:\n    source:\n      git:\n        url: https://example.com/r.git\n        ref: main\n",
		"skills:\n  pdf:\n    source:\n      local:\n        path: ./repo\n")
	src := got.Map["skills"].Map["pdf"].Map["source"]
	if _, ok := src.Map["git"]; ok {
		t.Error("git branch survived a switch to local; branches never coexist")
	}
	if src.Map["local"].Map["path"].Str != "./repo" {
		t.Error("local branch missing")
	}

	// Same branch composes: the base's url survives the child's ref override.
	got = mergeYAML(t,
		"skills:\n  pdf:\n    source:\n      git:\n        url: https://example.com/r.git\n        ref: main\n",
		"skills:\n  pdf:\n    source:\n      git:\n        ref: v2\n")
	git := got.Map["skills"].Map["pdf"].Map["source"].Map["git"]
	if git.Map["url"].Str != "https://example.com/r.git" {
		t.Error("url lost on a same-branch override")
	}
	if git.Map["ref"].Str != "v2" {
		t.Errorf("ref = %q, want v2", git.Map["ref"].Str)
	}
}

func TestMergeDiscardsTheOtherLocatorWhenAnOverlayPicksOne(t *testing.T) {
	// §3.5.2's worked example. Without this the resource carries ref AND
	// source and fails locator validation, so a marketplace-backed skill could
	// never be overridden with a direct source.
	got := mergeYAML(t,
		"skills:\n  pdf:\n    ref: pdf@anthropic-skills\n",
		"skills:\n  pdf:\n    source:\n      git:\n        url: https://example.com/fork.git\n")
	pdf := got.Map["skills"].Map["pdf"]
	if _, ok := pdf.Map["ref"]; ok {
		t.Error("inherited ref survived an overlay declaring source")
	}
	if _, ok := pdf.Map["source"]; !ok {
		t.Fatal("source missing")
	}

	// Keys OUTSIDE the group compose normally: `enabled: false` must not
	// disturb the locator, and a replaced prompt content inherits mode.
	got = mergeYAML(t,
		"skills:\n  pdf:\n    ref: pdf@anthropic-skills\n",
		"skills:\n  pdf:\n    enabled: false\n")
	if _, ok := got.Map["skills"].Map["pdf"].Map["ref"]; !ok {
		t.Error("`enabled: false` discarded the locator; it is outside the group")
	}
	got = mergeYAML(t,
		"prompt:\n  mode: replace\n  source:\n    local:\n      path: ./p.md\n",
		"prompt:\n  content: |\n    inline\n")
	p := got.Map["prompt"]
	if _, ok := p.Map["source"]; ok {
		t.Error("prompt source survived an overlay declaring content")
	}
	if p.Map["mode"].Str != "replace" {
		t.Error("mode lost; it is outside the content|source group")
	}
}
