package schema

import (
	"path/filepath"
	"testing"
)

// TestRequiredSkipsInputsOfADisabledResource is the spec's §12 worked
// example: a company-review skill references a secret through its git
// source's auth, and the opencode runtime disables that skill. For claude
// the secret is required; for opencode it is not, because Required only
// walks active resources.
func TestRequiredSkipsInputsOfADisabledResource(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "p.yaml", `version: "1"
name: p
targets:
  - claude
  - opencode
skills:
  company-review:
    source:
      git:
        url: https://gitlab.company.com/skills.git
        auth:
          value_from:
            secret: gitlab-token
runtimes:
  opencode:
    skills:
      company-review:
        enabled: false
`)

	claude, _, err := Effective(filepath.Join(dir, "p.yaml"), "claude")
	if err != nil {
		t.Fatalf("effective(claude): %v", err)
	}
	refs := Required(claude)
	if len(refs) != 1 || refs[0].Resource != "skills.company-review" || refs[0].Kind != "secret" || refs[0].Name != "gitlab-token" {
		t.Fatalf("claude refs = %+v, want one ref for skills.company-review/secret/gitlab-token", refs)
	}

	opencode, _, err := Effective(filepath.Join(dir, "p.yaml"), "opencode")
	if err != nil {
		t.Fatalf("effective(opencode): %v", err)
	}
	if refs := Required(opencode); len(refs) != 0 {
		t.Errorf("opencode refs = %+v, want none: the skill is disabled", refs)
	}
}

// TestRequiredCollectsEveryReferenceShape covers the other places §12 says a
// reference can appear: a header's value_from on model.headers and on an MCP
// transport's headers, and model.auth.value_from — plus a deterministic,
// sorted return order.
func TestRequiredCollectsEveryReferenceShape(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "p.yaml", `version: "1"
name: p
targets:
  - claude
model:
  type: anthropic
  auth:
    type: bearer
    value_from:
      secret: model-token
  headers:
    X-Team:
      value_from:
        variable: team-id
mcps:
  memory:
    transport:
      type: http
      url: https://mcp.example.com
      headers:
        X-Api-Key:
          value_from:
            secret: mcp-token
`)
	p, _, err := Effective(filepath.Join(dir, "p.yaml"), "claude")
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	refs := Required(p)
	if len(refs) != 3 {
		t.Fatalf("refs = %+v, want 3", refs)
	}
	want := []Ref{
		{Resource: "mcps.memory", Kind: "secret", Name: "mcp-token"},
		{Resource: "model", Kind: "secret", Name: "model-token"},
		{Resource: "model", Kind: "variable", Name: "team-id"},
	}
	for i, w := range want {
		if refs[i].Resource != w.Resource || refs[i].Kind != w.Kind || refs[i].Name != w.Name {
			t.Errorf("refs[%d] = %+v, want %+v", i, refs[i], w)
		}
	}
}

func TestRequiredReturnsNothingForAProfileWithNoReferences(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "p.yaml", "version: \"1\"\nname: p\ntargets:\n  - claude\n")
	p, _, err := Effective(filepath.Join(dir, "p.yaml"), "claude")
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if refs := Required(p); len(refs) != 0 {
		t.Errorf("refs = %+v, want none", refs)
	}
}
