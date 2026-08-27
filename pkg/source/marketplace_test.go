package source

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func catalogueDir(t *testing.T, entries string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, ".claude-plugin")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "marketplace.json"),
		[]byte(`{"plugins":[`+entries+`]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The same-repo case: a bare relative string, which is how the real catalogues
// are built. One clone serves every entry, and that is the whole reason there
// is no second hop to guard.
func TestASameRepoEntryResolvesByPathJoin(t *testing.T) {
	dir := catalogueDir(t, `{"name":"code-review","source":"./plugins/code-review"}`,
		map[string]string{"plugins/code-review/.claude-plugin/plugin.json": `{"name":"code-review"}`})

	got, err := ResolveItem("acme", "plugins", dir, "code-review")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(got, ".claude-plugin", "plugin.json")); err != nil {
		t.Errorf("the entry did not resolve to its directory: %v", err)
	}
}

// THE test of this phase. An entry naming another repository is a resolution
// ERROR, never a fetch.
//
// This is not caution about an unimplemented feature. Refusing it is what
// removes the cross-host second hop, and §21.2's entire guard with it. An
// implementation that "helpfully" fetched one would hand a marketplace owner
// every credential its users hold — and the manifest's author does not write
// the catalogue, so reviewing your own manifest could not protect you.
func TestAnEntryNamingAnotherRepositoryIsRefusedByName(t *testing.T) {
	for _, tc := range []struct{ name, entry, wantIn string }{
		{"git-subdir", `{"name":"evil","source":{"source":"git-subdir","url":"https://attacker.example/x.git","path":"p"}}`, "attacker.example"},
		{"url", `{"name":"evil","source":{"source":"url","url":"https://attacker.example/x.tgz"}}`, "attacker.example"},
		{"github", `{"name":"evil","source":{"source":"github","repo":"attacker/evil"}}`, "attacker/evil"},
		{"bare url string", `{"name":"evil","source":"https://attacker.example/x.git"}`, "attacker.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := catalogueDir(t, tc.entry, nil)
			_, err := ResolveItem("acme", "plugins", dir, "evil")
			if err == nil {
				t.Fatal("an external entry resolved")
			}
			for _, want := range []string{"acme", "evil", tc.wantIn, "same-repo"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q lacks %q", err, want)
				}
			}
		})
	}
}

// A relative path that climbs out of the catalogue's own tree is the same
// escape without a URL, and the same refusal.
func TestAnEntryEscapingTheCatalogueTreeIsRefused(t *testing.T) {
	dir := catalogueDir(t, `{"name":"evil","source":"../../etc"}`, nil)
	if _, err := ResolveItem("acme", "plugins", dir, "evil"); err == nil {
		t.Error("an entry climbing out of the catalogue resolved")
	}
}

// A catalogue is someone else's file. "No item x" without the list is a
// guessing game.
func TestAMissingItemListsWhatTheCatalogueHas(t *testing.T) {
	dir := catalogueDir(t, `{"name":"alpha","source":"./a"},{"name":"bravo","source":"./b"}`, nil)
	_, err := ResolveItem("acme", "plugins", dir, "charlie")
	if err == nil {
		t.Fatal("a missing item resolved")
	}
	for _, want := range []string{"charlie", "alpha", "bravo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

// §21.1's skills contract: the directory listing IS the catalogue, and there is
// no marketplace.json at all.
func TestASkillsCatalogueResolvesByDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pdf"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveItem("anthropic-skills", "skills", dir, "pdf")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "pdf" {
		t.Errorf("got %q", got)
	}
	if _, err := ResolveItem("anthropic-skills", "skills", dir, "nope"); err == nil {
		t.Error("a missing skill item resolved")
	}
	// And the item name cannot climb out.
	if _, err := ResolveItem("anthropic-skills", "skills", dir, "../.."); err == nil {
		t.Error("a skill item escaped the catalogue root")
	}
}

// Upstream grows fields — description, version, author, category, homepage,
// license. A catalogue that has them must keep working.
func TestUnknownCatalogueFieldsAreIgnored(t *testing.T) {
	dir := catalogueDir(t,
		`{"name":"x","source":"./x","description":"d","version":"1.2.3","author":{"name":"a"},"category":"c"}`,
		map[string]string{"x/.claude-plugin/plugin.json": "{}"})
	if _, err := ResolveItem("acme", "plugins", dir, "x"); err != nil {
		t.Errorf("a catalogue with extra fields was refused: %v", err)
	}
}

func TestParseRefGrammar(t *testing.T) {
	for _, tc := range []struct{ in, item, mkt string }{
		{"pdf@anthropic-skills", "pdf", "anthropic-skills"},
		// The LAST @ splits, so an item name may hold one.
		{"a@b@mkt", "a@b", "mkt"},
	} {
		item, mkt, err := ParseRef(tc.in)
		if err != nil || item != tc.item || mkt != tc.mkt {
			t.Errorf("ParseRef(%q) = %q,%q,%v", tc.in, item, mkt, err)
		}
	}
	for _, bad := range []string{"pdf", "@mkt", "pdf@", ""} {
		if _, _, err := ParseRef(bad); err == nil {
			t.Errorf("ParseRef(%q) was accepted", bad)
		}
	}
}

// describeForeign reads only the fields upstream uses to name a repository, so
// a catalogue growing a new one degrades to the raw JSON rather than to silence.
func TestAnUnrecognisedForeignShapeStillNamesSomething(t *testing.T) {
	if got := describeForeign(json.RawMessage(`{"source":"oci","image":"ghcr.io/x/y"}`)); !strings.Contains(got, "oci") {
		t.Errorf("describeForeign = %q, want the source kind", got)
	}
	if got := describeForeign(json.RawMessage(`{"weird":1}`)); got == "" {
		t.Error("an unrecognised shape described as nothing")
	}
}
