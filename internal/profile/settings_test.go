//go:build unix

package profile

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// Naming a key takes its value whole, with its nesting, and takes nothing else.
// This is the whole feature: statusLine and theme out of a settings.json that
// also holds mcpServers, permissions and hooks.
func TestSliceJSONTakesTheNamedKeysAndNothingElse(t *testing.T) {
	src := []byte(`{
	  "theme": "dark-ansi",
	  "statusLine": {"type": "command", "command": "bash /x/statusline.sh"},
	  "permissions": {"allow": ["Bash"]},
	  "mcpServers": {"linear": {"url": "https://l"}, "other": {"url": "https://o"}}
	}`)

	out, found, err := sliceSettings(agent.JSON, src, []string{"statusLine", "theme"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(found, " ") != "statusLine theme" {
		t.Errorf("found = %q, want the keys that were asked for", found)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("the slice is not valid JSON: %v\n%s", err, out)
	}
	if len(got) != 2 {
		t.Errorf("slice holds %d keys, want 2: %s", len(got), out)
	}
	if got["theme"] != "dark-ansi" {
		t.Errorf("theme = %v, want dark-ansi", got["theme"])
	}
	sl, ok := got["statusLine"].(map[string]any)
	if !ok || sl["command"] != "bash /x/statusline.sh" {
		t.Errorf("statusLine did not survive with its nesting: %v", got["statusLine"])
	}
}

// A dotted path names a branch, and two paths sharing a parent land in the same
// object rather than the second overwriting the first.
func TestSliceJSONMergesTwoPathsSharingAParent(t *testing.T) {
	src := []byte(`{"mcpServers": {"linear": {"url": "l"}, "gh": {"url": "g"}, "no": {"url": "n"}}}`)

	out, found, err := sliceSettings(agent.JSON, src, []string{"mcpServers.linear", "mcpServers.gh"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("found = %q, want both paths", found)
	}
	var got struct {
		MCP map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.MCP) != 2 || got.MCP["linear"] == nil || got.MCP["gh"] == nil {
		t.Errorf("mcpServers = %v, want exactly linear and gh", got.MCP)
	}
}

// Naming a parent and one of its own children is not a mistake a caller can
// make by picking the wrong order: the parent already contains the child, so
// the parent's full value must survive whichever position it was given in, and
// both names are reported found.
func TestSliceJSONParentDominatesRegardlessOfOrder(t *testing.T) {
	src := []byte(`{"mcpServers": {"linear": {"url": "l"}, "gh": {"url": "g"}, "no": {"url": "n"}}}`)

	for _, keys := range [][]string{
		{"mcpServers", "mcpServers.linear"},
		{"mcpServers.linear", "mcpServers"},
	} {
		out, found, err := sliceSettings(agent.JSON, src, keys)
		if err != nil {
			t.Fatalf("keys=%v: %v", keys, err)
		}
		if len(found) != 2 {
			t.Errorf("keys=%v: found = %q, want both names", keys, found)
		}
		var got struct {
			MCP map[string]any `json:"mcpServers"`
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("keys=%v: %v", keys, err)
		}
		if len(got.MCP) != 3 {
			t.Errorf("keys=%v: mcpServers = %v, want the whole parent (3 servers), "+
				"not the child-only map a naive overwrite would leave", keys, got.MCP)
		}
	}
}

// A key that is not there is reported back to the caller, which warns. Never an
// error: a typo must not fail a create that otherwise worked. A key holding a
// literal dot is unreachable for the same reason and reported the same way —
// paths split on ".", and that is a stated limit, not a bug to work around.
func TestSliceJSONReportsWhatItCouldNotFind(t *testing.T) {
	src := []byte(`{"theme": "dark", "a.b": 1, "a": {"c": 2}}`)

	out, found, err := sliceSettings(agent.JSON, src, []string{"themey", "a.b", "a.c"})
	if err != nil {
		t.Fatalf("a missing key must not be an error: %v", err)
	}
	// "a.b" resolves as a -> b, which does not exist; the literal "a.b" key is
	// unreachable. "a.c" does resolve.
	if strings.Join(found, " ") != "a.c" {
		t.Errorf("found = %q, want only a.c", found)
	}
	if !strings.Contains(string(out), `"c"`) {
		t.Errorf("out = %s, want the one path that resolved", out)
	}
}

// Nothing found means no file at all, rather than an empty object the agent
// would then read as "these settings are deliberately blank".
func TestSliceJSONFindingNothingProducesNoFile(t *testing.T) {
	out, found, err := sliceSettings(agent.JSON, []byte(`{"theme":"dark"}`), []string{"nope"})
	if err != nil || out != nil || found != nil {
		t.Errorf("sliceSettings = (%s, %q, %v), want (nil, nil, nil)", out, found, err)
	}
}

func TestSliceJSONRejectsAnUnreadableFile(t *testing.T) {
	if _, _, err := sliceSettings(agent.JSON, []byte("not json"), []string{"theme"}); err == nil {
		t.Error("sliceSettings accepted a file that is not JSON")
	}
}

const tomlFixture = `# codex config
model = "gpt-5"
approval_policy = "on-request"

[tui]
notifications = true

# a comment that belongs to tui, because a block starts at its header
[tui.model_availability_nux]
seen = true

[mcp_servers.codemem]
command = "codemem"

[mcp_servers.other]
command = "other"

[[hooks.state]]
run = "one"

[[hooks.state]]
run = "two"

[projects."/tmp/a]b"]
trusted = true
`

// Naming a table takes its sub-tables too: that is what someone means by "tui".
// Siblings are not taken, and the comment above a header stays with the block
// before it — a block starts at its header, and attaching leading comments to
// the following section would be a guess about intent.
func TestSliceTOMLTakesATableAndItsChildren(t *testing.T) {
	out, found, err := sliceSettings(agent.TOML, []byte(tomlFixture), []string{"tui"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(found, " ") != "tui" {
		t.Errorf("found = %q, want tui", found)
	}
	s := string(out)
	for _, want := range []string{"[tui]", "notifications = true", "[tui.model_availability_nux]", "seen = true"} {
		if !strings.Contains(s, want) {
			t.Errorf("slice is missing %q:\n%s", want, s)
		}
	}
	for _, unwanted := range []string{"[mcp_servers", "[[hooks.state]]", "approval_policy"} {
		if strings.Contains(s, unwanted) {
			t.Errorf("slice carried %q, which nobody asked for:\n%s", unwanted, s)
		}
	}
}

// A sub-table is nameable on its own, and naming one does not bring its
// siblings: mcp_servers.codemem is one server, not every server.
func TestSliceTOMLLeavesSiblingTables(t *testing.T) {
	out, _, err := sliceSettings(agent.TOML, []byte(tomlFixture), []string{"mcp_servers.codemem"})
	if err != nil {
		t.Fatal(err)
	}
	if s := string(out); !strings.Contains(s, `command = "codemem"`) || strings.Contains(s, `command = "other"`) {
		t.Errorf("slice = %s, want codemem alone", s)
	}
}

// The same key-order independence JSON has: naming a table and one of its own
// sub-tables, in either order, marks both names found and emits the block
// once rather than duplicating it or losing the more specific name because it
// lost a first-match race.
func TestSliceTOMLParentDominatesRegardlessOfOrder(t *testing.T) {
	for _, keys := range [][]string{
		{"tui", "tui.model_availability_nux"},
		{"tui.model_availability_nux", "tui"},
	} {
		out, found, err := sliceSettings(agent.TOML, []byte(tomlFixture), keys)
		if err != nil {
			t.Fatalf("keys=%v: %v", keys, err)
		}
		if len(found) != 2 {
			t.Errorf("keys=%v: found = %q, want both names", keys, found)
		}
		s := string(out)
		if strings.Count(s, "[tui]") != 1 || strings.Count(s, "[tui.model_availability_nux]") != 1 {
			t.Errorf("keys=%v: slice = %s, want each block exactly once", keys, s)
		}
	}
}

// A top-level scalar lives before the first header and is matched on the key
// part of its line.
func TestSliceTOMLMatchesATopLevelScalar(t *testing.T) {
	out, found, err := sliceSettings(agent.TOML, []byte(tomlFixture), []string{"model"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("found = %q, want model", found)
	}
	if s := string(out); !strings.Contains(s, `model = "gpt-5"`) || strings.Contains(s, "approval_policy") {
		t.Errorf("slice = %s, want the one scalar", s)
	}
}

// [[x]] is a header of x, so every block of an array of tables is emitted, in
// source order. Free, because the blocks are verbatim.
func TestSliceTOMLEmitsEveryArrayOfTablesBlock(t *testing.T) {
	out, _, err := sliceSettings(agent.TOML, []byte(tomlFixture), []string{"hooks.state"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Count(s, "[[hooks.state]]") != 2 || !strings.Contains(s, `run = "one"`) || !strings.Contains(s, `run = "two"`) {
		t.Errorf("slice = %s, want both blocks", s)
	}
	if strings.Index(s, `run = "one"`) > strings.Index(s, `run = "two"`) {
		t.Error("blocks were reordered; source order is the only order that is guaranteed valid")
	}
}

// The guarantee the whole design rests on, asserted as a property rather than
// argued: nothing is re-encoded, so every line that comes out went in. Stronger
// than a byte-subset check, and it is what makes "we do not parse TOML"
// acceptable — the only possible defect is cutting in the wrong place.
func TestSliceTOMLEmitsOnlySourceLines(t *testing.T) {
	src := map[string]bool{"": true}
	for _, ln := range strings.Split(tomlFixture, "\n") {
		src[ln] = true
	}
	out, _, err := sliceSettings(agent.TOML, []byte(tomlFixture),
		[]string{"model", "tui", "mcp_servers.codemem", "hooks.state", `projects."/tmp/a]b"`})
	if err != nil {
		t.Fatal(err)
	}
	for _, ln := range strings.Split(string(out), "\n") {
		if !src[ln] {
			t.Errorf("emitted a line that is not in the source: %q", ln)
		}
	}
}

// One of two ways to cut in the wrong place. A "[" at the start of a line
// inside a multiline string is indistinguishable from a table header without a
// parser, so the whole file is refused, and the message says why rather than
// failing vaguely.
func TestSliceTOMLRefusesAMultilineString(t *testing.T) {
	for _, q := range []string{`"""`, "'''"} {
		src := "[tui]\nbanner = " + q + "\n[not a header]\n" + q + "\n"
		_, _, err := sliceSettings(agent.TOML, []byte(src), []string{"tui"})
		if err == nil {
			t.Fatalf("%s was accepted; a header inside it cannot be told from a real one", q)
		}
		if !strings.Contains(err.Error(), "multiline") {
			t.Errorf("error = %v, want it to name the reason", err)
		}
	}
}

// The other way to cut in the wrong place. A multi-line array is valid, common
// TOML — nothing to do with the quoting ambiguity above — and a line inside one
// can equally start with "[": `matrix = [\n  [1, 2],\n]` would have this
// scanner read "[1, 2]," as a table header named "1, 2" and silently emit
// garbage instead of the requested table. Refused the same way, naming the
// reason.
func TestSliceTOMLRefusesAMultilineArray(t *testing.T) {
	src := "[tui]\n" +
		"matrix = [\n" +
		"  [1, 2],\n" +
		"]\n"
	_, _, err := sliceSettings(agent.TOML, []byte(src), []string{"tui"})
	if err == nil {
		t.Fatal("a multi-line array was accepted; a \"[\" inside it cannot be told from a header")
	}
	if !strings.Contains(err.Error(), "multiple lines") {
		t.Errorf("error = %v, want it to name the reason", err)
	}
}

// codex writes real filesystem paths into table names, so the closing bracket
// is found with a scanner that skips quoted spans. A plain IndexByte(']') cuts
// this name in the wrong place and it then matches nothing.
func TestTOMLHeaderSkipsQuotedBrackets(t *testing.T) {
	tests := []struct {
		line, want string
		ok         bool
	}{
		{`[projects."/tmp/a]b"]`, `projects."/tmp/a]b"`, true},
		{`  [ tui ]`, "tui", true},
		{`[[hooks.state]]`, "hooks.state", true},
		{`model = "x"`, "", false},
		{`# [tui]`, "", false},
		{`[unterminated`, "", false},
	}
	for _, tt := range tests {
		got, ok := tomlHeader(tt.line)
		if got != tt.want || ok != tt.ok {
			t.Errorf("tomlHeader(%q) = (%q, %v), want (%q, %v)", tt.line, got, ok, tt.want, tt.ok)
		}
	}
}
