package hydrate

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/pkg/schema"
	"github.com/ackstorm/agent-profile/pkg/source"
)

// exportFixture is one root holding one of everything the ledger can record.
func exportFixture(t *testing.T) (root string, prof schema.Profile, fetched map[string]source.Resolved) {
	t.Helper()
	src := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "# pdf"})
	art := writeTree(t, t.TempDir(), map[string]string{"style.md": "house style"})

	prof = schema.Profile{
		Version: "1", Name: "plan", Targets: []string{"claude"},
		Inputs: schema.Inputs{Secrets: map[string]schema.Binding{
			"memory-token": {Env: "MEMORY_TOKEN"},
			"gitlab-token": {Env: "GITLAB_TOKEN"},
		}},
		Model: &schema.Model{
			Type: "openai", Model: "claude-opus-5",
			BaseURL: "https://llm.acme.com/v1",
			Auth:    &schema.Auth{Type: "bearer", ValueFrom: schema.ValueFrom{Secret: "gitlab-token"}},
		},
		Skills: map[string]schema.Resource{"pdf": {
			Enabled: true,
			Source: &schema.Source{Git: &schema.GitSource{
				URL: "https://gitlab.acme.com/team/skills.git", Ref: "main", Subpath: "pdf",
				Auth: &schema.GitAuth{ValueFrom: schema.ValueFrom{Secret: "gitlab-token"}},
			}},
		}},
		Artifacts: map[string]schema.Artifact{"style": {
			Enabled: true, Destination: "memory",
			Source: &schema.Source{Local: &schema.LocalSource{Path: art}},
		}},
		MCPs: map[string]schema.MCP{"memory": specMemoryServer()},
	}
	fetched = map[string]source.Resolved{
		"skill pdf":      {Dir: src, ResolvedRef: "8a71c2e"},
		"artifact style": {Dir: art},
	}
	root = t.TempDir()
	if _, err := Apply(t.Context(), Plan{
		Root: root, Adapter: claudeAdapter(t), Now: fixedNow, Profile: prof, Fetched: fetched,
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return root, prof, fetched
}

// The exit criterion: a root's ledger becomes a manifest that validates and
// re-applies to the same thing (§35.2).
func TestExportRoundTripsThroughTheParserAndReAppliesIdentically(t *testing.T) {
	root, _, fetched := exportFixture(t)

	exported, err := Export(root, "plan", []string{"claude"})
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	// It must PARSE. Rendering something the manifest reader rejects would
	// make export a formatter rather than a round trip.
	dir := t.TempDir()
	path := filepath.Join(dir, "exported.yaml")
	if err := os.WriteFile(path, schema.Render(exported), 0o600); err != nil {
		t.Fatal(err)
	}
	reparsed, warns, err := schema.Effective(path, "claude")
	if err != nil {
		t.Fatalf("the exported manifest does not parse:\n%s\n%v", schema.Render(exported), err)
	}
	for _, w := range warns {
		t.Logf("warning: %s", w.Text)
	}

	// Re-applying it lands the same bytes in a second root.
	root2 := t.TempDir()
	if _, err := Apply(t.Context(), Plan{
		Root: root2, Adapter: claudeAdapter(t), Now: fixedNow, Profile: reparsed, Fetched: fetched,
	}); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	for rel, sum := range hashTree(t, root) {
		if rel == ledgerName {
			continue
		}
		if got := hashTree(t, root2)[rel]; got != sum {
			a, _ := os.ReadFile(filepath.Join(root, rel))
			b, _ := os.ReadFile(filepath.Join(root2, rel))
			t.Errorf("%s differs after export and re-apply\n--- original\n%s--- reapplied\n%s", rel, a, b)
		}
	}

	// And a second export of an unchanged root is identical, or every export
	// is a diff nobody can read.
	again, err := Export(root, "plan", []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if string(schema.Render(again)) != string(schema.Render(exported)) {
		t.Error("two exports of one unchanged root differ")
	}
}

// §35.2: emit RESOLVED values where a value was inferred. The manifest declared
// no scheme; a reader on a host that does not look like GitLab would infer the
// other one and get a 401 whose cause is invisible.
func TestExportEmitsTheResolvedAuthScheme(t *testing.T) {
	root, _, _ := exportFixture(t)
	exported, err := Export(root, "plan", []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	got := exported.Skills["pdf"].Source.Git.Auth.Scheme
	if got != "basic-oauth2" {
		t.Errorf("scheme = %q, want the scheme this machine actually inferred for gitlab.acme.com", got)
	}
}

// §13.1: an export must synthesise inputs.secrets from recorded bindings, or
// the manifest references a secret nothing declares and fails its own
// validation. And §34: the binding NAME, never the value.
func TestExportSynthesisesOnlyTheBindingsItsResourcesReference(t *testing.T) {
	root, _, _ := exportFixture(t)
	exported, err := Export(root, "plan", []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"memory-token": "MEMORY_TOKEN", "gitlab-token": "GITLAB_TOKEN"}
	if len(exported.Inputs.Secrets) != len(want) {
		t.Fatalf("secrets = %+v, want %v", exported.Inputs.Secrets, want)
	}
	for name, env := range want {
		if exported.Inputs.Secrets[name].Env != env {
			t.Errorf("%s = %+v, want env %s", name, exported.Inputs.Secrets[name], env)
		}
	}

	// §34, structurally: what the ledger records for a credentialled header is
	// the REFERENCE, and there is no field beside it that could hold a value.
	// A literal Value here would mean a resolved secret had reached the ledger.
	l, err := LoadLedger(root)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := l.Resource("mcp", "memory")
	if !ok || rec.MCP == nil {
		t.Fatalf("the declaration was not recorded: %+v", rec)
	}
	auth := rec.MCP.Transport.Headers["Authorization"]
	if auth.Value != "" {
		t.Errorf("the ledger holds a literal Authorization value: %q", auth.Value)
	}
	if auth.ValueFrom == nil || auth.ValueFrom.Secret != "memory-token" {
		t.Errorf("the ledger lost the reference: %+v", auth.ValueFrom)
	}
}

// Two records binding one logical name two ways has no correct merge; whichever
// sorted last would win and the loser would authenticate with the wrong
// credential.
func TestExportRefusesTwoRecordsThatBindOneNameTwoWays(t *testing.T) {
	root, _, _ := exportFixture(t)

	l, err := LoadLedger(root)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := l.Resource("mcp", "memory")
	rec.Secrets = map[string]schema.Binding{"gitlab-token": {Env: "OTHER_TOKEN"}}
	l.Put(rec)
	if err := l.Save(root); err != nil {
		t.Fatal(err)
	}

	_, err = Export(root, "plan", []string{"claude"})
	if err == nil {
		t.Fatal("export unioned two disagreeing bindings")
	}
	for _, want := range []string{"gitlab-token", "GITLAB_TOKEN", "OTHER_TOKEN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
}

func hashTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
