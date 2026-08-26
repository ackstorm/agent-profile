package schema

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveReportsTheInputErrorBeforePreflightWhenBothFail is the order
// test §37 requires: input resolution is step 10, preflight is step 11. A
// profile with BOTH an undeclared input and a missing stdio MCP command must
// report the input error — never the preflight one — or someone has
// reordered the phase for convenience.
func TestResolveReportsTheInputErrorBeforePreflightWhenBothFail(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty: the runtime CLI and the MCP command both fail preflight
	dir := t.TempDir()
	write(t, dir, "p.yaml", `version: "1"
name: p
targets:
  - claude
mcps:
  memory:
    transport:
      type: stdio
      command: no-such-mcp-command
model:
  auth:
    type: bearer
    value_from:
      secret: undeclared-secret
`)
	_, _, err := Resolve(filepath.Join(dir, "p.yaml"), "claude", false)
	if err == nil {
		t.Fatal("resolve on a broken profile = nil error, want error")
	}
	if !strings.Contains(err.Error(), "undeclared-secret") {
		t.Errorf("err = %v, want the INPUT error (undeclared-secret), not the preflight one", err)
	}
	if strings.Contains(err.Error(), "no-such-mcp-command") {
		t.Errorf("err = %v, named the preflight failure; input resolution must fail first", err)
	}
}

func TestResolveRunsPreflightWhenInputsAreClean(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "p.yaml", `version: "1"
name: p
targets:
  - claude
mcps:
  memory:
    transport:
      type: stdio
      command: no-such-mcp-command
`)
	binDir := t.TempDir()
	stubBin(t, binDir, "claude")
	t.Setenv("PATH", binDir)
	_, _, err := Resolve(filepath.Join(dir, "p.yaml"), "claude", false)
	if err == nil || !strings.Contains(err.Error(), "no-such-mcp-command") {
		t.Errorf("err = %v, want the preflight error naming the missing command", err)
	}
}

func TestResolveSucceedsAndReturnsTheResolvedInputs(t *testing.T) {
	dir := t.TempDir()
	binDir := t.TempDir()
	stubBin(t, binDir, "claude")
	t.Setenv("PATH", binDir)
	t.Setenv("LLM_TOKEN", "sekret")
	write(t, dir, "p.yaml", `version: "1"
name: p
targets:
  - claude
inputs:
  secrets:
    llm-token:
      env: LLM_TOKEN
model:
  auth:
    type: bearer
    value_from:
      secret: llm-token
`)
	res, resolved, err := Resolve(filepath.Join(dir, "p.yaml"), "claude", false)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Runtime != "claude" {
		t.Errorf("runtime = %q", res.Runtime)
	}
	if len(res.Refs) != 1 || res.Refs[0].Name != "llm-token" {
		t.Errorf("refs = %v", res.Refs)
	}
	v, ok := resolved.Value("secret", "llm-token")
	if !ok || v != "sekret" {
		t.Errorf("resolved value = %q, %v; want the env value", v, ok)
	}
}

// TestResolveStrictPromotesALocallyDeclaredNonTargetWarningToAnError is F3:
// §7.2's warning is ordinary without --strict and an error with it.
func TestResolveStrictPromotesALocallyDeclaredNonTargetWarningToAnError(t *testing.T) {
	dir := t.TempDir()
	binDir := t.TempDir()
	stubBin(t, binDir, "claude")
	t.Setenv("PATH", binDir)
	write(t, dir, "p.yaml", `version: "1"
name: p
targets:
  - claude
runtimes:
  opencode:
    environment:
      A: b
`)
	res, _, err := Resolve(filepath.Join(dir, "p.yaml"), "claude", false)
	if err != nil {
		t.Fatalf("resolve without strict: %v", err)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("warnings = %v, want one", res.Warnings)
	}

	_, _, err = Resolve(filepath.Join(dir, "p.yaml"), "claude", true)
	if err == nil {
		t.Fatal("resolve with strict on a profile with a warning = nil error, want error")
	}
	if !strings.Contains(err.Error(), "opencode") {
		t.Errorf("strict error %q does not name the offending runtime block", err)
	}
}

func TestResolveDoesNotMutateAnything(t *testing.T) {
	dir := t.TempDir()
	binDir := t.TempDir()
	stubBin(t, binDir, "claude")
	t.Setenv("PATH", binDir)
	write(t, dir, "p.yaml", "version: \"1\"\nname: p\ntargets:\n  - claude\n")

	before, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Resolve(filepath.Join(dir, "p.yaml"), "claude", false); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	after, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Errorf("resolve changed the directory listing: before=%v after=%v", before, after)
	}
}
