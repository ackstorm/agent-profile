package hydrate

import (
	"slices"
	"strings"
	"testing"
)

// The invariant that keeps the drop warning honest: a rule for a kind not in
// KnownComponentKinds would silently disable the warning for that kind, because
// Route skips unknown entries before it ever looks for a rule.
func TestEveryRuleKindIsKnown(t *testing.T) {
	for runtime, table := range routeTables {
		for _, r := range table {
			if !KnownComponentKinds[r.Kind] {
				t.Errorf("%s routes %q, which is not in KnownComponentKinds — add it in the SAME change",
					runtime, r.Kind)
			}
		}
	}
}

// §8 and §24.1: a KNOWN kind with no destination is reported as dropped. An
// unknown entry is skipped silently, because a manifest or a README is not a
// component and reporting it would bury the signal.
func TestAKnownKindWithNoDestinationIsReportedAndAnUnknownOneIsNot(t *testing.T) {
	got, err := Route("pi", []string{
		"skills", "hooks", "rules", ".claude-plugin", "README.md", "LICENSE",
	})
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string]Routed{}
	for _, r := range got {
		byKind[r.Kind] = r
	}
	if _, reported := byKind["README.md"]; reported {
		t.Error("a README was reported; §24.1 skips non-content silently")
	}
	if _, reported := byKind[".claude-plugin"]; reported {
		t.Error("the plugin manifest was reported")
	}
	// hooks and rules are real kinds pi has no destination for. "Why is my
	// hook missing" has exactly one useful answer and this is it.
	for _, kind := range []string{"hooks", "rules"} {
		r, ok := byKind[kind]
		if !ok {
			t.Errorf("%q vanished with no report", kind)
			continue
		}
		if r.Dropped == "" {
			t.Errorf("%q was routed by pi: %+v", kind, r)
		}
		if !strings.Contains(r.Dropped, "pi") {
			t.Errorf("the drop for %q does not name the runtime: %q", kind, r.Dropped)
		}
	}
	if byKind["skills"].To != "skills" || byKind["skills"].Dropped != "" {
		t.Errorf("skills = %+v", byKind["skills"])
	}
}

// codex had NO skills row until codex-cli 0.149.1, because ach routes skills to
// .agents/skills, outside CODEX_HOME. It now reads $CODEX_HOME/skills as well,
// so a plugin's skills land in the profile like every other runtime's.
func TestCodexRoutesSkillsIntoItsConfigDir(t *testing.T) {
	got, err := Route("codex", []string{"skills"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].To != "skills" || got[0].Dropped != "" {
		t.Fatalf("codex skills routing = %+v", got)
	}
}

// A conversion this implementation does not do yet routes NOTHING and says
// why. A half-correct TOML conversion is worse than an honest warning: the
// runtime loads it and fails somewhere else, with an error naming neither the
// plugin nor ap.
func TestARuleNeedingAnUnimplementedTransformDropsAndExplains(t *testing.T) {
	got, err := Route("codex", []string{"agents"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].To != "" {
		t.Errorf("a transform-needing rule produced a destination: %+v", got[0])
	}
	for _, want := range []string{"TOML", "not implemented"} {
		if !strings.Contains(got[0].Dropped, want) {
			t.Errorf("drop %q lacks %q", got[0].Dropped, want)
		}
	}
}

// §24.2, normative because it is a security property and not formatting: a
// plugin that can register an MCP server by shipping a frontmatter key adds a
// tool the user never reviewed.
func TestAgentFrontmatterMCPKeysAreStrippedAndReported(t *testing.T) {
	fm := map[string]any{
		"name":        "reviewer",
		"mcp_servers": map[string]any{"evil": map[string]any{"command": "curl"}},
		"mcpServers":  map[string]any{"also-evil": map[string]any{}},
		"tools":       []any{"read"},
	}
	removed := StripAgentMCP(fm)
	for _, k := range AgentFrontmatterMCPKeys {
		if _, still := fm[k]; still {
			t.Errorf("%q survived: a plugin could register an MCP server the user never reviewed", k)
		}
	}
	if !slices.Equal(removed, []string{"mcpServers", "mcp_servers"}) {
		t.Errorf("removed = %v", removed)
	}
	// Everything else is untouched — this strips one thing, it does not
	// sanitise frontmatter in general.
	if fm["name"] != "reviewer" || len(fm["tools"].([]any)) != 1 {
		t.Errorf("frontmatter was otherwise altered: %+v", fm)
	}
	if got := StripAgentMCP(map[string]any{"name": "x"}); len(got) != 0 {
		t.Errorf("clean frontmatter reported %v", got)
	}
}

// Route's output is sorted, so a report is diffable between runs.
func TestRouteIsDeterministic(t *testing.T) {
	entries := []string{"skills", "commands", "agents", "rules", "hooks"}
	first, err := Route("claude", entries)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		got, _ := Route("claude", []string{"hooks", "agents", "rules", "skills", "commands"})
		if len(got) != len(first) {
			t.Fatalf("length differs: %d vs %d", len(got), len(first))
		}
		for i := range got {
			if got[i].Kind != first[i].Kind {
				t.Fatalf("order differs at %d: %q vs %q", i, got[i].Kind, first[i].Kind)
			}
		}
	}
}

// claude's AGENTS.md becomes CLAUDE.md, and it is a COMPOSITE merge: the file
// belongs to the user, so a plugin contributes a marked region rather than
// replacing what they wrote.
func TestClaudeRoutesAgentsMdIntoClaudeMdAsAComposite(t *testing.T) {
	got, err := Route("claude", []string{"AGENTS.md"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].To != "CLAUDE.md" || got[0].Merge != "composite" {
		t.Errorf("routed = %+v, want CLAUDE.md as a composite", got[0])
	}
}
