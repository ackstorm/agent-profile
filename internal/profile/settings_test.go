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
