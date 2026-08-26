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
