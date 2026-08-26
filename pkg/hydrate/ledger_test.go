package hydrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ackstorm/agent-profile/pkg/schema"
)

// A root nothing has been applied to is the normal first case, not an error
// state. Making it an error would put a "does this exist yet" branch in every
// caller, and one of them would get it wrong.
func TestAnAbsentLedgerLoadsAsEmpty(t *testing.T) {
	l, err := LoadLedger(t.TempDir())
	if err != nil {
		t.Fatalf("an absent ledger errored: %v", err)
	}
	if len(l.Resources) != 0 || len(l.Definitions) != 0 {
		t.Errorf("an absent ledger is not empty: %+v", l)
	}
}

func TestLedgerRoundTripsBothArms(t *testing.T) {
	root := t.TempDir()
	want := &Ledger{
		Resources: []ResourceRec{{
			Name: "pdf", Kind: "skill", ResolvedRef: "8a71c2e", InstalledAt: "2026-08-26T10:00:00Z",
			Source: &schema.Source{Git: &schema.GitSource{URL: "https://e/r.git", Subpath: "skills/pdf"}},
			Files: []FileRec{
				{RelPath: "skills/pdf/SKILL.md", Hash: "abc"},
				{RelPath: "settings.json", Hash: "def", Merge: "deep", Keys: []string{"a.b", "a.c"}},
			},
		}},
		Definitions: []DefinitionRec{{
			Name: "acme-plugins", Kind: "marketplace", ResolvedRef: "3f9d10b",
			AuthScheme: "basic-oauth2", AuthBinding: "gitlab-token",
			Source: &schema.Source{Git: &schema.GitSource{URL: "https://gl/a.git"}},
		}},
	}
	if err := want.Save(root); err != nil {
		t.Fatal(err)
	}
	got, err := LoadLedger(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != ledgerVersion {
		t.Errorf("version = %d", got.Version)
	}
	if len(got.Resources) != 1 || len(got.Definitions) != 1 {
		t.Fatalf("arms lost: %+v", got)
	}
	r := got.Resources[0]
	if r.Name != "pdf" || r.ResolvedRef != "8a71c2e" || len(r.Files) != 2 {
		t.Errorf("resource = %+v", r)
	}
	// Merge and Keys are what make removal safe in a co-owned file. Losing
	// them in a round trip would mean uninstall removes the whole file.
	if r.Files[1].Merge != "deep" || len(r.Files[1].Keys) != 2 {
		t.Errorf("merge metadata lost: %+v", r.Files[1])
	}
	// The second arm exists so a marketplace survives the manifest that
	// declared it (F3): a manifest is an INPUT and is not kept.
	d := got.Definitions[0]
	if d.Name != "acme-plugins" || d.AuthScheme != "basic-oauth2" || d.AuthBinding != "gitlab-token" {
		t.Errorf("definition = %+v", d)
	}
}

// §13 and §34: a binding's NAME is structure and may be recorded; its VALUE may
// not be. This asserts through the marshalled bytes rather than through the
// struct's fields, because a field added later would slip past a field check.
func TestNoSecretValueCanReachTheLedger(t *testing.T) {
	const secret = "glpat-DO-NOT-PERSIST-ME"
	root := t.TempDir()
	l := &Ledger{
		Definitions: []DefinitionRec{{
			Name: "acme-plugins", Kind: "marketplace",
			AuthScheme: "basic-oauth2", AuthBinding: "gitlab-token",
			Source: &schema.Source{Git: &schema.GitSource{
				URL:  "https://gl/a.git",
				Auth: &schema.GitAuth{Scheme: "basic-oauth2", ValueFrom: schema.ValueFrom{Secret: "gitlab-token"}},
			}},
		}},
	}
	if err := l.Save(root); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ledgerName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("a secret value reached the ledger:\n%s", raw)
	}
	// The binding NAME must survive, or export cannot synthesise an inputs
	// block from it (§35.2) and the exported manifest references a secret
	// nothing declares.
	if !strings.Contains(string(raw), "gitlab-token") {
		t.Errorf("the binding name was dropped:\n%s", raw)
	}
	// And the shape must have no field capable of carrying one.
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"token", "value", "credential", "password"} {
		if strings.Contains(strings.ToLower(string(raw)), `"`+forbidden+`"`) {
			t.Errorf("the ledger has a %q field; there must be nowhere to put a value", forbidden)
		}
	}
}

// A half-written ledger claims files that may not exist, and every later
// verdict would rest on a record that was never true. The write is atomic, so a
// concurrent reader sees the old ledger or the new one — never a truncation.
func TestLedgerSaveIsAtomicUnderAConcurrentReader(t *testing.T) {
	root := t.TempDir()
	small := &Ledger{Resources: []ResourceRec{{Name: "a", Kind: "skill", InstalledAt: "t"}}}
	if err := small.Save(root); err != nil {
		t.Fatal(err)
	}

	big := &Ledger{}
	for i := range 400 {
		big.Resources = append(big.Resources, ResourceRec{
			Name: strings.Repeat("n", 40) + string(rune('a'+i%26)), Kind: "skill", InstalledAt: "t",
			Files: []FileRec{{RelPath: strings.Repeat("p", 60), Hash: strings.Repeat("h", 64)}},
		})
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Every read must produce a parseable ledger. A torn write shows
			// up here as a JSON error, which is exactly what atomicity buys.
			if _, err := LoadLedger(root); err != nil {
				t.Errorf("a concurrent reader saw a torn ledger: %v", err)
				return
			}
		}
	}()

	for range 20 {
		if err := big.Save(root); err != nil {
			t.Errorf("save: %v", err)
			break
		}
		if err := small.Save(root); err != nil {
			t.Errorf("save: %v", err)
			break
		}
	}
	close(stop)
	wg.Wait()

	// No temporary file may survive a successful write.
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("a temporary ledger survived: %s", e.Name())
		}
	}
}

// Re-applying a resource replaces its record. Appending instead would grow a
// duplicate on every run, and a later removal would only find the first.
func TestPutReplacesRatherThanAppends(t *testing.T) {
	l := &Ledger{}
	l.Put(ResourceRec{Name: "pdf", Kind: "skill", ResolvedRef: "old"})
	l.Put(ResourceRec{Name: "pdf", Kind: "skill", ResolvedRef: "new"})
	l.Put(ResourceRec{Name: "pdf", Kind: "artifact", ResolvedRef: "other"})
	if len(l.Resources) != 2 {
		t.Fatalf("resources = %+v, want 2 (skill and artifact are different kinds)", l.Resources)
	}
	got, ok := l.Resource("skill", "pdf")
	if !ok || got.ResolvedRef != "new" {
		t.Errorf("resource = %+v, %v", got, ok)
	}
	l.PutDefinition(DefinitionRec{Name: "m", Kind: "marketplace", ResolvedRef: "1"})
	l.PutDefinition(DefinitionRec{Name: "m", Kind: "marketplace", ResolvedRef: "2"})
	if len(l.Definitions) != 1 || l.Definitions[0].ResolvedRef != "2" {
		t.Errorf("definitions = %+v", l.Definitions)
	}
}

// A ledger written by a newer ap must be refused, not silently half-read: the
// files it claims may use a shape this binary cannot reason about, and acting
// on a partial reading is how a removal touches the wrong thing.
func TestAFutureLedgerVersionIsRefused(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ledgerName),
		[]byte(`{"version":99,"resources":[],"definitions":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLedger(root); err == nil {
		t.Error("a newer ledger version was accepted")
	}
}
