package source

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/pkg/schema"
)

// specFixture is pkg/schema's §35 fixture, read rather than copied.
//
// A second copy is exactly how the auth block's shape drifted once already:
// spec-35-execute.yaml had been written to match the decoder instead of the
// document it is named after, and the one fixture whose whole job is to be
// §35's bytes agreed with the bug. One file, transformed visibly at test time,
// cannot do that.
const specFixture = "../schema/testdata/spec-35-execute.yaml"

var remoteGitURL = regexp.MustCompile(`https://[^\s]+\.git`)

// offlineFixture rewrites every remote git URL in §35 to a local repository
// and writes the result beside the assets it references. The transformation is
// returned so a test can assert what it changed.
func offlineFixture(t *testing.T, stripAuth bool) (path string, dir string) {
	t.Helper()
	raw, err := os.ReadFile(specFixture)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, "value_from:") {
		t.Fatal("the §35 fixture carries no auth block; the transformation below is asserting nothing")
	}

	repo := newBareRepo(t, map[string]string{
		"skills/pdf/SKILL.md":             "# pdf",
		"skills/executing-plans/SKILL.md": "# executing-plans",
		"review/SKILL.md":                 "# company-review",
		// §35's catalogue, with a same-repo entry: a bare relative string,
		// which is how the real ones are built — one clone, N plugins.
		".claude-plugin/marketplace.json":                `{"plugins":[{"name":"code-review","source":"./plugins/code-review"}]}`,
		"plugins/code-review/.claude-plugin/plugin.json": `{"name":"code-review"}`,
	})
	body = remoteGitURL.ReplaceAllString(body, repo)

	if stripAuth {
		// A credential may not cross a non-TLS transport (§17.2), and a local
		// path is not https. Removing the auth blocks is what makes the rest
		// of §35 resolvable offline; the guard itself is asserted by the
		// sibling test below, end to end through a real profile.
		body = regexp.MustCompile(`(?m)^\s*auth:\n(?:\s+(?:scheme|value_from|secret):.*\n)+`).ReplaceAllString(body, "")
	}

	dir = t.TempDir()
	// §35 extends a base, and extends resolves in the MANIFEST directory with
	// no search paths (§5.2) — so the parent has to travel with the child.
	base, err := os.ReadFile(filepath.Join(filepath.Dir(specFixture), "spec-35-coding-base.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spec-35-coding-base.yaml"), base, 0o600); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "p.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// §35's artifact is a local ./AGENTS.md relative to the manifest.
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# agents"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, dir
}

func TestTheSpecsFullExampleResolvesOffline(t *testing.T) {
	path, dir := offlineFixture(t, true)
	p, _, err := schema.Effective(path, "claude")
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	got, reports, err := Resolve(t.Context(), p, Opts{
		Cache: mustCache(t), ManifestDir: dir,
		Secret: func(string) (string, error) { return "", ErrSecretUnset },
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("nothing resolved")
	}
	// Every family §35 exercises, by name, so a family silently dropping out
	// of the walk shows up here.
	for _, want := range []string{
		"marketplace anthropic-skills",
		"marketplace acme-plugins",
		"skill executing-plans",
		"skill company-review",
		"artifact agents-md",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("%s did not resolve; got %v", want, keysOf(got))
		}
	}
	// §35's two refs resolve against the catalogues the same manifest
	// declares — a path join inside a tree already fetched, no second hop.
	for _, ref := range []string{"skill pdf", "plugin code-review"} {
		if _, ok := got[ref]; !ok {
			t.Errorf("%s did not resolve; got %v", ref, keysOf(got))
		}
	}
	for _, r := range reports {
		t.Logf("%s: %s", r.Resource, r.Text)
	}
}

// The same fixture with §35's auth blocks intact. The guard must fire through
// a REAL profile, not only through a hand-built GitSpec: the unit test proves
// CheckCredentialTransport refuses, this proves nothing between the manifest
// and the fetch quietly routes around it.
func TestTheSpecsPrivateSourcesRefuseACredentialOverANonTLSTransport(t *testing.T) {
	path, dir := offlineFixture(t, false)
	p, _, err := schema.Effective(path, "claude")
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	_, _, err = Resolve(t.Context(), p, Opts{
		Cache: mustCache(t), ManifestDir: dir,
		Secret: func(string) (string, error) { return "glpat-SECRET", nil },
	})
	if err == nil {
		t.Fatal("a credential was sent over a local, non-TLS transport")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("error %q does not name the rule it enforced", err)
	}
	if strings.Contains(err.Error(), "glpat-SECRET") {
		t.Errorf("the error embeds the credential: %v", err)
	}
}
