package hydrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ackstorm/agent-profile/pkg/schema"
	"github.com/ackstorm/agent-profile/pkg/source"
)

// applySkill installs a two-file skill into root and returns the root.
func applySkill(t *testing.T, root, name string) {
	t.Helper()
	src := writeTree(t, t.TempDir(), map[string]string{
		"SKILL.md":           "# " + name,
		"scripts/convert.py": "print()",
	})
	_, err := Apply(t.Context(), Plan{
		Root: root, Adapter: claudeAdapter(t), Now: fixedNow,
		Profile: schema.Profile{Skills: map[string]schema.Resource{name: {Enabled: true}}},
		Fetched: map[string]source.Resolved{"skill " + name: {Dir: src}},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
}

// The exit criterion, and the reason the ledger records a hash at all.
func TestUninstallNeverRemovesAUserEditedFile(t *testing.T) {
	root := t.TempDir()
	applySkill(t, root, "pdf")

	edited := filepath.Join(root, "skills", "pdf", "scripts", "convert.py")
	if err := os.WriteFile(edited, []byte("print('mine')"), 0o600); err != nil {
		t.Fatal(err)
	}

	rm, err := Remove(root, "skill", "pdf", false)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(edited); err != nil {
		t.Fatalf("the edited file was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "pdf", "SKILL.md")); !os.IsNotExist(err) {
		t.Error("the unchanged file survived; it should have been removed")
	}

	var skipped int
	for _, v := range rm.Verdicts {
		if v.Op == "skip" {
			skipped++
			if v.Reason != "modified since install" {
				t.Errorf("reason = %q", v.Reason)
			}
		}
	}
	if skipped != 1 {
		t.Errorf("skipped %d file(s), want exactly the edited one: %+v", skipped, rm.Verdicts)
	}

	// The record goes even though a file stayed. A skipped file is the user's,
	// and continuing to claim it would let a later export speak for it.
	l, _ := LoadLedger(root)
	if _, ok := l.Resource("skill", "pdf"); ok {
		t.Error("the record survived the removal")
	}
}

// The exit criterion for merged files, and the case a hash gate would break.
//
// "memory" is installed FIRST, so the second install invalidates its recorded
// hash on disk. Gating a merged record on that hash would refuse this removal —
// which is why classifyFile does not.
func TestUninstallingOneMCPServerLeavesEveryOtherKeyIntact(t *testing.T) {
	root := t.TempDir()
	_, err := Apply(t.Context(), Plan{
		Root: root, Adapter: claudeAdapter(t), Now: fixedNow,
		Profile: schema.Profile{
			Inputs: schema.Inputs{Secrets: map[string]schema.Binding{"memory-token": {Env: "MEMORY_TOKEN"}}},
			MCPs: map[string]schema.MCP{
				"memory":     specMemoryServer(),
				"filesystem": specFilesystemServer(),
			},
		},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	// A server the user added by hand, in the same container.
	path := filepath.Join(root, ".claude.json")
	doc := map[string]any{}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["mcpServers"].(map[string]any)["mine"] = map[string]any{"command": "mine"}
	doc["numStartups"] = 41.0
	out, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Remove(root, "mcp", "memory", false); err != nil {
		t.Fatalf("remove: %v", err)
	}

	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	servers, ok := got["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("the container was removed with its entry: %v", got)
	}
	if _, gone := servers["memory"]; gone {
		t.Error("memory survived the removal")
	}
	for _, keep := range []string{"filesystem", "mine"} {
		if _, ok := servers[keep]; !ok {
			t.Errorf("%q was removed and it is not ours", keep)
		}
	}
	if got["numStartups"] != 41.0 {
		t.Errorf("an unrelated top-level key was lost: %v", got["numStartups"])
	}
}

// §33.3: the preview MUST be produced by the same classifier as the action.
// Here that is structural — one entry point, one call to classify — and this
// asserts the property the structure exists to guarantee.
func TestTheDryRunPreviewMatchesWhatRemovalDoes(t *testing.T) {
	root := t.TempDir()
	applySkill(t, root, "pdf")

	preview, err := Remove(root, "skill", "pdf", true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "pdf", "SKILL.md")); err != nil {
		t.Fatalf("the dry run removed a file: %v", err)
	}
	if l, _ := LoadLedger(root); len(l.Resources) != 1 {
		t.Fatal("the dry run touched the ledger")
	}

	real, err := Remove(root, "skill", "pdf", false)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !reflect.DeepEqual(preview.Verdicts, real.Verdicts) {
		t.Errorf("preview %+v\naction   %+v", preview.Verdicts, real.Verdicts)
	}
	if !real.Removed() {
		t.Error("Removed() is false after removing two files")
	}
}

// It may not remove anything the ledger does not own (§33.3), and the refusal
// names what the ledger DOES hold — the filesystem cannot answer this question.
func TestRemoveRefusesWhatTheLedgerDoesNotOwn(t *testing.T) {
	root := t.TempDir()
	applySkill(t, root, "pdf")

	_, err := Remove(root, "skill", "xlsx", false)
	if err == nil {
		t.Fatal("removing an unowned resource succeeded")
	}
	if !contains(err.Error(), "pdf") {
		t.Errorf("the error does not name what is held: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "pdf", "SKILL.md")); err != nil {
		t.Errorf("a refused removal touched another resource: %v", err)
	}
}

// A skill is a tree; leaving its directories behind makes every listing show a
// resource that no longer exists.
func TestRemovingASkillPrunesItsEmptyDirectoriesButStopsAtASibling(t *testing.T) {
	root := t.TempDir()
	applySkill(t, root, "pdf")
	applySkill(t, root, "xlsx")

	if _, err := Remove(root, "skill", "pdf", false); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "pdf")); !os.IsNotExist(err) {
		t.Error("the skill's directory survived empty")
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "xlsx", "SKILL.md")); err != nil {
		t.Errorf("pruning walked past a sibling: %v", err)
	}
}

// MergeOut's refusal is MergeInto's: a file the user broke is not a file to
// rewrite.
func TestMergeOutRefusesAnUnparseableDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MergeOut(path, []string{"mcpServers.memory"}); err == nil {
		t.Fatal("MergeOut rewrote a document it could not parse")
	}
	body, _ := os.ReadFile(path)
	if string(body) != "{not json" {
		t.Errorf("the file was touched: %q", body)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
