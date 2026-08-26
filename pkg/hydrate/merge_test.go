package hydrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/BurntSushi/toml"
)

// The three assertions in this test ARE the task.
func TestMergeIsAdditiveReportsItsKeysAndIsIdempotent(t *testing.T) {
	for _, tc := range []struct{ name, file string }{
		{"json", "config.json"},
		{"toml", "config.toml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.file)
			isTOML := tc.name == "toml"

			// A document the user already had, with a server of their own.
			seed := map[string]any{
				"mcpServers": map[string]any{
					"mine": map[string]any{"command": "my-server"},
				},
				"theme": "dark",
			}
			writeDoc(t, path, seed, isTOML)

			contribution := map[string]any{
				"mcpServers": map[string]any{
					"memory": map[string]any{"url": "https://memory.company.com/mcp"},
				},
			}
			keys, err := MergeInto(path, contribution)
			if err != nil {
				t.Fatal(err)
			}

			// 1. The user's key survives. This is Phase 4's hand-added FILE
			//    assertion one level down, and it is the same property.
			got := readBack(t, path, isTOML)
			servers, _ := got["mcpServers"].(map[string]any)
			if _, ok := servers["mine"]; !ok {
				t.Errorf("a hand-added server was lost: %+v", got)
			}
			if got["theme"] != "dark" {
				t.Errorf("a sibling key was lost: %+v", got)
			}
			if _, ok := servers["memory"]; !ok {
				t.Errorf("the contribution did not land: %+v", got)
			}

			// 2. The reported keys are exactly what was written, dotted.
			//    Phase 7 removes precisely these and nothing else.
			if want := []string{"mcpServers.memory"}; !slices.Equal(keys, want) {
				t.Errorf("keys = %v, want %v", keys, want)
			}

			// 3. Merging the same contribution twice is byte-identical, or
			//    every apply shows a spurious diff in the user's version
			//    control.
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := MergeInto(path, contribution); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Errorf("a second identical merge changed the bytes:\n%s\n---\n%s", before, after)
			}
		})
	}
}

// A file that does not parse is an ERROR, never a file to overwrite. The user
// broke it, or it was never ours; replacing it would destroy work this program
// did not create and cannot restore.
func TestAnUnparseableDocumentIsRefusedNotOverwritten(t *testing.T) {
	for _, name := range []string{"config.json", "config.toml"} {
		path := filepath.Join(t.TempDir(), name)
		const broken = "this is not { valid ]"
		if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := MergeInto(path, map[string]any{"a": map[string]any{"b": 1}}); err == nil {
			t.Errorf("%s: a broken document was merged into", name)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != broken {
			t.Errorf("%s: the broken document was overwritten:\n%s", name, body)
		}
	}
}

// An entry is replaced WHOLE, not merged field by field. Merging fields would
// leave a server's stale headers behind when a manifest drops them, and would
// make the recorded key ("mcpServers.memory.url") remove half a server on
// uninstall.
func TestAnEntryIsReplacedWholeAndRecordedAtEntryLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeDoc(t, path, map[string]any{"mcpServers": map[string]any{
		"memory": map[string]any{"url": "old", "headers": map[string]any{"X": "stale"}},
	}}, false)

	keys, err := MergeInto(path, map[string]any{"mcpServers": map[string]any{
		"memory": map[string]any{"url": "new"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := readBack(t, path, false)
	entry := got["mcpServers"].(map[string]any)["memory"].(map[string]any)
	if entry["url"] != "new" {
		t.Errorf("url = %v", entry["url"])
	}
	if _, stale := entry["headers"]; stale {
		t.Errorf("a dropped field survived: %+v", entry)
	}
	if want := []string{"mcpServers.memory"}; !slices.Equal(keys, want) {
		t.Errorf("keys = %v, want the ENTRY not its fields", keys)
	}
}

func TestMergeCreatesAnAbsentDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "c.json")
	keys, err := MergeInto(path, map[string]any{"mcpServers": map[string]any{"a": map[string]any{"url": "u"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Errorf("keys = %v", keys)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the document was not created: %v", err)
	}
}

func writeDoc(t *testing.T, path string, doc map[string]any, isTOML bool) {
	t.Helper()
	out, err := encodeDoc(doc, isTOML)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readBack(t *testing.T, path string, isTOML bool) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{}
	if isTOML {
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v\n%s", path, err, raw)
		}
		return doc
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, raw)
	}
	return doc
}
