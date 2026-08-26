package hydrate

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// KnownComponentKinds is the plugin component vocabulary: the top-level entries
// of a plugin tree that SOME adapter here knows how to route.
//
// It gates the drop warning. A tree carrying one of these that the active
// adapter has no destination for is REPORTED as dropped, so the user learns
// "this runtime does not support hooks" (§8, §24.1). Anything not in this set —
// `.claude-plugin`, `README.md`, `LICENSE`, an unrecognised directory — is
// non-content by design and skipped silently.
//
// INVARIANT: every rule's source kind across all four tables appears here.
// TestEveryRuleKindIsKnown enforces it, which is what stops a new rule from
// silently disabling the drop warning for its own kind.
var KnownComponentKinds = map[string]bool{
	"rules":     true,
	"commands":  true,
	"agents":    true,
	"skills":    true,
	"prompts":   true,
	"mcp":       true,
	".mcp.json": true,
	"AGENTS.md": true,
	"hooks":     true,
}

// Rule routes one plugin component kind to one destination.
//
// Kind is the tree's top-level entry. To is the destination relative to the
// ROOT — the agent's configuration directory — not to a project. ach's tables
// are written in project terms (`.claude/skills/**`), and the config-dir form
// is that with the runtime's own dot-prefix stripped, exactly as ach's
// RemapGlobalPath does at run time.
//
// Transform names a format conversion this implementation does not do yet. A
// rule carrying one routes NOTHING and reports why: a half-correct TOML
// conversion is worse than an honest warning, because the runtime loads it and
// fails somewhere else.
type Rule struct {
	Kind      string
	To        string
	Merge     string // "" = replace; "deep" for structured configuration
	Transform string
}

// routeTables is the per-runtime routing, ported from ach's adapter tables and
// re-expressed against a configuration-directory root.
//
// Nothing here generalises, and the gaps are as load-bearing as the rows:
//
//   - codex has NO skills row. ach routes skills to `.agents/skills/`, which is
//     outside CODEX_HOME, so pointing that variable at a profile does not
//     isolate them — writing there would leak one profile's skills into every
//     other. Same fact agentreg.Agent.Skills records by being empty.
//   - claude's AGENTS.md becomes CLAUDE.md, and it is a COMPOSITE merge: the
//     file belongs to the user, and a plugin contributes a marked region rather
//     than replacing it.
//   - opencode and codex need format conversions their rows name.
var routeTables = map[string][]Rule{
	"claude": {
		{Kind: "rules", To: "rules"},
		{Kind: "commands", To: "commands"},
		{Kind: "agents", To: "agents"},
		{Kind: "skills", To: "skills"},
		{Kind: "AGENTS.md", To: "CLAUDE.md", Merge: "composite"},
		{Kind: "mcp", To: ".claude.json", Merge: "deep"},
		{Kind: ".mcp.json", To: ".claude.json", Merge: "deep"},
	},
	"codex": {
		{Kind: "commands", To: "prompts"},
		{Kind: "agents", To: "agents", Transform: "markdown frontmatter to TOML"},
		{Kind: "mcp", To: "config.toml", Merge: "deep"},
		{Kind: ".mcp.json", To: "config.toml", Merge: "deep"},
	},
	"opencode": {
		{Kind: "commands", To: "commands", Transform: "command frontmatter allowlist"},
		{Kind: "agents", To: "agents", Transform: "agent tools and colour rewriting"},
		{Kind: "skills", To: "skills"},
		{Kind: "mcp", To: "opencode.json", Merge: "deep"},
		{Kind: ".mcp.json", To: "opencode.json", Merge: "deep"},
	},
	"pi": {
		{Kind: "commands", To: "prompts"},
		{Kind: "skills", To: "skills"},
		{Kind: "mcp", To: "mcp.json", Merge: "deep"},
		{Kind: ".mcp.json", To: "mcp.json", Merge: "deep"},
	},
}

// Routed is one component kind's destination, or the reason it has none.
type Routed struct {
	Kind, To, Merge string
	// Dropped is set when the kind is KNOWN and this runtime has no
	// destination, or has one this implementation cannot produce yet. Either
	// way it is reported, never silently skipped.
	Dropped string
}

// Route decides what happens to every top-level entry of a plugin tree.
//
// Entries are returned sorted, so a report is diffable, and an unknown entry is
// simply absent: §24.1 says a manifest or a README is skipped silently, and it
// is the KNOWN kind with no destination that has to be reported.
func Route(runtime string, entries []string) ([]Routed, error) {
	table, ok := routeTables[runtime]
	if !ok {
		return nil, fmt.Errorf("runtime %q has no plugin routing", runtime)
	}
	byKind := map[string]Rule{}
	for _, r := range table {
		byKind[r.Kind] = r
	}

	sorted := append([]string(nil), entries...)
	sort.Strings(sorted)

	var out []Routed
	for _, e := range sorted {
		kind := path.Clean(e)
		if !KnownComponentKinds[kind] {
			continue
		}
		r, routed := byKind[kind]
		switch {
		case !routed:
			out = append(out, Routed{Kind: kind, Dropped: fmt.Sprintf(
				"runtime %q has no destination for %q", runtime, kind)})
		case r.Transform != "":
			out = append(out, Routed{Kind: kind, Dropped: fmt.Sprintf(
				"routing %q to %q needs a %s, which is not implemented; the files were not written",
				kind, runtime, r.Transform)})
		default:
			out = append(out, Routed{Kind: kind, To: r.To, Merge: r.Merge})
		}
	}
	return out, nil
}

// AgentFrontmatterMCPKeys are the frontmatter keys through which a plugin could
// otherwise register an MCP server.
//
// §24.2, and it is normative because it is a security property rather than
// formatting: a plugin that can register an MCP server by shipping a
// frontmatter key adds a tool the user never reviewed. Every adapter strips
// these; ach's codex adapter carries the same rule with the same reason.
var AgentFrontmatterMCPKeys = []string{"mcp_servers", "mcpServers", "mcp"}

// StripAgentMCP removes those keys from an agent's frontmatter mapping and
// reports what it removed, because §8 forbids doing it in silence.
func StripAgentMCP(frontmatter map[string]any) []string {
	var removed []string
	for _, k := range AgentFrontmatterMCPKeys {
		if _, ok := frontmatter[k]; ok {
			delete(frontmatter, k)
			removed = append(removed, k)
		}
	}
	sort.Strings(removed)
	return removed
}

// pluginRootFiles are the entries a plugin may carry that are not components.
// Named only so the reason they are skipped is written down: they describe the
// plugin, they are not part of it.
var pluginRootFiles = map[string]bool{
	".claude-plugin": true, "README.md": true, "LICENSE": true, "LICENSE.md": true,
}

// IsNonContent reports whether an entry is skipped silently by design.
func IsNonContent(entry string) bool {
	e := path.Clean(entry)
	return pluginRootFiles[e] || (!KnownComponentKinds[e] && !strings.HasPrefix(e, "."))
}
