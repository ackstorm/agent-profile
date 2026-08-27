package source

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/pkg/schema"
)

// profileFrom composes a manifest written inline and returns the effective
// profile for claude, plus the directory it was written to — which is the
// manifest directory a local source resolves against.
func profileFrom(t *testing.T, body string) (schema.Profile, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "p.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	p, _, err := schema.Effective(path, "claude")
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	return p, dir
}

func TestLocalResolvesRelativeToTheManifestAndRefusesEscapes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets", "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveLocal(dir, schema.LocalSource{Path: "./assets", Subpath: "review"})
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(dir, "assets", "review"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Dir != want {
		t.Errorf("Dir = %q, want %q", got.Dir, want)
	}
	// Local sources are never cached and never carry a resolved ref: they are
	// mutable development content by definition, and a receipt for bytes that
	// can change under you would be a lie (§32).
	if got.ResolvedRef != "" {
		t.Errorf("ResolvedRef = %q, want empty for a local source", got.ResolvedRef)
	}

	// A local source is not a licence to read the filesystem: a manifest
	// arrives from a cloned repository as often as from a hand-written file.
	for _, bad := range []string{"../..", "/etc", "assets/../.."} {
		if _, err := ResolveLocal(dir, schema.LocalSource{Path: bad}); err == nil {
			t.Errorf("path %q escaping the manifest directory was accepted", bad)
		}
	}
	// Including through a symlink, which a lexical check alone would miss.
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ResolveLocal(dir, schema.LocalSource{Path: "link"}); err == nil {
		t.Error("a symlink out of the manifest directory was followed")
	}
}

// §12: only active resources contribute requirements. A disabled resource
// whose source needs a token must not make that token required — and the way
// to prove that is that the secret is never READ, not that it merely resolves.
func TestResolveSkipsDisabledResourcesAndTheirSecrets(t *testing.T) {
	p, dir := profileFrom(t, `version: "1"
name: p
targets:
  - claude
  - opencode
inputs:
  secrets:
    gl:
      env: GL
skills:
  company-review:
    source:
      git:
        url: https://gitlab.acme.internal/a.git
        auth:
          value_from:
            secret: gl
runtimes:
  claude:
    skills:
      company-review:
        enabled: false
`)
	got, reports, err := Resolve(t.Context(), p, Opts{
		Cache: mustCache(t), ManifestDir: dir,
		Secret: func(string) (string, error) {
			t.Fatal("a disabled resource's secret was read")
			return "", nil
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("resolved %d entries, want 0: %v", len(got), got)
	}
	if len(reports) != 0 {
		t.Errorf("reports = %v, want none", reports)
	}
}

// §17.1's condition: inference is acceptable only because it is disclosed. The
// report exists on SUCCESS, not only in a 401 — an inference invisible when it
// works is undebuggable when it stops working.
func TestResolveReportsTheSchemeAndTheResolvedRef(t *testing.T) {
	repo := newBareRepo(t, map[string]string{"SKILL.md": "#"})
	p, dir := profileFrom(t, `version: "1"
name: p
targets:
  - claude
skills:
  s:
    source:
      git:
        url: `+repo+`
`)
	got, reports, err := Resolve(t.Context(), p, Opts{Cache: mustCache(t), ManifestDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["skill s"]; !ok {
		t.Fatalf("skill s was not resolved: %v", got)
	}
	if !slices.ContainsFunc(reports, func(r Report) bool {
		return r.Resource == "skill s" && strings.Contains(r.Text, got["skill s"].ResolvedRef)
	}) {
		t.Errorf("no report carrying the resolved ref for skill s: %+v", reports)
	}
}

// The referrer is what turns a ten-minute hunt into a two-minute fix, and
// §12's own worked example omits it.
func TestResolveNamesTheResourceWhenASecretIsMissing(t *testing.T) {
	p, dir := profileFrom(t, `version: "1"
name: p
targets:
  - claude
inputs:
  secrets:
    gl:
      env: GL_UNSET
skills:
  company-review:
    source:
      git:
        url: https://gitlab.acme.internal/a.git
        auth:
          value_from:
            secret: gl
`)
	_, _, err := Resolve(t.Context(), p, Opts{
		Cache: mustCache(t), ManifestDir: dir,
		Secret: func(string) (string, error) { return "", ErrSecretUnset },
	})
	if err == nil {
		t.Fatal("a missing secret resolved")
	}
	for _, want := range []string{"company-review", "gl"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// §22: a ref resolves against a catalogue declared in the SAME manifest, by a
// path join inside the tree that catalogue already fetched. No second hop
// exists, which is why §21.2 was removed rather than satisfied.
func TestARefResolvesAgainstItsCatalogue(t *testing.T) {
	repo := newBareRepo(t, map[string]string{"skills/pdf/SKILL.md": "# pdf"})
	p, dir := profileFrom(t, `version: "1"
name: p
targets:
  - claude
marketplaces:
  anthropic-skills:
    type: skills
    source:
      git:
        url: `+repo+`
        subpath: skills
skills:
  pdf:
    ref: pdf@anthropic-skills
`)
	got, _, err := Resolve(t.Context(), p, Opts{Cache: mustCache(t), ManifestDir: dir})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	pdf, ok := got["skill pdf"]
	if !ok {
		t.Fatalf("the ref did not resolve: %v", keysOf(got))
	}
	if b, err := os.ReadFile(filepath.Join(pdf.Dir, "SKILL.md")); err != nil || string(b) != "# pdf" {
		t.Errorf("item content = %q %v", b, err)
	}
	// One clone serves the catalogue and every item in it.
	if pdf.ResolvedRef != got["marketplace anthropic-skills"].ResolvedRef {
		t.Errorf("the item carries a different receipt from its catalogue")
	}
}

// §22's type matching. Without it a skill drawn from a plugin catalogue fails
// its SKILL.md contract with an error naming the wrong thing.
func TestARefMustMatchItsCatalogueType(t *testing.T) {
	repo := newBareRepo(t, map[string]string{"skills/pdf/SKILL.md": "#"})
	p, dir := profileFrom(t, `version: "1"
name: p
targets:
  - claude
marketplaces:
  acme:
    type: plugins
    source:
      git:
        url: `+repo+`
skills:
  pdf:
    ref: pdf@acme
`)
	_, _, err := Resolve(t.Context(), p, Opts{Cache: mustCache(t), ManifestDir: dir})
	if err == nil {
		t.Fatal("a skill resolved from a plugins catalogue")
	}
	for _, want := range []string{"acme", "plugins", "skills"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

// A ref naming a catalogue the manifest does not declare has to say so. v1
// keeps no marketplace definition in the ledger, so there is nowhere else it
// could have come from (§35.1).
func TestARefToAnUndeclaredCatalogueIsRefused(t *testing.T) {
	p, dir := profileFrom(t, `version: "1"
name: p
targets:
  - claude
skills:
  pdf:
    ref: pdf@nowhere
`)
	_, _, err := Resolve(t.Context(), p, Opts{Cache: mustCache(t), ManifestDir: dir})
	if err == nil || !strings.Contains(err.Error(), "nowhere") {
		t.Errorf("err = %v; it must name the missing catalogue", err)
	}
}

// Determinism is what makes a report diffable, and a report nobody can diff is
// a report nobody reads.
func TestResolveWalksInAStableOrder(t *testing.T) {
	repo := newBareRepo(t, map[string]string{"a/x": "1", "b/x": "2", "c/x": "3"})
	p, dir := profileFrom(t, `version: "1"
name: p
targets:
  - claude
skills:
  charlie:
    source:
      git:
        url: `+repo+`
        subpath: c
  alpha:
    source:
      git:
        url: `+repo+`
        subpath: a
  bravo:
    source:
      git:
        url: `+repo+`
        subpath: b
`)
	var first []string
	for range 3 {
		_, reports, err := Resolve(t.Context(), p, Opts{Cache: mustCache(t), ManifestDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(reports))
		for _, r := range reports {
			names = append(names, r.Resource)
		}
		if first == nil {
			first = names
			continue
		}
		if !slices.Equal(first, names) {
			t.Fatalf("report order differs between runs:\n%v\n%v", first, names)
		}
	}
	if want := []string{"skill alpha", "skill bravo", "skill charlie"}; !slices.Equal(first, want) {
		t.Errorf("order = %v, want %v", first, want)
	}
}

// v1 catalogues are same-repo only, which is how the real ones are built: one
// clone, N plugins. An external entry is refused by NAME — and refusing it is
// what removed §21.2 from the spec, since with no cross-repo hop there is no
// foreign host for a credential to reach.
func TestAForeignMarketplaceEntryIsRefusedByName(t *testing.T) {
	err := ErrForeignEntry{
		Marketplace: "acme-plugins", Entry: "code-review",
		EntryURL: "https://github.com/attacker/evil.git",
	}
	for _, want := range []string{"acme-plugins", "code-review", "github.com/attacker/evil.git", "same-repo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %q", err.Error(), want)
		}
	}
}
