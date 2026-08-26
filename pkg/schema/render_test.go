package schema

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderNeverEmitsASecretBinding(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "p.yaml", `version: "1"
name: p
targets:
  - claude
inputs:
  secrets:
    llm-token:
      env: LITELLM_TOKEN
model:
  type: anthropic
  auth:
    type: bearer
    value_from:
      secret: llm-token
`)
	t.Setenv("LITELLM_TOKEN", "sk-do-not-print-me")
	p, _, err := Effective(filepath.Join(dir, "p.yaml"), "claude")
	if err != nil {
		t.Fatal(err)
	}
	out := string(Render(p))
	if strings.Contains(out, "sk-do-not-print-me") {
		t.Fatal("render emitted a secret VALUE")
	}
	// The binding name is structure, not a secret, and is what makes render
	// useful for debugging. The variable NAME is the reference §34 materializes.
	if !strings.Contains(out, "llm-token") {
		t.Error("render dropped the secret's logical name; §31 asks for the effective profile")
	}
}

// TestRenderOmitsRuntimesForAManifestThatDeclaredNone is the regression for
// Effective unconditionally setting p.Runtimes = map[string]Runtime{runtime:
// p.Runtimes[runtime]}: a manifest with no runtimes: block at all got one
// invented — an empty `runtimes:\n  claude:` — because a zero-value Runtime
// is still a map entry.
func TestRenderOmitsRuntimesForAManifestThatDeclaredNone(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "p.yaml", "version: \"1\"\nname: p\ntargets:\n  - claude\n")
	p, _, err := Effective(filepath.Join(dir, "p.yaml"), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if p.Runtimes != nil {
		t.Errorf("Effective invented a runtimes block: %#v", p.Runtimes)
	}
	out := string(Render(p))
	if strings.Contains(out, "runtimes:") {
		t.Errorf("render emitted a runtimes: key for a manifest that declared none:\n%s", out)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "p.yaml", "version: \"1\"\nname: p\ntargets:\n  - claude\nskills:\n  b:\n    ref: b@m\n  a:\n    ref: a@m\n")
	p, _, _ := Effective(filepath.Join(dir, "p.yaml"), "claude")
	first := string(Render(p))
	for i := 0; i < 20; i++ {
		if got := string(Render(p)); got != first {
			t.Fatalf("render is not deterministic:\n%s\n---\n%s", first, got)
		}
	}
	if strings.Index(first, "a:") > strings.Index(first, "b:") {
		t.Error("collection keys are not sorted; map order leaked into the output")
	}
}

// A source family render does not know about is a SILENT drop, which §8
// forbids outright — and render's output is what a producer in another
// repository reads to see what its manifest means. archive was added to the
// union after this renderer was written; without this, it would have printed
// a resource with `source:` and nothing under it.
func TestRenderEmitsEverySourceFamily(t *testing.T) {
	dir := t.TempDir()
	digest := "sha256:" + strings.Repeat("c", 64)
	write(t, dir, "p.yaml", `version: "1"
name: p
targets:
  - claude
inputs:
  secrets:
    ach-key:
      env: ACH_KEY
skills:
  from-git:
    source:
      git:
        url: https://github.com/o/r.git
  from-local:
    source:
      local:
        path: ./here
  from-archive:
    source:
      archive:
        url: https://ach/c/9f2a/skill.tar.gz
        digest: `+digest+`
        subpath: review
        auth:
          scheme: bearer
          value_from:
            secret: ach-key
`)
	p, _, err := Effective(filepath.Join(dir, "p.yaml"), "claude")
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	out := string(Render(p))
	for _, want := range []string{"git:", "local:", "archive:", digest, "scheme: bearer", "secret: ach-key"} {
		if !strings.Contains(out, want) {
			t.Errorf("render dropped %q\n%s", want, out)
		}
	}
}
