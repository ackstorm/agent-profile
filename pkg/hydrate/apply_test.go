package hydrate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/agent-profile/pkg/agentreg"
	"github.com/ackstorm/agent-profile/pkg/schema"
	"github.com/ackstorm/agent-profile/pkg/source"
)

func claudeAdapter(t *testing.T) Adapter {
	t.Helper()
	a, ok := agentreg.Lookup("claude")
	if !ok {
		t.Fatal("claude is not in the registry")
	}
	ad, err := AdapterFor(a)
	if err != nil {
		t.Fatal(err)
	}
	return ad
}

func writeTree(t *testing.T, root string, files map[string]string) string {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func fixedNow() time.Time { return time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC) }

// The five assertions in this test ARE Phase 4.
func TestApplyWritesRecordsAndLeavesHandAddedFilesAlone(t *testing.T) {
	src := writeTree(t, t.TempDir(), map[string]string{
		"SKILL.md":           "# pdf",
		"scripts/convert.py": "print()",
	})
	root := t.TempDir()

	// A file the user put there by hand, inside the very directory the skill
	// will be written into.
	handAdded := filepath.Join(root, "skills", "pdf", "notes.md")
	if err := os.MkdirAll(filepath.Dir(handAdded), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handAdded, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	// And one the apply will overwrite.
	if err := os.WriteFile(filepath.Join(root, "skills", "pdf", "SKILL.md"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := Plan{
		Root: root, Adapter: claudeAdapter(t), Now: fixedNow,
		Profile: schema.Profile{Skills: map[string]schema.Resource{
			"pdf": {Enabled: true, Source: &schema.Source{}},
		}},
		Fetched: map[string]source.Resolved{"skill pdf": {Dir: src, ResolvedRef: "8a71c2e"}},
	}
	res, err := Apply(t.Context(), p)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	// 1. The files landed.
	if b, err := os.ReadFile(filepath.Join(root, "skills", "pdf", "SKILL.md")); err != nil || string(b) != "# pdf" {
		t.Errorf("SKILL.md = %q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "skills", "pdf", "scripts", "convert.py")); err != nil || string(b) != "print()" {
		t.Errorf("convert.py = %q %v", b, err)
	}

	// 2. The hand-added file survived. Apply is additive (§33), and this is
	//    the assertion that proves it — the difference between apply and sync.
	if b, err := os.ReadFile(handAdded); err != nil || string(b) != "mine" {
		t.Errorf("a hand-added file did not survive: %q %v", b, err)
	}

	// 3. The overwrite was logged, the new file was not logged as one. §33
	//    requires it: a silent overwrite is the one thing an additive policy
	//    cannot afford.
	ops := map[string]string{}
	for _, c := range res.Changes {
		ops[filepath.ToSlash(c.Path)] = c.Op
	}
	if ops["skills/pdf/SKILL.md"] != "overwrite" {
		t.Errorf("SKILL.md op = %q, want overwrite: %+v", ops["skills/pdf/SKILL.md"], res.Changes)
	}
	if ops["skills/pdf/scripts/convert.py"] != "create" {
		t.Errorf("convert.py op = %q, want create", ops["skills/pdf/scripts/convert.py"])
	}
	if _, logged := ops["skills/pdf/notes.md"]; logged {
		t.Error("the hand-added file was reported as touched")
	}

	assertLedgerMatchesDisk(t, root)
}

// 4. The recorded hash equals the hash of what is on disk. §33.1's honesty
// property: every later verdict — remove, skip, report as modified — rests on
// this matching, so a ledger whose hashes drift from the files is worse than
// none.
func assertLedgerMatchesDisk(t *testing.T, root string) {
	t.Helper()
	l, err := LoadLedger(root)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := l.Resource("skill", "pdf")
	if !ok {
		t.Fatalf("the skill was not recorded: %+v", l)
	}
	if rec.ResolvedRef != "8a71c2e" || rec.InstalledAt != "2026-08-26T10:00:00Z" {
		t.Errorf("record = %+v", rec)
	}
	if len(rec.Files) != 2 {
		t.Fatalf("recorded %d files, want 2 (the hand-added one is not ours): %+v", len(rec.Files), rec.Files)
	}
	for _, f := range rec.Files {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.RelPath)))
		if err != nil {
			t.Fatalf("the ledger claims %s, which is not on disk: %v", f.RelPath, err)
		}
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != f.Hash {
			t.Errorf("%s: recorded hash %s, on-disk %s", f.RelPath, f.Hash, got)
		}
		if strings.Contains(f.RelPath, "notes.md") {
			t.Errorf("the ledger claims a file it did not write: %s", f.RelPath)
		}
	}
}

// 5. The ledger is written LAST (§37.2). A ledger claiming files that were
// never written is worse than no ledger: every later verdict would rest on a
// record that was never true.
func TestAFailedApplyLeavesNoLedger(t *testing.T) {
	root := t.TempDir()
	p := Plan{
		Root: root, Adapter: claudeAdapter(t), Now: fixedNow,
		Profile: schema.Profile{Skills: map[string]schema.Resource{
			"pdf": {Enabled: true, Source: &schema.Source{}},
		}},
		// A source directory that does not exist: materialization fails.
		Fetched: map[string]source.Resolved{"skill pdf": {Dir: filepath.Join(root, "nope")}},
	}
	if _, err := Apply(t.Context(), p); err == nil {
		t.Fatal("an apply with an unreadable source succeeded")
	}
	if _, err := os.Stat(filepath.Join(root, ledgerName)); !os.IsNotExist(err) {
		t.Errorf("a failed apply wrote a ledger: %v", err)
	}
}

// §8: an unsupported concept warns and is skipped, never dropped silently.
// codex has no skills destination inside its config directory, and "why is my
// skill missing" has exactly one useful answer.
func TestARuntimeWithNoSkillsDestinationWarnsRatherThanDropping(t *testing.T) {
	a, _ := agentreg.Lookup("codex")
	ad, _ := AdapterFor(a)
	src := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "#"})
	root := t.TempDir()

	res, err := Apply(t.Context(), Plan{
		Root: root, Adapter: ad, Now: fixedNow,
		Profile: schema.Profile{Skills: map[string]schema.Resource{"pdf": {Enabled: true, Source: &schema.Source{}}}},
		Fetched: map[string]source.Resolved{"skill pdf": {Dir: src}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("codex silently dropped a skill")
	}
	joined := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"codex", "pdf"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warning does not name %q: %s", want, joined)
		}
	}
	// And nothing was recorded, because nothing was written.
	l, _ := LoadLedger(root)
	if _, ok := l.Resource("skill", "pdf"); ok {
		t.Error("a skipped skill was recorded in the ledger")
	}
}

// §4, and v0.6.1 made the detection LEDGER-driven: a merged file's contributed
// keys are invisible to the filesystem, so only the ledger knows a disabled
// resource put something in a file that still looks untouched.
func TestADisabledResourceWithLedgerStateWarns(t *testing.T) {
	root := t.TempDir()
	seed := &Ledger{Resources: []ResourceRec{{
		Name: "company-review", Kind: "skill", InstalledAt: "t",
		Files: []FileRec{{RelPath: "settings.json", Hash: "h", Merge: "deep", Keys: []string{"a.b"}}},
	}}}
	if err := seed.Save(root); err != nil {
		t.Fatal(err)
	}
	res, err := Apply(t.Context(), Plan{
		Root: root, Adapter: claudeAdapter(t), Now: fixedNow,
		Profile: schema.Profile{Skills: map[string]schema.Resource{
			"company-review": {Enabled: false, Source: &schema.Source{}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "company-review") || !strings.Contains(joined, "disabled") {
		t.Errorf("no ledger-driven warning for a disabled-but-materialized skill: %v", res.Warnings)
	}
}

// A marketplace materializes no file, so a file-only ledger would lose it the
// moment the manifest is gone — and a manifest is an input that is not kept.
func TestAMarketplaceIsRecordedAsADefinition(t *testing.T) {
	root := t.TempDir()
	res, err := Apply(t.Context(), Plan{
		Root: root, Adapter: claudeAdapter(t), Now: fixedNow,
		Profile: schema.Profile{Marketplaces: map[string]schema.Marketplace{
			"acme-plugins": {Enabled: true, Type: "plugins", Source: &schema.Source{Git: &schema.GitSource{
				URL:  "https://gl.acme.internal/a.git",
				Auth: &schema.GitAuth{Scheme: "basic-oauth2", ValueFrom: schema.ValueFrom{Secret: "gitlab-token"}},
			}}},
		}},
		Fetched: map[string]source.Resolved{
			"marketplace acme-plugins": {ResolvedRef: "3f9d10b", SchemeUsed: "basic-oauth2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = res
	l, err := LoadLedger(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Definitions) != 1 {
		t.Fatalf("definitions = %+v", l.Definitions)
	}
	d := l.Definitions[0]
	if d.Name != "acme-plugins" || d.ResolvedRef != "3f9d10b" || d.AuthScheme != "basic-oauth2" {
		t.Errorf("definition = %+v", d)
	}
	// The binding NAME, never a value (§34).
	if d.AuthBinding != "gitlab-token" {
		t.Errorf("auth binding = %q, want the binding name", d.AuthBinding)
	}
}

// Re-applying replaces the record rather than appending, or the ledger grows a
// duplicate on every run and a later removal only finds the first.
func TestApplyTwiceIsIdempotentInTheLedger(t *testing.T) {
	src := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "#"})
	root := t.TempDir()
	p := Plan{
		Root: root, Adapter: claudeAdapter(t), Now: fixedNow,
		Profile: schema.Profile{Skills: map[string]schema.Resource{"pdf": {Enabled: true, Source: &schema.Source{}}}},
		Fetched: map[string]source.Resolved{"skill pdf": {Dir: src, ResolvedRef: "a"}},
	}
	if _, err := Apply(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	res, err := Apply(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := LoadLedger(root)
	if len(l.Resources) != 1 {
		t.Errorf("the ledger grew a duplicate: %+v", l.Resources)
	}
	// The second run overwrites what the first wrote, and says so.
	for _, c := range res.Changes {
		if c.Op != "overwrite" {
			t.Errorf("second apply reported %q for %s, want overwrite", c.Op, c.Path)
		}
	}
}
